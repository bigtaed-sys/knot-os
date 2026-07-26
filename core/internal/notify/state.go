// Package notify is the Telegram bot + future webhook hooks. v0.4
// ships with the Telegram half: a long-polling bot that delivers
// notifications when the event bus fires interesting things AND
// accepts commands + inline-keyboard taps to drive the router from
// the chat.
//
// State (bot token, linked chats, per-chat language preference)
// lives at /etc/knot/notify.yaml. The bot token is sensitive and
// goes through the secrets sealer when the wrapper is present
// (production); falls back to plain in -dev for editability.
package notify

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// LinkedChat is one Telegram chat that the bot accepts commands
// from and pushes notifications to.
type LinkedChat struct {
	// ChatID is the Telegram chat (== user, in the private-chat
	// case knotd targets). Stable identifier.
	ChatID int64 `yaml:"chat_id" json:"chat_id"`
	// Username is the Telegram @username at link time, if any —
	// strictly for human display in the UI list.
	Username string `yaml:"username,omitempty" json:"username,omitempty"`
	// FirstName + LastName are similarly for display.
	FirstName string `yaml:"first_name,omitempty" json:"first_name,omitempty"`
	LastName  string `yaml:"last_name,omitempty" json:"last_name,omitempty"`
	// Lang is "ru" or "en". Defaults to the bot's primary on link.
	Lang string `yaml:"lang" json:"lang"`
	// LinkedAt is when the user successfully entered the PIN.
	LinkedAt time.Time `yaml:"linked_at" json:"linked_at"`
}

// State is the persisted shape of /etc/knot/notify.yaml.
type State struct {
	// BotToken is the Telegram bot token (1234567890:ABC…). Empty
	// means "bot not configured" — the bot loop won't start.
	BotToken string `yaml:"bot_token,omitempty" json:"-"`
	// BotUsername is what /getMe returned at startup. Cached so
	// the UI can render "Open @YourBot" without an extra round-trip.
	BotUsername string `yaml:"bot_username,omitempty" json:"bot_username,omitempty"`
	// PrimaryLang is the language new chats default to. Either
	// "ru" or "en"; falls back to "ru" if unset.
	PrimaryLang string `yaml:"primary_lang,omitempty" json:"primary_lang,omitempty"`
	// AppID / AppHash are the my.telegram.org API credentials. When both
	// are set the bot connects over MTProto (via the local proxy) instead
	// of the HTTP Bot API — so it works where api.telegram.org is blocked.
	AppID   int32  `yaml:"app_id,omitempty" json:"app_id,omitempty"`
	AppHash string `yaml:"app_hash,omitempty" json:"-"`
	// Chats are the linked Telegram chats, keyed by chat_id.
	Chats []LinkedChat `yaml:"chats,omitempty" json:"chats,omitempty"`
}

// Sealer is the at-rest encryption interface — same one config/
// uses. Allowed nil for tests / dev.
type Sealer interface {
	Wrap(plaintext string) (string, error)
	Unwrap(stored string) (string, error)
}

// pendingPIN is one in-flight link attempt: the user opened the
// link dialog in the UI, knotd generated a PIN, the user has 5
// minutes to send it to the bot.
type pendingPIN struct {
	pin       string
	expiresAt time.Time
}

// Store owns notify state in memory + on disk. Thread-safe.
type Store struct {
	mu         sync.RWMutex
	state      State
	storePath  string
	sealer     Sealer
	pendingPIN pendingPIN
}

// Open loads state from path, creating a fresh State when the file
// is absent. Bot-token decryption uses sealer (when non-nil).
func Open(path string, sealer Sealer) (*Store, error) {
	s := &Store{storePath: path, sealer: sealer}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("notify: read %s: %w", path, err)
	}
	var st State
	if err := yaml.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("notify: parse %s: %w", path, err)
	}
	if sealer != nil && st.BotToken != "" {
		raw, err := sealer.Unwrap(st.BotToken)
		if err != nil {
			return nil, fmt.Errorf("notify: unwrap token: %w", err)
		}
		st.BotToken = raw
	}
	if st.PrimaryLang == "" {
		st.PrimaryLang = "ru"
	}
	s.state = st
	return s, nil
}

// Snapshot returns a copy of the state for read-only callers.
// BotToken is included — internal callers (the bot loop) need it.
// API handlers strip it before serialising via the JSON tag.
func (s *Store) Snapshot() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := State{
		BotToken:    s.state.BotToken,
		BotUsername: s.state.BotUsername,
		PrimaryLang: s.state.PrimaryLang,
		AppID:       s.state.AppID,
		AppHash:     s.state.AppHash,
	}
	out.Chats = append([]LinkedChat(nil), s.state.Chats...)
	return out
}

// SetAppCredentials stores the my.telegram.org app_id/app_hash (or clears
// them with 0/""), then persists. Empty credentials revert the bot to the
// HTTP Bot API transport on next start.
func (s *Store) SetAppCredentials(appID int32, appHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.AppID = appID
	s.state.AppHash = strings.TrimSpace(appHash)
	return s.saveLocked()
}

