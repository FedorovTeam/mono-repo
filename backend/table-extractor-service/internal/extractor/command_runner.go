package extractor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

type ExternalCommandRunner struct {
	sem chan struct{}
}

func NewExternalCommandRunner(maxParallel int) *ExternalCommandRunner {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	return &ExternalCommandRunner{
		sem: make(chan struct{}, maxParallel),
	}
}

func (r *ExternalCommandRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (CommandResult, error) {
	var zero CommandResult

	acquired := r.acquire(ctx)
	if !acquired {
		return zero, ctx.Err()
	}
	defer r.release()

	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(runCtx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, fmt.Errorf("%s timed out", name)
		}
		return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
	}

	return CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, nil
}

func (r *ExternalCommandRunner) acquire(ctx context.Context) bool {
	select {
	case r.sem <- struct{}{}:
		return true
	case <-ctx.Done():
		return false
	}
}

func (r *ExternalCommandRunner) release() {
	select {
	case <-r.sem:
	default:
	}
}
