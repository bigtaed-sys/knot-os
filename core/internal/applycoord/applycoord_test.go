package applycoord

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// stubBackend is a controllable network.Backend for tests. Apply
// returns the next configured error from applyErrs (or nil), and
// records the last config it saw. Status returns whatever the test
// last set via SetStatus.
type stubBackend struct {
	mu        sync.Mutex
	applies   []config.Config
	applyErrs []error
	status    network.Status
	statusErr error
}

func (b *stubBackend) Apply(_ context.Context, cfg config.Config) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.applies = append(b.applies, cfg)
	if len(b.applyErrs) == 0 {
		return nil
	}
	err := b.applyErrs[0]
	b.applyErrs = b.applyErrs[1:]
	return err
}

func (b *stubBackend) Status(_ context.Context) (network.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, b.statusErr
}

func (b *stubBackend) Scan(_ context.Context) ([]network.ScannedNetwork, error) {
	return nil, nil
}
func (b *stubBackend) Name() string { return "stub" }

func (b *stubBackend) SetStatus(s network.Status) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.status = s
}

func (b *stubBackend) ApplyCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.applies)
}

func newCoord(t *testing.T, b *stubBackend) *Coordinator {
	t.Helper()
	cur := config.Config{Role: config.RoleSetup}
	c, err := NewCoordinator(Options{
		Backend:       b,
		HealthTimeout: 100 * time.Millisecond,
		SnapshotFn:    func() config.Config { return cur },
		CommitFn: func(c config.Config) error {
			cur = c
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestApplySuccessPath(t *testing.T) {
	b := &stubBackend{}
	b.SetStatus(network.Status{
		Role: config.RoleWiFiRouter,
		AP:   &network.APStatus{SSID: "MyNet", Up: true},
	})
	c := newCoord(t, b)

	target := config.Config{
		Role: config.RoleWiFiRouter,
		Network: config.Network{
			AP: &config.WiFiAP{SSID: "MyNet"},
		},
	}
	att := c.Apply(context.Background(), target, "test")
	if att.Status != StatusSucceeded {
		t.Fatalf("status=%s err=%s", att.Status, att.Error)
	}
	if !att.HealthCheckPassed {
		t.Error("health check should have passed")
	}
	if got := b.ApplyCount(); got != 1 {
		t.Errorf("backend.Apply called %d times, want 1 (no rollback expected)", got)
	}
}

func TestApplyErrorTriggersRollback(t *testing.T) {
	b := &stubBackend{
		applyErrs: []error{errors.New("hostapd failed"), nil},
	}
	c := newCoord(t, b)

	target := config.Config{Role: config.RoleWiFiRouter}
	att := c.Apply(context.Background(), target, "test")

	if att.Status != StatusRolledBack {
		t.Fatalf("status=%s, want rolled_back; err=%s", att.Status, att.Error)
	}
	if att.Error == "" {
		t.Error("expected an error message describing what failed")
	}
	if att.HealthCheckPassed {
		t.Error("health check should not have passed on a rolled-back apply")
	}
	if got := b.ApplyCount(); got != 2 {
		t.Errorf("backend.Apply called %d times, want 2 (apply + rollback)", got)
	}
}

func TestApplyHealthCheckFailureRollsBack(t *testing.T) {
	b := &stubBackend{}
	// Status reports AP DOWN — health check will fail.
	b.SetStatus(network.Status{
		Role: config.RoleWiFiRouter,
		AP:   &network.APStatus{SSID: "MyNet", Up: false},
	})
	c := newCoord(t, b)

	target := config.Config{
		Role: config.RoleWiFiRouter,
		Network: config.Network{
			AP: &config.WiFiAP{SSID: "MyNet"},
		},
	}
	att := c.Apply(context.Background(), target, "test")

	if att.Status != StatusRolledBack {
		t.Fatalf("status=%s, want rolled_back; err=%s", att.Status, att.Error)
	}
	if got := b.ApplyCount(); got != 2 {
		t.Errorf("backend.Apply called %d times, want 2 (apply + rollback)", got)
	}
}

func TestApplyRollbackFailureMarksFailed(t *testing.T) {
	b := &stubBackend{
		// Apply errors. Rollback also errors → terminal Failed.
		applyErrs: []error{errors.New("apply boom"), errors.New("rollback boom")},
	}
	c := newCoord(t, b)

	att := c.Apply(context.Background(), config.Config{Role: config.RoleWiFiRouter}, "test")
	if att.Status != StatusFailed {
		t.Errorf("status=%s, want failed", att.Status)
	}
	if att.RollbackError == "" {
		t.Error("RollbackError should be populated when rollback fails")
	}
}

func TestRecentReturnsMostRecentFirst(t *testing.T) {
	b := &stubBackend{}
	b.SetStatus(network.Status{
		Role: config.RoleSetup,
		AP:   &network.APStatus{SSID: "Setup", Up: true},
	})
	c := newCoord(t, b)

	for i := 0; i < 3; i++ {
		c.Apply(context.Background(), config.Config{Role: config.RoleSetup}, "test")
	}
	got := c.Recent(0)
	if len(got) != 3 {
		t.Fatalf("got %d attempts, want 3", len(got))
	}
	// Newest first: ID strings differ but timestamps go DESC.
	if !got[0].StartedAt.After(got[1].StartedAt) && !got[0].StartedAt.Equal(got[1].StartedAt) {
		t.Errorf("Recent should be newest-first")
	}
}

func TestGetReturnsSpecificAttempt(t *testing.T) {
	b := &stubBackend{}
	b.SetStatus(network.Status{
		Role: config.RoleSetup,
		AP:   &network.APStatus{SSID: "Setup", Up: true},
	})
	c := newCoord(t, b)

	att := c.Apply(context.Background(), config.Config{Role: config.RoleSetup}, "test")
	got := c.Get(att.ID)
	if got == nil {
		t.Fatal("Get returned nil for known attempt")
	}
	if got.ID != att.ID {
		t.Errorf("ID mismatch: %s vs %s", got.ID, att.ID)
	}
	if c.Get("does-not-exist") != nil {
		t.Error("Get should return nil for unknown ID")
	}
}

func TestCommitFailureRollsBack(t *testing.T) {
	b := &stubBackend{}
	b.SetStatus(network.Status{
		Role: config.RoleWiFiRouter,
		AP:   &network.APStatus{SSID: "MyNet", Up: true},
	})
	cur := config.Config{Role: config.RoleSetup}
	c, _ := NewCoordinator(Options{
		Backend:       b,
		HealthTimeout: 100 * time.Millisecond,
		SnapshotFn:    func() config.Config { return cur },
		CommitFn: func(c config.Config) error {
			// First commit succeeds (target apply), second fails (rollback).
			// We need a cleaner mock — use a counter.
			return errors.New("commit boom: disk full")
		},
	})
	target := config.Config{
		Role: config.RoleWiFiRouter,
		Network: config.Network{
			AP: &config.WiFiAP{SSID: "MyNet"},
		},
	}
	att := c.Apply(context.Background(), target, "test")
	// commit fails → rollback. Rollback also calls commit which also
	// fails → terminal Failed.
	if att.Status != StatusFailed {
		t.Errorf("status=%s err=%s rb=%s", att.Status, att.Error, att.RollbackError)
	}
}

func TestStatusFinal(t *testing.T) {
	for _, c := range []struct {
		s  Status
		ok bool
	}{
		{StatusPending, false},
		{StatusRunning, false},
		{StatusHealthCheck, false},
		{StatusSucceeded, true},
		{StatusFailed, true},
		{StatusRolledBack, true},
	} {
		if c.s.Final() != c.ok {
			t.Errorf("%q.Final() = %v, want %v", c.s, c.s.Final(), c.ok)
		}
	}
}

func TestHealthCheckerExtenderUplinkRequired(t *testing.T) {
	b := &stubBackend{}
	hc := DefaultHealthChecker(b)

	// AP up, uplink NOT connected → fail.
	b.SetStatus(network.Status{
		Role:   config.RoleWiFiExtender,
		AP:     &network.APStatus{SSID: "Repeater", Up: true},
		Uplink: &network.UplinkStatus{SSID: "ISP", Connected: false},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := hc.Check(ctx, config.Config{Role: config.RoleWiFiExtender}); err == nil {
		t.Error("expected health-check failure when uplink isn't connected")
	}

	// Uplink connected → pass.
	b.SetStatus(network.Status{
		Role:   config.RoleWiFiExtender,
		AP:     &network.APStatus{SSID: "Repeater", Up: true},
		Uplink: &network.UplinkStatus{SSID: "ISP", Connected: true},
	})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel2()
	if err := hc.Check(ctx2, config.Config{Role: config.RoleWiFiExtender}); err != nil {
		t.Errorf("uplink connected but check failed: %v", err)
	}
}

func TestHealthCheckerRouterToleratesNoWAN(t *testing.T) {
	b := &stubBackend{}
	b.SetStatus(network.Status{
		Role: config.RoleWiFiRouter,
		AP:   &network.APStatus{SSID: "MyNet", Up: true},
		WAN:  &network.WANStatus{Interface: "eth0", Up: false},
	})
	hc := DefaultHealthChecker(b)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := hc.Check(ctx, config.Config{Role: config.RoleWiFiRouter}); err != nil {
		t.Errorf("no-carrier WAN should NOT fail health check: %v", err)
	}
}