// SetBotToken stores a new token, clears the cached username (the
// bot loop is responsible for re-querying /getMe and updating it),
// and persists.
func (s *Store) SetBotToken(token string) error {
	token = strings.TrimSpace(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.BotToken = token
	s.state.BotUsername = ""
	return s.saveLocked()
}

// SetBotUsername caches the @handle returned by /getMe.
func (s *Store) SetBotUsername(name string) {
	s.mu.Lock()
	s.state.BotUsername = strings.TrimPrefix(name, "@")
	_ = s.saveLocked()
	s.mu.Unlock()
}

// SetPrimaryLang switches the default new-chat language. "ru" or
// "en"; anything else is rejected.
func (s *Store) SetPrimaryLang(lang string) error {
	if lang != "ru" && lang != "en" {
		return fmt.Errorf("primary lang must be ru or en (got %q)", lang)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state.PrimaryLang = lang
	return s.saveLocked()
}

// IssuePIN generates a 6-digit PIN for chat-linking. Returns the
// PIN (valid 5 minutes) and overwrites any previous pending PIN.
func (s *Store) IssuePIN() (string, error) {
	pin, err := newPIN()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.pendingPIN = pendingPIN{
		pin:       pin,
		expiresAt: time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()
	return pin, nil
}

// PendingPIN reports whether a PIN is currently in-flight (used by
// the API to render countdown / "expired" hints in the UI).
func (s *Store) PendingPIN() (active bool, expiresAt time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pendingPIN.pin == "" || time.Now().After(s.pendingPIN.expiresAt) {
		return false, time.Time{}
	}
	return true, s.pendingPIN.expiresAt
}

// ClaimPIN consumes the pending PIN if it matches, links the
// supplied chat info, and persists. Returns the linked LinkedChat
// on success.
func (s *Store) ClaimPIN(submitted string, chatID int64, username, firstName, lastName string) (*LinkedChat, error) {
	submitted = strings.TrimSpace(submitted)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingPIN.pin == "" {
		return nil, errors.New("no pending PIN — generate one in the System page first")
	}
	if time.Now().After(s.pendingPIN.expiresAt) {
		s.pendingPIN = pendingPIN{}
		return nil, errors.New("PIN expired — generate a new one in the System page")
	}
	if submitted != s.pendingPIN.pin {
		return nil, errors.New("PIN does not match")
	}
	// Already linked?
	for i, c := range s.state.Chats {
		if c.ChatID == chatID {
			// Update display fields, preserve LinkedAt + Lang.
			s.state.Chats[i].Username = username
			s.state.Chats[i].FirstName = firstName
			s.state.Chats[i].LastName = lastName
			s.pendingPIN = pendingPIN{}
			if err := s.saveLocked(); err != nil {
				return nil, err
			}
			out := s.state.Chats[i]
			return &out, nil
		}
	}
	link := LinkedChat{
		ChatID:    chatID,
		Username:  username,
		FirstName: firstName,
		LastName:  lastName,
		Lang:      s.state.PrimaryLang,
		LinkedAt:  time.Now(),
	}
	if link.Lang == "" {
		link.Lang = "ru"
	}
	s.state.Chats = append(s.state.Chats, link)
	s.pendingPIN = pendingPIN{}
	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	out := link
	return &out, nil
}

// SetChatLang updates one chat's language preference.
func (s *Store) SetChatLang(chatID int64, lang string) error {
	if lang != "ru" && lang != "en" {
		return fmt.Errorf("lang must be ru or en (got %q)", lang)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.state.Chats {
		if s.state.Chats[i].ChatID == chatID {
			s.state.Chats[i].Lang = lang
			return s.saveLocked()
		}
	}
	return fmt.Errorf("chat %d not linked", chatID)
}

// Unlink removes a chat from the linked set. Idempotent.
func (s *Store) Unlink(chatID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]LinkedChat, 0, len(s.state.Chats))
	for _, c := range s.state.Chats {
		if c.ChatID != chatID {
			out = append(out, c)
		}
	}
	s.state.Chats = out
	return s.saveLocked()
}

// IsLinked reports whether a chat_id is on the allowlist.
func (s *Store) IsLinked(chatID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.state.Chats {
		if c.ChatID == chatID {
			return true
		}
	}
	return false
}

// LangFor returns the language of a linked chat, or PrimaryLang as
// fallback for unknown / unlinked.
func (s *Store) LangFor(chatID int64) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, c := range s.state.Chats {
		if c.ChatID == chatID {
			return c.Lang
		}
	}
	if s.state.PrimaryLang != "" {
		return s.state.PrimaryLang
	}
	return "ru"
}

// AllLinked returns a copy of the linked-chat list.
func (s *Store) AllLinked() []LinkedChat {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]LinkedChat(nil), s.state.Chats...)
}

func (s *Store) saveLocked() error {
	if s.storePath == "" {
		return nil
	}
	dir := filepath.Dir(s.storePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out := State{
		BotToken:    s.state.BotToken,
		BotUsername: s.state.BotUsername,
		PrimaryLang: s.state.PrimaryLang,
		Chats:       s.state.Chats,
	}
	if s.sealer != nil && out.BotToken != "" {
		wrapped, err := s.sealer.Wrap(out.BotToken)
		if err != nil {
			return fmt.Errorf("wrap token: %w", err)
		}
		out.BotToken = wrapped
	}
	data, err := yaml.Marshal(&out)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".notify-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	return os.Rename(tmpName, s.storePath)
}

// newPIN returns a 6-digit numeric PIN with leading-zero tolerance
// ("000123" is a valid display) using crypto/rand for entropy.
func newPIN() (string, error) {
	max := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
