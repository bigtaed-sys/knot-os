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
	r.Post("/system/update", s.handleUpdate)
}

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
