package extractor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"

	"go.uber.org/zap"

	"github.com/mono-repo/table-extractor-service/internal/telemetry"
)

type ExternalCommandRunner struct {
	sem    chan struct{}
	logger *zap.Logger
}

func NewExternalCommandRunner(maxParallel int, logger *zap.Logger) *ExternalCommandRunner {
	if maxParallel <= 0 {
		maxParallel = 1
	}
	return &ExternalCommandRunner{
		sem:    make(chan struct{}, maxParallel),
		logger: logger,
	}
}

func (r *ExternalCommandRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (CommandResult, error) {
	var zero CommandResult
	logger := r.resolveLogger()
	start := time.Now()
	fields := []zap.Field{
		zap.String("command", name),
		zap.Strings("args", args),
		zap.Duration("timeout", timeout),
		zap.String("correlation_id", telemetry.CorrelationIDFromContext(ctx)),
	}
	logger.Debug("command run started", fields...)

	acquired := r.acquire(ctx)
	if !acquired {
		logger.Error("command run cancelled before start", append(fields, zap.Duration("duration", time.Since(start)), zap.Error(ctx.Err()))...)
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
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	duration := time.Since(start)
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	exitCode := commandExitCode(cmd.ProcessState, err)

	logFields := append(fields,
		zap.Duration("duration", duration),
		zap.Int("stdout_bytes", len(result.Stdout)),
		zap.Int("stderr_bytes", len(result.Stderr)),
		zap.Int("exit_code", exitCode),
		zap.Bool("timed_out", timedOut),
	)

	if timedOut {
		err = fmt.Errorf("%s timed out", name)
	}

	if err != nil {
		logger.Error("command run failed", append(logFields, zap.Error(err))...)
		return result, err
	}

	logger.Debug("command run finished", logFields...)
	return result, nil
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

func (r *ExternalCommandRunner) resolveLogger() *zap.Logger {
	if r.logger != nil {
		return r.logger
	}
	return zap.NewNop()
}

func commandExitCode(state *os.ProcessState, runErr error) int {
	if state != nil {
		return state.ExitCode()
	}

	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		return exitErr.ExitCode()
	}

	if runErr != nil {
		return -1
	}

	return 0
}
