// Package applycoord wraps the network.Backend's Apply method with
// transactional semantics: a snapshot of the previous config is
// taken, Apply runs, the runtime state is health-checked, and on
// failure the snapshot is automatically re-applied so the system
// returns to the last known-good state.
//
// Why this exists: every Apply path in v2026.05.1 was best-effort.
// If `applyRouter` got halfway through and returned an error,
// hostapd was already torn down, the config wasn't saved, and the
// user was left without an AP until the next interactive Apply.
// This package turns Apply into an "all-or-nothing" operation —
// either the new config is fully in effect AND the system passes
// a runtime health check, or we roll all the way back.
//
// Scope: this is the foundation for M33. v1 is deliberately
// coarse-grained — Apply is one big step from the coordinator's
// point of view; we surface progress as Pending → Running →
// HealthCheck → Succeeded/RolledBack/Failed. Per-step events
// (hostapd up, dnsmasq up, ...) come in a follow-up patch when
// the backend's Apply is split into a streaming sequence.
package applycoord

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/knot-os/knot-os/core/internal/config"
	"github.com/knot-os/knot-os/core/internal/network"
)

// Status is the lifecycle state of an Attempt.
type Status string

const (
	StatusPending     Status = "pending"     // queued, not yet started
	StatusRunning     Status = "running"     // backend.Apply in progress
	StatusHealthCheck Status = "healthcheck" // Apply returned, verifying runtime
	StatusSucceeded   Status = "succeeded"   // healthy after Apply
	StatusFailed      Status = "failed"      // Apply errored AND rollback failed
	StatusRolledBack  Status = "rolled_back" // Apply or health-check failed; snapshot restored
)

// Final reports whether the attempt has reached a terminal state.
func (s Status) Final() bool {
	switch s {
	case StatusSucceeded, StatusFailed, StatusRolledBack:
		return true
	}
	return false
}

// DefaultHealthTimeout is how long we wait for the post-Apply
// state to converge before declaring the apply broken and rolling
// back. wifi-router needs ~10s for hostapd to come up + dhclient
// to lease; 30s is comfortable headroom.
const DefaultHealthTimeout = 30 * time.Second

// MaxHistory is the size of the in-memory ring buffer of past
// attempts the API + UI / Telegram can pull. Older attempts roll
// off; v2026.05.2's audit log (M35) will persist them.
const MaxHistory = 20

