package plugin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// State is a plugin process's lifecycle state, surfaced to the UI.
type State string

const (
	StateStopped State = "stopped" // not running (disabled, or metadata-only)
	StateRunning State = "running" // process is up
	StateCrashed State = "crashed" // exited unexpectedly; being retried
)

// ProcStatus is a snapshot of one plugin process's runtime state.
type ProcStatus struct {
	State     State     `json:"state"`
	PID       int       `json:"pid,omitempty"`
	Restarts  int       `json:"restarts,omitempty"`
	LastError string    `json:"last_error,omitempty"`
	Since     time.Time `json:"since,omitempty"`
	Socket    string    `json:"-"` // Unix socket the plugin listens on
}

// SupervisorOptions configures a Supervisor.
type SupervisorOptions struct {
	// PluginsDir is the install root (e.g. /usr/lib/knot/plugins). A
	// plugin "foo" lives in PluginsDir/foo and that is its working
	// directory; a "./bin" in its Exec resolves against it.
	PluginsDir string
	// RuntimeDir is where per-plugin Unix sockets are created (e.g.
	// /run/knot/plugins). Wiped-on-reboot tmpfs in production.
	RuntimeDir string
	// HostSocket is the path of knotd's host-API Unix socket, handed
	// to every plugin as KNOT_HOST_SOCKET.
	HostSocket string
	// RunAsUID/RunAsGID, when > 0, drop each plugin process to this
	// unprivileged uid/gid (Linux only). 0 = run as the daemon's own
	// user (no drop) — the dev / non-Linux path. See sandbox_linux.go.
	RunAsUID int
	RunAsGID int
	// Logger receives supervision events. Defaults to log.Default().
	Logger *log.Logger
}

// Supervisor launches and watches the processes of enabled plugins.
// One process per plugin; each is restarted with capped backoff if it
// exits unexpectedly, and torn down when the plugin is disabled.
type Supervisor struct {
	opts   SupervisorOptions
	mu     sync.Mutex
	procs  map[string]*managedProc
	tokens map[string]string // host-API bearer token → plugin id
}

type managedProc struct {
	id     string
	socket string
	token  string
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	status ProcStatus
}

// NewSupervisor builds a Supervisor. Nothing runs until Sync/Start.
func NewSupervisor(opts SupervisorOptions) *Supervisor {
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	return &Supervisor{
		opts:   opts,
		procs:  make(map[string]*managedProc),
		tokens: make(map[string]string),
	}
}

