package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SessionTTL is how long a login persists. Optionally persisted to
// disk via NewSessionsAt — without that, sessions live in memory and
// reset on knotd restart (which until v0.3 was the only behaviour
// and worked fine for an extender, but felt rough whenever the
// auto-update flow restarted the daemon out from under the user).
const SessionTTL = 24 * time.Hour

// Session is the server-side state behind a session cookie.
type Session struct {
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Sessions stores active session tokens. The zero value is not usable;
// construct via NewSessions or NewSessionsAt.
type Sessions struct {
	mu        sync.Mutex
	tokens    map[string]Session
	now       func() time.Time

	// Optional on-disk persistence. When storePath is non-empty we
	// flush the table to disk after every mutation; on startup
	// NewSessionsAt loads the file (if present) so a knotd restart
	// keeps users logged in.
	storePath string
}

// NewSessions returns an empty in-memory session store.
func NewSessions() *Sessions {
	return &Sessions{
		tokens: make(map[string]Session),
		now:    time.Now,
	}
}

// NewSessionsAt returns a session store backed by a JSON file at
// path. Loads existing sessions on construction, drops expired ones
// up front, and atomically rewrites the file after every mutation.
//
// A missing file is fine — first-run state. A malformed file is
// logged via the returned error but the store still comes up with
// an empty table; we don't want a corrupted persisted-sessions file
// to make the daemon unbootable.
func NewSessionsAt(path string) (*Sessions, error) {
	s := &Sessions{
		tokens:    make(map[string]Session),
		now:       time.Now,
		storePath: path,
	}
	if err := s.loadLocked(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	return s, nil
}

func (s *Sessions) loadLocked() error {
	if s.storePath == "" {
		return nil
	}
	b, err := os.ReadFile(s.storePath)
	if err != nil {
		return err
	}
	var raw map[string]Session
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", s.storePath, err)
	}
	now := s.now()
	for tok, sess := range raw {
		if now.Before(sess.ExpiresAt) {
			s.tokens[tok] = sess
		}
	}
	return nil
}

// flushLocked writes the current token map to storePath. Caller
// must hold s.mu. Atomic via temp+rename. Best-effort: a write
// failure is non-fatal — the in-memory state is still authoritative
// and the next mutation will retry.
func (s *Sessions) flushLocked() {
	if s.storePath == "" {
		return
	}
	dir := filepath.Dir(s.storePath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(dir, ".sessions-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s.tokens); err != nil {
		_ = tmp.Close()
		cleanup()
		return
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return
	}
	_ = os.Rename(tmpName, s.storePath)
}

// Issue creates a fresh session and returns its token. The caller is
// responsible for setting it as a cookie on the response.
func (s *Sessions) Issue() (Session, error) {
	token, err := generateToken()
	if err != nil {
		return Session{}, err
	}
	now := s.now()
	sess := Session{
		Token:     token,
		CreatedAt: now,
		ExpiresAt: now.Add(SessionTTL),
	}
	s.mu.Lock()
	s.tokens[token] = sess
	s.flushLocked()
	s.mu.Unlock()
	return sess, nil
}

// Lookup returns the session for token, or false if it does not exist or
// has expired. Expired sessions are evicted as a side effect.
func (s *Sessions) Lookup(token string) (Session, bool) {
	if token == "" {
		return Session{}, false
	}
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.tokens[token]
	if !ok {
		return Session{}, false
	}
	if !now.Before(sess.ExpiresAt) {
		delete(s.tokens, token)
		s.flushLocked()
		return Session{}, false
	}
	return sess, true
}

// Revoke removes a session, e.g. on logout. It is safe to call with an
// unknown token.
func (s *Sessions) Revoke(token string) {
	s.mu.Lock()
	delete(s.tokens, token)
	s.flushLocked()
	s.mu.Unlock()
}

// RevokeAll wipes every session. Used when the password changes — any
// existing sessions should no longer be trusted.
func (s *Sessions) RevokeAll() {
	s.mu.Lock()
	s.tokens = make(map[string]Session)
	s.flushLocked()
	s.mu.Unlock()
}

// Count returns the number of currently held (not necessarily unexpired)
// sessions. Useful for tests and metrics.
func (s *Sessions) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tokens)
}
