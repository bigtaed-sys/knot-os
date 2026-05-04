package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
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
	// /system/update/rescue: per-device keypair management. /info
	// shows whether the private key has been revealed yet; /reveal
	// returns it once and then forever 410.
	r.Get("/system/update/rescue", s.handleRescueInfo)
	r.Post("/system/update/rescue/reveal", s.handleRescueReveal)
}

// SetUpdateManager wires the GitHub-driven update path into the API.
// Pass nil (or skip the call) to disable the /system/update/check
// and /apply endpoints — they then respond 503.
func (s *Server) SetUpdateManager(m *update.Manager) { s.updater = m }

// SetRescue wires the per-device rescue keypair into the API. The
// rescue public key authorises self-built binaries via the update
// manager; the private key is downloadable once via this API.
func (s *Server) SetRescue(r *update.Rescue) { s.rescue = r }

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

// handleUpdate accepts a knotd binary upload, optionally with a
// detached Ed25519 signature, atomically replaces the on-disk
// binary, and triggers a systemd restart.
//
// Two content types are supported:
//
//   multipart/form-data        — preferred. Required parts:
//                                  binary    : the new knotd
//                                  signature : detached .sig (Ed25519
//                                              over the binary bytes)
//   application/octet-stream   — legacy raw upload. No signature is
//                                supplied; the install proceeds only
//                                when the running daemon was built
//                                without a release key (dev). On a
//                                production-keyed build the request
//                                is rejected — use the multipart
//                                form or call /system/update/apply
//                                instead.
//
// Either path is delegated to update.Manager.VerifyAndInstall,
// which is the same code path used by the GitHub auto-updater.
// Restart in a goroutine so the response flushes before knotd dies.
func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.production {
		writeError(w, http.StatusServiceUnavailable, "dev_mode", "update disabled in dev mode")
		return
	}
	if s.updater == nil {
		writeError(w, http.StatusServiceUnavailable, "update_disabled", "update manager not configured")
		return
	}

	const maxBinarySize = 64 << 20
	const maxSigSize = 1 << 20

	binary, sig, err := readUpdateUpload(r, maxBinarySize, maxSigSize)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad_upload", err.Error())
		return
	}

	if err := s.updater.VerifyAndInstall(binary, sig); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "install_rejected", err.Error())
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":    true,
		"bytes": len(binary),
	})
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := s.updater.Restart(); err != nil {
			fmt.Fprintf(os.Stderr, "update: restart after manual install: %v\n", err)
		}
	}()
}

// readUpdateUpload extracts the binary and (optional) signature
// from a system/update request, supporting both content types
// described above.
func readUpdateUpload(r *http.Request, maxBinary, maxSig int64) (binary, sig []byte, err error) {
	ct := r.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "multipart/form-data"):
		// 65 MB cap on the whole upload; multipart adds boundary
		// overhead but it's negligible.
		if err := r.ParseMultipartForm(maxBinary + maxSig); err != nil {
			return nil, nil, fmt.Errorf("parse multipart: %w", err)
		}
		binary, err = readFormFile(r, "binary", maxBinary)
		if err != nil {
			return nil, nil, fmt.Errorf("binary: %w", err)
		}
		sig, err = readFormFile(r, "signature", maxSig)
		if err != nil && !errors.Is(err, http.ErrMissingFile) {
			return nil, nil, fmt.Errorf("signature: %w", err)
		}
		return binary, sig, nil
	case ct == "" || strings.HasPrefix(ct, "application/octet-stream"):
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBinary+1))
		if err != nil {
			return nil, nil, err
		}
		if int64(len(body)) > maxBinary {
			return nil, nil, fmt.Errorf("binary exceeds %d bytes", maxBinary)
		}
		return body, nil, nil
	default:
		return nil, nil, fmt.Errorf("unsupported Content-Type %q (use multipart/form-data or application/octet-stream)", ct)
	}
}

func readFormFile(r *http.Request, name string, max int64) ([]byte, error) {
	f, _, err := r.FormFile(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	body, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > max {
		return nil, fmt.Errorf("%s exceeds %d bytes", name, max)
	}
	return body, nil
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

// handleRescueInfo reports the rescue key state. Always safe to
// call — the response carries the public key and a flag for whether
// the private half is still available for download. The private
// bytes themselves are never returned by this endpoint.
func (s *Server) handleRescueInfo(w http.ResponseWriter, _ *http.Request) {
	if s.rescue == nil {
		writeError(w, http.StatusServiceUnavailable, "rescue_disabled", "rescue key not configured")
		return
	}
	pub := s.rescue.PublicKey()
	writeJSON(w, http.StatusOK, map[string]any{
		"public_key":      hexEncode(pub),
		"private_available": s.rescue.HasPrivateKey(),
	})
}

// handleRescueReveal returns the rescue private key as a hex string
// exactly once. After this call the daemon overwrites its in-memory
// copy with zeros and rewrites the on-disk file with revealed=true,
// so a second call always 410s.
//
// The response shape is deliberately minimal: just a JSON object
// with `private_key` containing 64 hex chars. The user is expected
// to copy that into a password manager. No fancy file download
// because Ed25519 private keys are tiny and copy-paste is the
// least error-prone delivery method.
func (s *Server) handleRescueReveal(w http.ResponseWriter, _ *http.Request) {
	if s.rescue == nil {
		writeError(w, http.StatusServiceUnavailable, "rescue_disabled", "rescue key not configured")
		return
	}
	priv, err := s.rescue.RevealPrivateOnce()
	if err != nil {
		if errors.Is(err, update.ErrAlreadyRevealed) {
			writeError(w, http.StatusGone, "rescue_already_revealed",
				"the rescue private key was already shown — generate a new one to retry")
			return
		}
		writeError(w, http.StatusInternalServerError, "rescue_reveal_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"private_key":  priv,
		"public_key":   hexEncode(s.rescue.PublicKey()),
		"warning":      "save this immediately — it cannot be retrieved again. Use it to sign self-built knotd binaries.",
	})
}

func hexEncode(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexdigits[v>>4]
		out[i*2+1] = hexdigits[v&0x0f]
	}
	return string(out)
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
