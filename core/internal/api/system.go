package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/knot-os/knot-os/core/internal/update"
)

// MountSystem registers /system/* endpoints (auth-required) for power
// control and binary self-update.
//
// In dev mode (where reboot/shutdown would terminate the developer's
// laptop) these endpoints respond 503 and refuse the action; the
// production flag is set from main when the LinuxBackend is in use.
func (s *Server) MountSystem(r chi.Router) {
	r.Post("/system/reboot", s.handleReboot)
	r.Post("/system/shutdown", s.handleShutdown)
	// /system/update accepts a raw octet-stream upload of a new
	// knotd binary. Kept for compatibility with scripts/update-knot.ps1
	// (manual dev pushes); does not enforce a signature, so it
	// remains gated by the auth cookie + production-mode flag.
	r.Post("/system/update", s.handleUpdate)
	// /system/update/check + /apply are the new GitHub-driven
	// auto-update path: signed binaries fetched from the latest
	// release, verified with the embedded Ed25519 release key
	// before install.
	r.Get("/system/update/check", s.handleUpdateCheck)
	r.Post("/system/update/apply", s.handleUpdateApply)
}

// SetUpdateManager wires the GitHub-driven update path into the API.
// Pass nil (or skip the call) to disable the /system/update/check
// and /apply endpoints — they then respond 503.
func (s *Server) SetUpdateManager(m *update.Manager) { s.updater = m }

// SetProductionMode flips the gate that allows /system/{reboot,shutdown}
// to actually execute. Off by default; main turns it on when running
// under a real Linux backend.
func (s *Server) SetProductionMode(on bool) { s.production = on }

func (s *Server) handleReboot(w http.ResponseWriter, _ *http.Request) {
	if !s.production {
		writeError(w, http.StatusServiceUnavailable, "dev_mode", "reboot disabled in dev mode")
		return
	}
	// Acknowledge before launching the reboot command — the system
	// goes down before the response gets a chance to flush otherwise.
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = exec.Command("systemctl", "reboot").Run()
	}()
}

func (s *Server) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	if !s.production {
		writeError(w, http.StatusServiceUnavailable, "dev_mode", "shutdown disabled in dev mode")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true})
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = exec.Command("systemctl", "poweroff").Run()
	}()
}

// handleUpdate accepts a knotd binary upload as the request body,
// validates it, atomically replaces the on-disk binary, and triggers
// a systemd restart. The new binary takes over within seconds.
//
// Validation in v0.1 is minimal:
//   - file must be non-empty and at least 1 MB (refuses obvious junk)
//   - file must look like an ELF (magic 7f 45 4c 46)
//
// Future hardening: signed updates with a public key baked into
// /etc/knot/update.pub, version compatibility check, automatic
// rollback on health-check failure.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.production {
		writeError(w, http.StatusServiceUnavailable, "dev_mode", "update disabled in dev mode")
		return
	}
	const (
		minSize     = 1 << 20  // 1 MB — anything smaller can't be knotd
		maxSize     = 64 << 20 // 64 MB — generous upper bound
		targetPath  = "/usr/local/bin/knotd"
		stagingDir  = "/var/lib/knot"
		stagingName = ".knotd.update"
	)

	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, "staging_failed", err.Error())
		return
	}

	staging := filepath.Join(stagingDir, stagingName)
	out, err := os.OpenFile(staging, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "staging_failed", err.Error())
		return
	}
	defer func() { _ = out.Close() }()

	limited := io.LimitReader(r.Body, maxSize+1)
	n, err := io.Copy(out, limited)
	if err != nil {
		_ = os.Remove(staging)
		writeError(w, http.StatusInternalServerError, "io", err.Error())
		return
	}
	if n > maxSize {
		_ = os.Remove(staging)
		writeError(w, http.StatusRequestEntityTooLarge, "too_large",
			fmt.Sprintf("uploaded file exceeds %d bytes", maxSize))
		return
	}
	if n < minSize {
		_ = os.Remove(staging)
		writeError(w, http.StatusUnprocessableEntity, "too_small",
			fmt.Sprintf("uploaded file is %d bytes; expected at least %d", n, minSize))
		return
	}

	// ELF magic check — rejects accidental uploads of source code or
	// images. Real signature verification arrives later.
	if err := verifyELF(staging); err != nil {
		_ = os.Remove(staging)
		writeError(w, http.StatusUnprocessableEntity, "not_elf", err.Error())
		return
	}

	// Atomic replace: rename onto target (same filesystem so this is
	// a single inode swap, no half-written state on disk).
	if err := os.Rename(staging, targetPath); err != nil {
		writeError(w, http.StatusInternalServerError, "install_failed", err.Error())
		return
	}

	// Acknowledge before restart — the response wouldn't get flushed
	// after systemctl restarts us.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":    true,
		"bytes": n,
	})
	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = exec.Command("systemctl", "restart", "knotd").Run()
	}()
}

// handleUpdateCheck queries GitHub Releases for the latest knotd
// build and reports whether it's newer than the running version.
// Cheap, idempotent — the wizard / System page can poll it on a
// timer if desired.
func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update_disabled", "auto-update not configured")
		return
	}
	res, err := s.updater.CheckLatest(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "github_unreachable", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// handleUpdateApply downloads the latest release, verifies it with
// the embedded Ed25519 key, atomically replaces the on-disk binary,
// and restarts knotd. Synchronous: blocks until install is complete
// (a few seconds; longest part is the GitHub download). The actual
// systemctl restart happens in a goroutine after the response
// flushes so the user sees "ok" before the service dies.
func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update_disabled", "auto-update not configured")
		return
	}
	if !s.production {
		writeError(w, http.StatusServiceUnavailable, "dev_mode", "auto-update disabled in dev mode")
		return
	}
	tag, err := s.updater.ApplyLatest(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "apply_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"version": tag,
	})
	// Restart in a goroutine so the response fully flushes first.
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := s.updater.Restart(); err != nil {
			// Best-effort log; we have no other channel back to the
			// user since the response already returned.
			fmt.Fprintf(os.Stderr, "update: restart after apply: %v\n", err)
		}
	}()
}

// verifyELF reads the first 4 bytes of path and reports an error
// unless they are the ELF magic (0x7f 'E' 'L' 'F'). Cheap sanity
// check that prevents flashing the wrong file shape.
func verifyELF(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var head [4]byte
	if _, err := io.ReadFull(f, head[:]); err != nil {
		return err
	}
	if head != [4]byte{0x7f, 'E', 'L', 'F'} {
		return errors.New("not an ELF binary")
	}
	return nil
}
