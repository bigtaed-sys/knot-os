package singbox

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// fakeRunner records what the manager asks it to do.
type fakeRunner struct {
	mu      sync.Mutex
	starts  int
	stops   int
	reloads int
	running bool
	startErr error
}

func (r *fakeRunner) Start(_ context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.startErr != nil {
		return r.startErr
	}
	r.starts++
	r.running = true
	return nil
}
func (r *fakeRunner) Reload(_ context.Context, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reloads++
	return nil
}
func (r *fakeRunner) Stop(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stops++
	r.running = false
	return nil
}
func (r *fakeRunner) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func TestManagerSkipsStartWithNoUserOutbounds(t *testing.T) {
	r := &fakeRunner{}
	m := newManagerWithTempDir(t, r)

	if err := m.Apply(context.Background(), DefaultsConfig()); err != nil {
		t.Fatal(err)
	}
	if r.starts != 0 {
		t.Errorf("starts=%d, want 0 (no user outbounds = engine idle)", r.starts)
	}
	if r.running {
		t.Error("runner should not be running")
	}
}

func TestManagerStartsOnFirstUserOutbound(t *testing.T) {
	r := &fakeRunner{}
	m := newManagerWithTempDir(t, r)

	cfg := Config{
		Outbounds: []Outbound{
			{Tag: "tokyo", Type: OutboundVLESS, Server: "h", Port: 443,
				UUID: "12345678-1234-1234-1234-123456789012"},
		},
	}
	if err := m.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	if r.starts != 1 {
		t.Errorf("starts=%d, want 1", r.starts)
	}
	if !r.running {
		t.Error("runner should be running")
	}
}

func TestManagerReloadsOnSubsequentApply(t *testing.T) {
	r := &fakeRunner{}
	m := newManagerWithTempDir(t, r)

	cfg := Config{
		Outbounds: []Outbound{
			{Tag: "tokyo", Type: OutboundVLESS, Server: "h", Port: 443,
				UUID: "12345678-1234-1234-1234-123456789012"},
		},
	}
	_ = m.Apply(context.Background(), cfg)
	cfg.Outbounds = append(cfg.Outbounds, Outbound{
		Tag: "frankfurt", Type: OutboundVLESS, Server: "h2", Port: 443,
		UUID: "12345678-1234-1234-1234-123456789013",
	})
	_ = m.Apply(context.Background(), cfg)

	if r.starts != 1 {
		t.Errorf("starts=%d, want 1 (second apply should reload, not restart)", r.starts)
	}
	if r.reloads != 1 {
		t.Errorf("reloads=%d, want 1", r.reloads)
	}
}

func TestManagerStopsWhenLastOutboundRemoved(t *testing.T) {
	r := &fakeRunner{}
	m := newManagerWithTempDir(t, r)

	cfg := Config{
		Outbounds: []Outbound{
			{Tag: "tokyo", Type: OutboundVLESS, Server: "h", Port: 443,
				UUID: "12345678-1234-1234-1234-123456789012"},
		},
	}
	_ = m.Apply(context.Background(), cfg)
	if !r.running {
		t.Fatal("not running after first apply")
	}
	_ = m.Apply(context.Background(), DefaultsConfig())
	if r.running {
		t.Error("should be stopped after empty config applied")
	}
	if r.stops != 1 {
		t.Errorf("stops=%d, want 1", r.stops)
	}
}

func TestManagerWithNilRunnerStillRendersFile(t *testing.T) {
	m := newManagerWithTempDir(t, nil)
	cfg := Config{
		Outbounds: []Outbound{
			{Tag: "tokyo", Type: OutboundDirect},
		},
	}
	if err := m.Apply(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	// Confirm the file lands on disk for inspection.
	if _, err := os.Stat(m.ConfigPath()); err != nil {
		t.Errorf("config file not written: %v", err)
	}
}

// newManagerWithTempDir builds a Manager whose ConfigPath is
// inside a tempdir so tests don't write to /run/knot.
func newManagerWithTempDir(t *testing.T, r Runner) *Manager {
	t.Helper()
	dir := t.TempDir()
	m := NewManager(r, nil)
	// Override the config path so tests don't try to touch
	// /run/knot/sing-box.json.
	m.WithConfigPath(filepath.Join(dir, "sing-box.json"))
	return m
}

// Sanity-check that the write path actually writes to the
// configured confPath (not to the package-level ConfigPath
// constant). Regression guard for the confDir → confPath
// refactor — tests that touched /run/knot used to fail on
// non-Linux dev hosts.
func TestManagerWriteConfigCreatesFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "sing-box.json")
	m := &Manager{confPath: target}
	if err := m.writeConfig([]byte(`{"hello":"world"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected file at %s, stat err: %v", target, err)
	}
}
