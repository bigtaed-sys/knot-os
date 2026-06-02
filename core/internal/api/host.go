package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/events"
	"github.com/knot-os/knot-os/core/internal/plugin"
)

// pluginRuntime is the slice of the plugin supervisor the API needs:
// attribute a host-API call to a plugin (by token) and read a
// plugin's process status (for the proxy + the /api/plugins list).
// An interface so the HTTP layer is testable without spawning real
// processes; *plugin.Supervisor is the production implementation.
type pluginRuntime interface {
	PluginForToken(token string) (string, bool)
	Status(id string) (plugin.ProcStatus, bool)
}

// SetPluginSupervisor wires the plugin process supervisor into the
// API server. Needed for the host API (token → plugin attribution)
// and for surfacing runtime status on /api/plugins.
func (s *Server) SetPluginSupervisor(sup *plugin.Supervisor) { s.pluginSup = sup }

// pluginCtxKey carries the calling plugin's id through the host-API
// request context after token auth.
type pluginCtxKey struct{}

// HostAPIHandler returns the http.Handler knotd serves on its host
// Unix socket (KNOT_HOST_SOCKET). Plugins call it to read controlled
// slices of router state. Every request is authenticated by the
// per-plugin bearer token the supervisor minted (KNOT_HOST_TOKEN);
// each endpoint is gated by a permission the plugin must declare in
// its manifest, so a plugin can only reach what it asked for.
//
// This handler is NOT mounted on the LAN-facing router — it lives on
// a root-owned loopback Unix socket only plugin processes can reach.
func (s *Server) HostAPIHandler() http.Handler {
	r := chi.NewRouter()
	r.Use(jsonContentType)
	r.Use(s.pluginTokenAuth)

	// whoami needs no permission — lets a plugin discover its own id
	// and granted scopes for diagnostics.
	r.Get("/host/v1/whoami", s.hostWhoami)
	r.With(s.requirePerm("status:read")).Get("/host/v1/status", s.hostStatus)
	r.With(s.requirePerm("devices:read")).Get("/host/v1/devices", s.hostDevices)
	// Write + reactive scopes (M2).
	r.With(s.requirePerm("devices:write")).Post("/host/v1/devices/{mac}/profile", s.hostSetDeviceProfile)
	r.With(s.requirePerm("events:read")).Get("/host/v1/events", s.hostEvents)
	return r
}

// pluginTokenAuth authenticates a host-API request by its bearer
// token and stashes the owning plugin id in the request context.
func (s *Server) pluginTokenAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.pluginSup == nil {
			writeError(w, http.StatusServiceUnavailable, "host_disabled", "plugin host not configured")
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		id, ok := s.pluginSup.PluginForToken(token)
		if !ok {
			writeError(w, http.StatusUnauthorized, "bad_token", "invalid or expired plugin token")
			return
		}
		ctx := context.WithValue(r.Context(), pluginCtxKey{}, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// requirePerm gates an endpoint on a manifest-declared permission.
func (s *Server) requirePerm(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id, _ := r.Context().Value(pluginCtxKey{}).(string)
			p, ok := s.plugins.Get(id)
			if !ok || !p.Grants(perm) {
				writeError(w, http.StatusForbidden, "permission_denied",
					"plugin "+id+" lacks permission "+perm)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Server) hostWhoami(w http.ResponseWriter, r *http.Request) {
	id, _ := r.Context().Value(pluginCtxKey{}).(string)
	p, _ := s.plugins.Get(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"plugin_id":   id,
		"permissions": p.Permissions,
	})
}

func (s *Server) hostStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.Snapshot()
	out := map[string]any{
		"role":        string(cfg.Role),
		"device_name": cfg.Device.Name,
		"version":     s.version,
	}
	if s.backend != nil {
		if st, err := s.backend.Status(r.Context()); err == nil && st.WAN != nil {
			out["wan_up"] = st.WAN.Up
			out["wan_ip"] = st.WAN.IP
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// hostSetDeviceProfile lets a plugin reassign a device to a profile —
// the write counterpart of /host/v1/devices. Same effects as the LAN
// UI: scheduler kick + routing rebuild + a bus event.
func (s *Server) hostSetDeviceProfile(w http.ResponseWriter, r *http.Request) {
	mac := macParam(r)
	var body struct {
		ProfileID string `json:"profile_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	updated, err := s.applyDeviceProfile(mac, body.ProfileID)
	if err != nil {
		writeError(w, http.StatusNotFound, "device_not_found", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, toJSON(updated, time.Now()))
}

// hostEvents streams router events to a plugin as Server-Sent Events.
// The plugin subscribes once and reacts to device-joined, WAN
// up/down, profile changes, etc. A heartbeat comment keeps the
// connection from idling out; the subscription is dropped when the
// plugin disconnects.
func (s *Server) hostEvents(w http.ResponseWriter, r *http.Request) {
	if s.eventBus == nil {
		writeError(w, http.StatusServiceUnavailable, "events_disabled", "event bus not configured")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "no_streaming", "response writer does not support streaming")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Bounded buffer: a slow plugin drops events rather than blocking
	// the bus (each bus dispatch is its own goroutine, but we don't
	// want them piling up if the plugin stalls).
	ch := make(chan events.Event, 64)
	subID := s.eventBus.Subscribe(func(_ context.Context, ev events.Event) {
		select {
		case ch <- ev:
		default:
		}
	})
	defer s.eventBus.Unsubscribe(subID)

	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev := <-ch:
			payload, _ := json.Marshal(map[string]any{
				"kind":    ev.Kind,
				"when":    ev.When,
				"payload": ev.Payload,
			})
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, payload)
			flusher.Flush()
		}
	}
}

func (s *Server) hostDevices(w http.ResponseWriter, _ *http.Request) {
	if s.devices == nil {
		writeJSON(w, http.StatusOK, map[string]any{"devices": []any{}})
		return
	}
	now := time.Now()
	all := s.devices.List()
	out := make([]map[string]any, 0, len(all))
	for _, d := range all {
		out = append(out, map[string]any{
			"mac":    d.MAC,
			"label":  d.Label(),
			"ip":     d.IP,
			"online": d.Online(now),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": out})
}