// PluginForToken maps a host-API bearer token back to the plugin id
// that owns it. The host API uses this to attribute a call to a
// plugin and enforce its declared permissions. Returns ("", false)
// for an unknown token.
func (s *Supervisor) PluginForToken(token string) (string, bool) {
	if token == "" {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.tokens[token]
	return id, ok
}

func newToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// SocketPath is where plugin id's process is told to listen.
func (s *Supervisor) SocketPath(id string) string {
	return filepath.Join(s.opts.RuntimeDir, id+".sock")
}

// Status returns the current runtime state of a plugin, or
// (StateStopped, false) when it isn't supervised.
func (s *Supervisor) Status(id string) (ProcStatus, bool) {
	s.mu.Lock()
	p, ok := s.procs[id]
	s.mu.Unlock()
	if !ok {
		return ProcStatus{State: StateStopped}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status, true
}

// Sync reconciles the running set with the desired one: it starts
// every enabled plugin that has a runtime and isn't running, and stops
// any running plugin not in the desired-enabled set. Safe to call on
// every plugin toggle and once at boot.
func (s *Supervisor) Sync(plugins []Plugin) {
	want := make(map[string]Plugin)
	for _, p := range plugins {
		if p.Enabled && p.HasRuntime() {
			want[p.ID] = p
		}
	}

	s.mu.Lock()
	// Stop anything no longer wanted.
	for id := range s.procs {
		if _, ok := want[id]; !ok {
			s.stopLocked(id)
		}
	}
	// Start anything newly wanted.
	for id, p := range want {
		if _, ok := s.procs[id]; !ok {
			s.startLocked(p)
		}
	}
	s.mu.Unlock()
}

// Stop terminates a single plugin's process (if running).
func (s *Supervisor) Stop(id string) {
	s.mu.Lock()
	s.stopLocked(id)
	s.mu.Unlock()
}

// StopAll tears every plugin down. Called at daemon shutdown.
func (s *Supervisor) StopAll() {
	s.mu.Lock()
	for id := range s.procs {
		s.stopLocked(id)
	}
	s.mu.Unlock()
}

// startLocked launches a supervised process for p. Caller holds s.mu.
func (s *Supervisor) startLocked(p Plugin) {
	ctx, cancel := context.WithCancel(context.Background())
	sock := s.SocketPath(p.ID)
	token := newToken()
	mp := &managedProc{
		id:     p.ID,
		socket: sock,
		token:  token,
		cancel: cancel,
		done:   make(chan struct{}),
		status: ProcStatus{State: StateStopped, Socket: sock},
	}
	s.procs[p.ID] = mp
	s.tokens[token] = p.ID
	go s.supervise(ctx, p, mp)
}

// stopLocked cancels a plugin's supervise loop and drops it. Caller
// holds s.mu. Best-effort; returns immediately (teardown is async).
func (s *Supervisor) stopLocked(id string) {
	mp, ok := s.procs[id]
	if !ok {
		return
	}
	delete(s.procs, id)
	delete(s.tokens, mp.token)
	mp.cancel()
	_ = os.Remove(mp.socket)
}

// supervise runs p's process and restarts it with capped exponential
// backoff until ctx is cancelled.
func (s *Supervisor) supervise(ctx context.Context, p Plugin, mp *managedProc) {
	defer close(mp.done)

	argv0, args := s.resolveExec(p)
	pluginDir := filepath.Join(s.opts.PluginsDir, p.ID)
	backoff := 200 * time.Millisecond
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}
		// Clear any stale socket so the plugin can bind fresh.
		_ = os.Remove(mp.socket)
		_ = os.MkdirAll(s.opts.RuntimeDir, 0o755)

		cmd := exec.CommandContext(ctx, argv0, args...)
		cmd.Dir = pluginDir
		cmd.Env = append(os.Environ(),
			"KNOT_PLUGIN_ID="+p.ID,
			"KNOT_PLUGIN_SOCKET="+mp.socket,
			"KNOT_HOST_SOCKET="+s.opts.HostSocket,
			"KNOT_HOST_TOKEN="+mp.token,
		)
		cmd.Stdout = pluginLogWriter{logger: s.opts.Logger, id: p.ID}
		cmd.Stderr = pluginLogWriter{logger: s.opts.Logger, id: p.ID}
		// OS-level confinement (Linux): drop to an unprivileged uid/gid,
		// isolate PID/IPC/UTS namespaces, and — unless the plugin
		// declared the "network" permission — give it an empty network
		// namespace (no internet; its Unix sockets still work since
		// those are filesystem objects). No-op on non-Linux.
		applySandbox(cmd, s.opts.RunAsUID, s.opts.RunAsGID, p.Grants("network"))
		// WaitDelay bounds how long Wait blocks after ctx-cancel kills
		// the process, so Stop never wedges the supervise goroutine.
		cmd.WaitDelay = 5 * time.Second

		start := time.Now()
		if err := cmd.Start(); err != nil {
			s.setStatus(mp, ProcStatus{State: StateCrashed, LastError: err.Error(), Socket: mp.socket})
			s.opts.Logger.Printf("plugin %s: start: %v", p.ID, err)
		} else {
			s.bumpRunning(mp, cmd.Process.Pid)
			s.opts.Logger.Printf("plugin %s: started pid=%d", p.ID, cmd.Process.Pid)
			err := cmd.Wait()
			if ctx.Err() != nil {
				// Intentional stop.
				s.setStatus(mp, ProcStatus{State: StateStopped, Socket: mp.socket})
				return
			}
			msg := "exited"
			if err != nil {
				msg = err.Error()
			}
			s.bumpCrashed(mp, msg)
			s.opts.Logger.Printf("plugin %s: %s — restarting", p.ID, msg)
			// A run that lasted a while is a sign of health: reset backoff.
			if time.Since(start) > 30*time.Second {
				backoff = 200 * time.Millisecond
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff *= 2; backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

// resolveExec splits the manifest Exec into argv0 + args, resolving a
// "./"-prefixed argv0 against the plugin's directory.
func (s *Supervisor) resolveExec(p Plugin) (string, []string) {
	if len(p.Exec) == 0 {
		return "", nil
	}
	argv0 := p.Exec[0]
	if len(argv0) > 2 && argv0[0] == '.' && (argv0[1] == '/' || argv0[1] == '\\') {
		argv0 = filepath.Join(s.opts.PluginsDir, p.ID, argv0[2:])
	}
	return argv0, append([]string(nil), p.Exec[1:]...)
}

func (s *Supervisor) setStatus(mp *managedProc, st ProcStatus) {
	mp.mu.Lock()
	st.Restarts = mp.status.Restarts
	mp.status = st
	mp.mu.Unlock()
}

func (s *Supervisor) bumpRunning(mp *managedProc, pid int) {
	mp.mu.Lock()
	mp.status.State = StateRunning
	mp.status.PID = pid
	mp.status.LastError = ""
	mp.status.Since = time.Now()
	mp.mu.Unlock()
}

func (s *Supervisor) bumpCrashed(mp *managedProc, msg string) {
	mp.mu.Lock()
	mp.status.State = StateCrashed
	mp.status.PID = 0
	mp.status.LastError = msg
	mp.status.Restarts++
	mp.mu.Unlock()
}

// pluginLogWriter prefixes a plugin's stdout/stderr lines into the
// daemon log so operators see plugin output in `journalctl -u knotd`.
type pluginLogWriter struct {
	logger *log.Logger
	id     string
}

func (w pluginLogWriter) Write(b []byte) (int, error) {
	w.logger.Printf("plugin %s: %s", w.id, trimTrailingNewline(string(b)))
	return len(b), nil
}

func trimTrailingNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
