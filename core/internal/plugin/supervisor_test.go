package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// sandboxProbe checks ONCE whether this environment can actually launch a
// sandboxed plugin. The supervisor confines children with PID/IPC/UTS/NET
// namespaces (applySandbox), which require privileges the process lacks on
// an unprivileged host — GitHub Actions runners and WSL both refuse the
// clone with EPERM ("operation not permitted"). knotd runs as root in
// production so it's fine there; the tests just can't reproduce it
// everywhere. On non-Linux, applySandbox is a no-op so the probe passes.
var sandboxProbe = sync.OnceValue(func() bool {
	cmd := exec.Command(os.Args[0], "knot-plugin-crash") // exits immediately
	applySandbox(cmd, 0, 0, false)
	if err := cmd.Start(); err != nil {
		return false
	}
	_ = cmd.Wait()
	return true
})

// requireSandbox skips a test that needs to launch a real sandboxed
// process when the environment won't permit it, so an unprivileged CI
// runner reports skip rather than a spurious failure.
func requireSandbox(t *testing.T) {
	t.Helper()
	if !sandboxProbe() {
		t.Skip("process sandbox unavailable here (unprivileged namespaces not permitted); exercised under root/CI-with-userns")
	}
}

// TestMain lets the test binary re-exec itself as a fake plugin: when
// invoked with a sentinel arg it behaves like a supervised plugin
// process instead of running the test suite. This is the standard Go
// pattern for exercising subprocess management without shipping a
// separate helper binary.
func TestMain(m *testing.M) {
	for _, a := range os.Args[1:] {
		switch a {
		case "knot-plugin-sleep":
			time.Sleep(10 * time.Minute)
			os.Exit(0)
		case "knot-plugin-crash":
			os.Exit(3)
		}
	}
	os.Exit(m.Run())
}

// newTestSupervisor builds a Supervisor whose plugin dirs live under a
// temp tree, and creates the per-plugin working directory exec needs.
func newTestSupervisor(t *testing.T, ids ...string) *Supervisor {
	t.Helper()
	root := t.TempDir()
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(root, id), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return NewSupervisor(SupervisorOptions{
		PluginsDir: root,
		RuntimeDir: filepath.Join(t.TempDir(), "run"),
	})
}

func sleeperPlugin(id string) Plugin {
	return Plugin{
		Manifest: Manifest{
			ID: id, Name: id, Version: "1.0.0",
			Exec: []string{os.Args[0], "knot-plugin-sleep"},
		},
		Enabled: true,
	}
}

// waitState polls until the plugin reaches want or the deadline passes.
func waitState(t *testing.T, s *Supervisor, id string, want State, d time.Duration) ProcStatus {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		st, _ := s.Status(id)
		if st.State == want {
			return st
		}
		time.Sleep(15 * time.Millisecond)
	}
	st, _ := s.Status(id)
	t.Fatalf("plugin %s: state %q not reached in %s (last=%q, err=%q)", id, want, d, st.State, st.LastError)
	return st
}

func TestSupervisorStartsAndStops(t *testing.T) {
	requireSandbox(t)
	s := newTestSupervisor(t, "sleepy")
	s.Sync([]Plugin{sleeperPlugin("sleepy")})

	st := waitState(t, s, "sleepy", StateRunning, 5*time.Second)
	if st.PID <= 0 {
		t.Errorf("running plugin should have a PID, got %d", st.PID)
	}

	s.Stop("sleepy")
	// After Stop it's removed from supervision → Status reports stopped.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.Status("sleepy"); !ok {
			return // dropped from the map = fully stopped
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Error("plugin still supervised after Stop")
}

func TestSupervisorSyncStopsRemoved(t *testing.T) {
	requireSandbox(t)
	s := newTestSupervisor(t, "a", "b")
	s.Sync([]Plugin{sleeperPlugin("a"), sleeperPlugin("b")})
	waitState(t, s, "a", StateRunning, 5*time.Second)
	waitState(t, s, "b", StateRunning, 5*time.Second)

	// Drop "b" from the desired set → it should be stopped, "a" stays.
	s.Sync([]Plugin{sleeperPlugin("a")})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := s.Status("b"); !ok {
			break
		}
		time.Sleep(15 * time.Millisecond)
	}
	if _, ok := s.Status("b"); ok {
		t.Error("b should have been stopped by Sync")
	}
	if st, _ := s.Status("a"); st.State != StateRunning {
		t.Errorf("a should still be running, got %q", st.State)
	}
	s.StopAll()
}

func TestSupervisorRestartsOnCrash(t *testing.T) {
	requireSandbox(t)
	s := newTestSupervisor(t, "crasher")
	p := Plugin{
		Manifest: Manifest{
			ID: "crasher", Name: "crasher", Version: "1.0.0",
			Exec: []string{os.Args[0], "knot-plugin-crash"},
		},
		Enabled: true,
	}
	s.Sync([]Plugin{p})

	// It exits immediately → supervisor records a crash and retries,
	// so the restart counter climbs.
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if st, _ := s.Status("crasher"); st.Restarts >= 2 {
			s.StopAll()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	st, _ := s.Status("crasher")
	s.StopAll()
	t.Fatalf("expected crasher to restart (>=2), got restarts=%d state=%q", st.Restarts, st.State)
}

func TestMetadataOnlyPluginNotStarted(t *testing.T) {
	s := newTestSupervisor(t, "meta")
	// No Exec → metadata-only; Sync must not try to run anything.
	s.Sync([]Plugin{{Manifest: Manifest{ID: "meta", Name: "meta", Version: "1.0.0"}, Enabled: true}})
	if _, ok := s.Status("meta"); ok {
		t.Error("metadata-only plugin should not be supervised")
	}
}
