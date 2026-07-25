//go:build linux

package linux

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// commandRunner is how the backend shells out. The production impl is
// *runner (real exec); tests inject a fake to drive mmcli/ip/nft output
// without touching the host. Every LinuxBackend command goes through this
// (b.r), so faking it makes the modem/apply paths unit-testable.
type commandRunner interface {
	run(ctx context.Context, name string, args ...string) (string, error)
	runOK(ctx context.Context, name string, args ...string) error
	runIgnoreError(ctx context.Context, name string, args ...string)
}

// runner is a thin wrapper around exec.CommandContext that adds a
// default timeout and capture of stdout/stderr. It centralizes how
// every Linux subsystem call is logged.
type runner struct {
	logger  *log.Logger
	timeout time.Duration
}

var _ commandRunner = (*runner)(nil)

func newRunner(logger *log.Logger) *runner {
	if logger == nil {
		logger = log.Default()
	}
	return &runner{logger: logger, timeout: 30 * time.Second}
}

// run executes the command and returns stdout. Stderr is captured and
// included verbatim in the returned error on failure.
func (r *runner) run(ctx context.Context, name string, args ...string) (string, error) {
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	r.logger.Printf("exec: %s %s", name, strings.Join(args, " "))
	err := cmd.Run()
	out := strings.TrimRight(stdout.String(), "\n")
	if err != nil {
		errOut := strings.TrimRight(stderr.String(), "\n")
		return out, fmt.Errorf("%s %s: %w (stderr: %s)", name, strings.Join(args, " "), err, errOut)
	}
	return out, nil
}

// runOK runs a command and discards stdout, returning only the error.
func (r *runner) runOK(ctx context.Context, name string, args ...string) error {
	_, err := r.run(ctx, name, args...)
	return err
}

// runIgnoreError runs a command but never returns an error — used for
// best-effort cleanup steps where "the thing is already gone" is fine.
func (r *runner) runIgnoreError(ctx context.Context, name string, args ...string) {
	if err := r.runOK(ctx, name, args...); err != nil {
		r.logger.Printf("ignored: %v", err)
	}
}