// Attempt is one transactional Apply attempt — its inputs, outcome,
// and timing. Returned by Apply and stored in the ring buffer for
// later inspection via GET /api/apply/{id}.
type Attempt struct {
	// ID is a 12-hex-char identifier the UI references in URLs.
	ID string `json:"id"`

	// Trigger is a free-form tag describing what kicked this off
	// ("api:put-config", "setup:complete", "scheduler:re-apply",
	// "guest:expired", ...). Helps debugging which path is broken.
	Trigger string `json:"trigger"`

	// Status walks Pending → Running → HealthCheck → terminal.
	Status Status `json:"status"`

	// StartedAt is when Apply was invoked. EndedAt is set when the
	// attempt reaches a terminal Status; zero before then.
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`

	// Snapshot is the config that was running BEFORE this attempt.
	// Used for rollback; never persisted (config.SaveWith only
	// stores the active config).
	Snapshot config.Config `json:"-"`

	// Target is the config the attempt tried to apply.
	Target config.Config `json:"target"`

	// Error, when non-empty, is the user-facing reason this attempt
	// went sideways (Apply returned err, health timed out, ...).
	Error string `json:"error,omitempty"`

	// RollbackError, when non-empty, means the rollback ALSO failed.
	// The system is in an unknown state; manual intervention or
	// reboot needed.
	RollbackError string `json:"rollback_error,omitempty"`

	// HealthCheckPassed reports whether the post-Apply health probe
	// succeeded. Always false on terminal statuses other than
	// Succeeded.
	HealthCheckPassed bool `json:"health_check_passed"`
}

// Duration returns how long the attempt ran (or has been running,
// for non-terminal attempts).
func (a Attempt) Duration() time.Duration {
	if a.EndedAt.IsZero() {
		return time.Since(a.StartedAt)
	}
	return a.EndedAt.Sub(a.StartedAt)
}

// HealthChecker verifies that the backend's runtime state matches
// what the just-applied config asks for. Returns nil if healthy,
// an error describing what's wrong otherwise.
//
// The default checker (DefaultHealthChecker) polls backend.Status
// against role-specific expectations.
type HealthChecker interface {
	Check(ctx context.Context, cfg config.Config) error
}

// HealthCheckerFunc adapts a plain function to the interface.
type HealthCheckerFunc func(ctx context.Context, cfg config.Config) error

// Check satisfies HealthChecker.
func (f HealthCheckerFunc) Check(ctx context.Context, cfg config.Config) error {
	return f(ctx, cfg)
}

// Coordinator orchestrates transactional applies.
//
// Concurrency: only one Apply can run at a time. A second concurrent
// Apply blocks on the mutex; this prevents two PUT /api/config calls
// from racing and leaving the system in a confused half-state.
type Coordinator struct {
	backend       network.Backend
	logger        *log.Logger
	healthChecker HealthChecker
	healthTimeout time.Duration

	// snapshotFn returns the current "running" config. Wired by
	// main.go to read out of api.Server's in-memory cfg, since the
	// coordinator itself doesn't own config persistence.
	snapshotFn func() config.Config

	// commitFn is called after a successful Apply to persist the
	// new config and update the in-memory snapshot source. Returns
	// non-nil if persistence failed — the apply is then rolled back
	// the same as a health-check failure (we don't want a healthy
	// runtime that doesn't survive reboot).
	commitFn func(config.Config) error

	mu       sync.Mutex
	current  *Attempt   // non-nil while an apply is in flight
	history  []*Attempt // ring buffer; newest at index 0
	historyN int
}

// Options configures NewCoordinator.
type Options struct {
	Backend       network.Backend
	Logger        *log.Logger
	HealthChecker HealthChecker // nil → DefaultHealthChecker
	HealthTimeout time.Duration // 0 → DefaultHealthTimeout
	SnapshotFn    func() config.Config
	CommitFn      func(config.Config) error
}

// NewCoordinator builds a Coordinator. SnapshotFn and CommitFn are
// required: the coordinator doesn't own the running config (the API
// server does), so it asks the caller for it via these closures.
func NewCoordinator(opts Options) (*Coordinator, error) {
	if opts.Backend == nil {
		return nil, errors.New("applycoord: Backend required")
	}
	if opts.SnapshotFn == nil {
		return nil, errors.New("applycoord: SnapshotFn required")
	}
	if opts.CommitFn == nil {
		return nil, errors.New("applycoord: CommitFn required")
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.HealthTimeout == 0 {
		opts.HealthTimeout = DefaultHealthTimeout
	}
	if opts.HealthChecker == nil {
		opts.HealthChecker = DefaultHealthChecker(opts.Backend)
	}
	return &Coordinator{
		backend:       opts.Backend,
		logger:        opts.Logger,
		healthChecker: opts.HealthChecker,
		healthTimeout: opts.HealthTimeout,
		snapshotFn:    opts.SnapshotFn,
		commitFn:      opts.CommitFn,
		history:       make([]*Attempt, 0, MaxHistory),
	}, nil
}

// Apply runs the transactional apply. Trigger is a short tag for
// diagnostics ("api:put-config", "setup:complete", ...).
//
// Returns the completed Attempt. The caller can inspect Status to
// distinguish Succeeded / RolledBack / Failed.
func (c *Coordinator) Apply(ctx context.Context, target config.Config, trigger string) *Attempt {
	c.mu.Lock()
	if c.current != nil {
		// Concurrent Apply — wait for the previous one to finish.
		// This is rare in practice (the API serializes most callers)
		// but defensive: a scheduler tick + a manual PUT could race.
		c.mu.Unlock()
		c.waitForIdle()
		c.mu.Lock()
	}
	att := &Attempt{
		ID:        newAttemptID(),
		Trigger:   trigger,
		Status:    StatusPending,
		StartedAt: time.Now().UTC(),
		Snapshot:  c.snapshotFn(),
		Target:    target,
	}
	c.current = att
	c.mu.Unlock()

	c.logger.Printf("apply[%s]: start (trigger=%s)", att.ID, trigger)

	defer func() {
		att.EndedAt = time.Now().UTC()
		c.mu.Lock()
		c.recordLocked(att)
		c.current = nil
		c.mu.Unlock()
		c.logger.Printf("apply[%s]: %s in %s",
			att.ID, att.Status, att.Duration().Round(10*time.Millisecond))
	}()

	// 1. backend.Apply.
	att.Status = StatusRunning
	if err := c.backend.Apply(ctx, target); err != nil {
		att.Error = fmt.Sprintf("apply: %v", err)
		c.rollback(ctx, att)
		return att
	}

	// 2. Persist the new config. If commit fails (disk full, sealer
	// error), we treat that the same as a health failure — runtime
	// is correct but won't survive reboot, so roll back.
	if err := c.commitFn(target); err != nil {
		att.Error = fmt.Sprintf("commit: %v", err)
		c.rollback(ctx, att)
		return att
	}

	// 3. Health check.
	att.Status = StatusHealthCheck
	hctx, cancel := context.WithTimeout(ctx, c.healthTimeout)
	defer cancel()
	if err := c.healthChecker.Check(hctx, target); err != nil {
		att.Error = fmt.Sprintf("healthcheck: %v", err)
		c.rollback(ctx, att)
		return att
	}

	att.Status = StatusSucceeded
	att.HealthCheckPassed = true
	return att
}

// rollback re-applies the snapshot. Best-effort: if rollback ALSO
// fails the attempt is marked StatusFailed (system in unknown state
// and the operator needs to investigate); otherwise StatusRolledBack.
func (c *Coordinator) rollback(ctx context.Context, att *Attempt) {
	c.logger.Printf("apply[%s]: rolling back (%s)", att.ID, att.Error)
	// Use a fresh context so a cancelled parent (the user clicked
	// away mid-apply) doesn't also cancel the rollback. Cap it at
	// 60s — on a Pi Zero 2W a worst-case backend.Apply is ~15s;
	// 60s is generous headroom.
	rctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := c.backend.Apply(rctx, att.Snapshot); err != nil {
		att.RollbackError = err.Error()
		att.Status = StatusFailed
		c.logger.Printf("apply[%s]: ROLLBACK FAILED: %v — system may be in inconsistent state",
			att.ID, err)
		return
	}
	if err := c.commitFn(att.Snapshot); err != nil {
		att.RollbackError = fmt.Sprintf("commit-rollback: %v", err)
		att.Status = StatusFailed
		return
	}
	att.Status = StatusRolledBack
}

// Get returns the named attempt or nil if unknown.
func (c *Coordinator) Get(id string) *Attempt {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current != nil && c.current.ID == id {
		cp := *c.current
		return &cp
	}
	for _, a := range c.history {
		if a.ID == id {
			cp := *a
			return &cp
		}
	}
	return nil
}

// Recent returns the last n completed attempts plus the in-flight one
// if any, newest first. n=0 returns everything currently buffered.
func (c *Coordinator) Recent(n int) []*Attempt {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]*Attempt, 0, len(c.history)+1)
	if c.current != nil {
		cp := *c.current
		out = append(out, &cp)
	}
	limit := len(c.history)
	if n > 0 && n < limit {
		limit = n
	}
	for i := 0; i < limit; i++ {
		cp := *c.history[i]
		out = append(out, &cp)
	}
	return out
}

// Current returns the in-flight attempt, or nil if none.
func (c *Coordinator) Current() *Attempt {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.current == nil {
		return nil
	}
	cp := *c.current
	return &cp
}

// recordLocked must be called with c.mu held.
func (c *Coordinator) recordLocked(a *Attempt) {
	cp := *a
	c.history = append([]*Attempt{&cp}, c.history...)
	if len(c.history) > MaxHistory {
		c.history = c.history[:MaxHistory]
	}
	c.historyN++
}

// waitForIdle blocks until the in-flight attempt finishes. Cheap
// busy-wait at 50ms granularity — concurrent applies are rare and
// short.
func (c *Coordinator) waitForIdle() {
	for {
		c.mu.Lock()
		idle := c.current == nil
		c.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newAttemptID returns 12 hex chars from crypto/rand. 48 bits of
// entropy — plenty for distinguishing apply attempts in a single
// device's lifetime.
func newAttemptID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
