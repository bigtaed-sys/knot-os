package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/notify"
	"github.com/knot-os/knot-os/core/internal/tgproxy"
)

// MountNotify registers /notify/* under the auth-gated group.
//
//	GET    /notify/telegram                   — current bot status + linked chats
//	PUT    /notify/telegram/token             — set/update the bot token
//	POST   /notify/telegram/restart           — restart the bot loop after a token change
//	POST   /notify/telegram/pin               — issue a one-time PIN for chat linking
//	GET    /notify/telegram/pin               — pending PIN status (active + expiresAt)
//	DELETE /notify/telegram/chats/{chatID}    — unlink a chat
//	PATCH  /notify/telegram/primary-lang      — set default new-chat language
func (s *Server) MountNotify(r chi.Router) {
	if s.notify == nil {
		r.Get("/notify/telegram", func(w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusServiceUnavailable, "notify_disabled", "notification subsystem not configured")
		})
		return
	}
	r.Get("/notify/telegram", s.handleNotifyGet)
	r.Put("/notify/telegram/token", s.handleNotifyPutToken)
	r.Put("/notify/telegram/app", s.handleNotifyPutApp)
	r.Post("/notify/telegram/restart", s.handleNotifyRestart)
	r.Post("/notify/telegram/pin", s.handleNotifyIssuePIN)
	r.Get("/notify/telegram/pin", s.handleNotifyPINStatus)
	r.Delete("/notify/telegram/chats/{chatID}", s.handleNotifyUnlinkChat)
	r.Patch("/notify/telegram/primary-lang", s.handleNotifySetPrimaryLang)
}

// SetNotifyServices wires the store + bot into the API. Pass nil
// (or skip the call) and every endpoint responds 503.
func (s *Server) SetNotifyServices(store *notify.Store, bot *notify.Bot) {
	s.notify = store
	s.notifyBot = bot
}

// notifyStateJSON masks the bot token. The store's Snapshot()
// includes it for internal callers, but it never leaves the API.
type notifyStateJSON struct {
	BotConfigured bool                `json:"bot_configured"`
	BotUsername   string              `json:"bot_username,omitempty"`
	PrimaryLang   string              `json:"primary_lang"`
	Chats         []notify.LinkedChat `json:"chats"`
	// MTProto transport: app_id/app_hash configured (app_hash never
	// leaves the API), and whether the local Telegram proxy is on (so the
	// UI can hint that the bot will dial through it).
	AppConfigured bool  `json:"app_configured"`
	AppID         int32 `json:"app_id,omitempty"`
	ProxyEnabled  bool  `json:"proxy_enabled"`
}

func (s *Server) handleNotifyGet(w http.ResponseWriter, _ *http.Request) {
	st := s.notify.Snapshot()
	cfg := s.Snapshot()
	writeJSON(w, http.StatusOK, notifyStateJSON{
		BotConfigured: st.BotToken != "",
		BotUsername:   st.BotUsername,
		PrimaryLang:   st.PrimaryLang,
		Chats:         st.Chats,
		AppConfigured: st.AppID > 0 && st.AppHash != "",
		AppID:         st.AppID,
		ProxyEnabled:  cfg.Network.TGProxy != nil && cfg.Network.TGProxy.Enabled,
	})
}

// handleNotifyPutApp sets/clears the MTProto app credentials and restarts
// the bot so it switches transport.
func (s *Server) handleNotifyPutApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AppID   int32  `json:"app_id"`
		AppHash string `json:"app_hash"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.notify.SetAppCredentials(body.AppID, body.AppHash); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	if s.notifyBot != nil {
		// Refresh the proxy route from the current config before restart.
		s.notifyBot.SetMTProxy(s.botMTProxyURL(), "/var/lib/knot/notify-mtproto.session")
		if err := s.notifyBot.Restart(r.Context()); err != nil {
			writeError(w, http.StatusBadRequest, "bot_start_failed", err.Error())
			return
		}
	}
	s.handleNotifyGet(w, r)
}

// botMTProxyURL builds the loopback tg:// proxy URL when the local
// Telegram proxy is enabled, mirroring cmd/knotd.
func (s *Server) botMTProxyURL() string {
	t := s.Snapshot().Network.TGProxy
	if t == nil || !t.Enabled || t.Secret == "" {
		return ""
	}
	return tgproxy.TGLink(tgproxy.Settings{Port: t.Port, Secret: t.Secret, LinkIP: "127.0.0.1"})
}

type tokenRequest struct {
	Token string `json:"token"`
}

func (s *Server) handleNotifyPutToken(w http.ResponseWriter, r *http.Request) {
	var body tokenRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.notify.SetBotToken(body.Token); err != nil {
		writeError(w, http.StatusInternalServerError, "save_failed", err.Error())
		return
	}
	// Restart the bot loop so it picks up the new token + does
	// /getMe to populate the username.
	if s.notifyBot != nil {
		if err := s.notifyBot.Restart(r.Context()); err != nil {
			writeError(w, http.StatusBadRequest, "bot_start_failed", err.Error())
			return
		}
	}
	s.handleNotifyGet(w, r)
}

func (s *Server) handleNotifyRestart(w http.ResponseWriter, r *http.Request) {
	if s.notifyBot == nil {
		writeError(w, http.StatusServiceUnavailable, "no_bot", "bot not constructed")
		return
	}
	if err := s.notifyBot.Restart(r.Context()); err != nil {
		writeError(w, http.StatusBadRequest, "restart_failed", err.Error())
		return
	}
	s.handleNotifyGet(w, r)
}

type pinResponse struct {
	PIN       string `json:"pin"`
	ExpiresAt string `json:"expires_at"`
}

func (s *Server) handleNotifyIssuePIN(w http.ResponseWriter, _ *http.Request) {
	pin, err := s.notify.IssuePIN()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "pin_failed", err.Error())
		return
	}
	_, expiresAt := s.notify.PendingPIN()
	writeJSON(w, http.StatusOK, pinResponse{
		PIN:       pin,
		ExpiresAt: expiresAt.UTC().Format("2006-01-02T15:04:05Z"),
	})
}

type pinStatusResponse struct {
	Active    bool   `json:"active"`
	ExpiresAt string `json:"expires_at,omitempty"`
}

func (s *Server) handleNotifyPINStatus(w http.ResponseWriter, _ *http.Request) {
	active, expiresAt := s.notify.PendingPIN()
	resp := pinStatusResponse{Active: active}
	if active {
		resp.ExpiresAt = expiresAt.UTC().Format("2006-01-02T15:04:05Z")
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleNotifyUnlinkChat(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "chatID")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_chat_id", err.Error())
		return
	}
	if err := s.notify.Unlink(id); err != nil {
		writeError(w, http.StatusInternalServerError, "unlink_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type primaryLangRequest struct {
	Lang string `json:"lang"`
}

func (s *Server) handleNotifySetPrimaryLang(w http.ResponseWriter, r *http.Request) {
	var body primaryLangRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	if err := s.notify.SetPrimaryLang(body.Lang); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "invalid_lang", err.Error())
		return
	}
	s.handleNotifyGet(w, r)
}
