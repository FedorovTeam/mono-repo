package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRunnerProcessesAndMaintainsQueue(t *testing.T) {
	proc := &mockProcessor{
		processQueue: []bool{true, false, false, true},
	}

	runner := NewRunner(Config{
		MaxWorkers:          1,
		ProcessingTimeout:   30 * time.Millisecond,
		MaintenanceInterval: 20 * time.Millisecond,
		Retry: RetryConfig{
			MaxAttempts:     3,
			BackoffInterval: 5 * time.Millisecond,
			MaxBackoff:      25 * time.Millisecond,
		},
	}, proc, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	require.NoError(t, runner.Start(ctx))
	require.GreaterOrEqual(t, proc.processCalls(), 2)
	require.GreaterOrEqual(t, proc.restoreCalls(), 1)
	require.GreaterOrEqual(t, proc.retryCalls(), 1)
	require.GreaterOrEqual(t, proc.cleanupCalls(), 1)
}

func TestRunnerWithoutProcessorExitsOnContext(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	runner := NewRunner(Config{}, nil, zap.NewNop())

	err := runner.Start(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

type mockProcessor struct {
	mu           sync.Mutex
	processQueue []bool
	processCnt   int
	restoreCnt   int
	retryCnt     int
	cleanupCnt   int
}

func (m *mockProcessor) ProcessExtractionJob(context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.processCnt++
	if len(m.processQueue) == 0 {
		return false, nil
	}
	res := m.processQueue[0]
	m.processQueue = m.processQueue[1:]
	return res, nil
}

func (m *mockProcessor) RestoreStuckProcessing(context.Context, time.Duration, int, time.Duration, time.Duration) (int, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoreCnt++
	return 1, 0, nil
}

func (m *mockProcessor) RetryFailed(context.Context, int, time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retryCnt++
	return 1, nil
}

func (m *mockProcessor) CleanupFinished(context.Context, time.Duration) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanupCnt++
	return 1, nil
}

func (m *mockProcessor) processCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.processCnt
}

func (m *mockProcessor) restoreCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.restoreCnt
}

func (m *mockProcessor) retryCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.retryCnt
}

func (m *mockProcessor) cleanupCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cleanupCnt
}
