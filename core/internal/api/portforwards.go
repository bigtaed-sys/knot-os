package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/applycoord"
	"github.com/knot-os/knot-os/core/internal/config"
)

// MountPortForwards registers the inbound-DNAT endpoints:
//
//	GET /portforwards — list current rules
//	PUT /portforwards — replace the whole rule set (transactional apply)
//
// Rules live in config.Network.PortForwards, so a change runs through
// the same snapshot + health-check + auto-rollback path as any other
// config edit.
func (s *Server) MountPortForwards(r chi.Router) {
	r.Get("/portforwards", s.handleListPortForwards)
	r.Put("/portforwards", s.handlePutPortForwards)
}

func (s *Server) handleListPortForwards(w http.ResponseWriter, _ *http.Request) {
	cfg := s.Snapshot()
	pfs := cfg.Network.PortForwards
	if pfs == nil {
		pfs = []config.PortForward{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"port_forwards": pfs,
		// router_mode tells the UI whether forwards will actually take
		// effect — the extender role has no WAN of its own.
		"router_mode": cfg.Role == config.RoleWiFiRouter,
	})
}

func (s *Server) handlePutPortForwards(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PortForwards []config.PortForward `json:"port_forwards"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", err.Error())
		return
	}
	// Assign stable IDs to any new rules so the UI can address them.
	for i := range body.PortForwards {
		if body.PortForwards[i].ID == "" {
			body.PortForwards[i].ID = genPortForwardID()
		}
	}

	incoming := s.Snapshot()
	incoming.Network.PortForwards = body.PortForwards

	status, payload := s.commitConfig(r.Context(), incoming, "api:put-portforwards")
	writeJSON(w, status, payload)
}

// commitConfig runs the transactional apply path shared by the
// dedicated config-section endpoints. It preserves the in-memory auth
// hash (never sent over the wire), validates, and either drives the
// apply coordinator (snapshot + health-check + auto-rollback) or, when
// no coordinator is wired, falls back to the legacy direct apply.
//
// persistConfigLight saves a config change and reconciles the soft
// subsystems (DNS, routing, zapret) WITHOUT going through backend.Apply.
// backend.Apply re-applies the whole network stack — restarting hostapd
// and dropping every Wi-Fi client — which is the right thing for changes
// that touch interfaces/AP/NAT, but wrong for changes that only drive a
// side daemon (zapret's nfqws + its own nft table). Mirrors how the
// profiles/devices endpoints persist + fireConfigApplied without a
// network bounce.
func (s *Server) persistConfigLight(incoming config.Config) (int, map[string]any) {
	s.mu.RLock()
	incoming.Auth = s.cfg.Auth
	s.mu.RUnlock()

	if err := incoming.Validate(); err != nil {
		return http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{"code": "invalid_config", "message": err.Error()},
		}
	}
	if err := config.SaveWith(s.configPath, incoming, s.sealer); err != nil {
		return http.StatusInternalServerError, map[string]any{
			"error": map[string]any{"code": "save_failed", "message": err.Error()},
		}
	}
	s.mu.Lock()
	s.cfg = incoming
	s.mu.Unlock()
	s.fireConfigApplied(incoming)
	return http.StatusOK, map[string]any{"config": incoming}
}

// Returns the HTTP status and response body for the caller to write.
func (s *Server) commitConfig(ctx context.Context, incoming config.Config, reason string) (int, map[string]any) {
	s.mu.RLock()
	incoming.Auth = s.cfg.Auth
	s.mu.RUnlock()

	if err := incoming.Validate(); err != nil {
		return http.StatusUnprocessableEntity, map[string]any{
			"error": map[string]any{"code": "invalid_config", "message": err.Error()},
		}
	}

	if s.applyCoord != nil {
		att := s.applyCoord.Apply(ctx, incoming, reason)
		switch att.Status {
		case applycoord.StatusSucceeded:
			return http.StatusOK, map[string]any{"apply_id": att.ID, "config": incoming}
		case applycoord.StatusRolledBack:
			return http.StatusUnprocessableEntity, map[string]any{
				"error": map[string]any{
					"code":     "apply_rolled_back",
					"message":  "the new config didn't pass health check; rolled back to the previous one",
					"reason":   att.Error,
					"apply_id": att.ID,
				},
			}
		default:
			return http.StatusInternalServerError, map[string]any{
				"error": map[string]any{
					"code":           "apply_failed",
					"message":        "apply failed and rollback also failed; system may be in an inconsistent state",
					"reason":         att.Error,
					"rollback_error": att.RollbackError,
					"apply_id":       att.ID,
				},
			}
		}
	}

	// Legacy direct path — tests / minimal dev setups.
	if err := s.backend.Apply(ctx, incoming); err != nil {
		return http.StatusInternalServerError, map[string]any{
			"error": map[string]any{"code": "apply_failed", "message": err.Error()},
		}
	}
	if err := config.SaveWith(s.configPath, incoming, s.sealer); err != nil {
		return http.StatusInternalServerError, map[string]any{
			"error": map[string]any{"code": "save_failed", "message": err.Error()},
		}
	}
	s.mu.Lock()
	s.cfg = incoming
	s.mu.Unlock()
	s.fireConfigApplied(incoming)
	return http.StatusOK, map[string]any{"config": incoming}
}

// genPortForwardID returns a short, URL-safe, unique-enough id.
func genPortForwardID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "pf-0"
	}
	return "pf-" + hex.EncodeToString(b[:])
}
