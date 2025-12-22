package worker

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/mono-repo/table-extractor-service/internal/domain"
)

func TestRunnerProcessesAndMaintainsQueue(t *testing.T) {
	proc := &mockProcessor{
		processQueue: []bool{true, false, false, true},
		restoredJobs: []domain.TableExtractionJob{testJob("corr-restore", domain.StatusPending)},
		retriedJobs:  []domain.TableExtractionJob{testJob("corr-retry", domain.StatusPending)},
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

	restoredJobs []domain.TableExtractionJob
	failedJobs   []domain.TableExtractionJob
	retriedJobs  []domain.TableExtractionJob
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

func (m *mockProcessor) RestoreStuckProcessing(context.Context, time.Duration, int, time.Duration, time.Duration) ([]domain.TableExtractionJob, []domain.TableExtractionJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoreCnt++
	return append([]domain.TableExtractionJob{}, m.restoredJobs...), append([]domain.TableExtractionJob{}, m.failedJobs...), nil
}

func (m *mockProcessor) RetryFailed(context.Context, int, time.Duration) ([]domain.TableExtractionJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retryCnt++
	return append([]domain.TableExtractionJob{}, m.retriedJobs...), nil
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

func TestRunnerLogsCorrelationAndStatus(t *testing.T) {
	core, observed := observer.New(zapcore.DebugLevel)
	logger := zap.New(core)
	proc := &mockProcessor{
		restoredJobs: []domain.TableExtractionJob{testJob("corr-restored", domain.StatusPending)},
		failedJobs:   []domain.TableExtractionJob{testJob("corr-failed", domain.StatusFailed)},
		retriedJobs:  []domain.TableExtractionJob{testJob("corr-retried", domain.StatusPending)},
	}

	runner := NewRunner(Config{
		MaxWorkers:          0,
		MaintenanceInterval: 5 * time.Millisecond,
		Retry: RetryConfig{
			MaxAttempts:     3,
			BackoffInterval: 1 * time.Millisecond,
		},
	}, proc, logger)

	runner.runMaintenanceCycle(context.Background())

	require.Truef(t, hasFieldWithValue(observed.All(), "restored_correlation_ids", "corr-restored"), "logs: %+v", observed.All())
	require.Truef(t, hasFieldWithValue(observed.All(), "failed_correlation_ids", "corr-failed"), "logs: %+v", observed.All())
	require.Truef(t, hasFieldWithValue(observed.All(), "correlation_ids", "corr-retried"), "logs: %+v", observed.All())
	require.Truef(t, hasFieldWithValue(observed.All(), "restored_status", string(domain.StatusPending)), "logs: %+v", observed.All())
	require.Truef(t, hasFieldWithValue(observed.All(), "status", string(domain.StatusPending)), "logs: %+v", observed.All())
	require.GreaterOrEqual(t, proc.restoreCalls(), 1, "logs: %+v", observed.All())
	require.GreaterOrEqual(t, proc.retryCalls(), 1, "logs: %+v", observed.All())
}

func hasFieldWithValue(entries []observer.LoggedEntry, key, value string) bool {
	for _, entry := range entries {
		for _, field := range entry.Context {
			if field.Key == key {
				if array, ok := field.Interface.([]string); ok {
					for _, item := range array {
						if item == value {
							return true
						}
					}
				}
				if rawArray, ok := field.Interface.([]interface{}); ok {
					for _, item := range rawArray {
						if str, ok := item.(string); ok && str == value {
							return true
						}
					}
				}
				if strings.Contains(fmt.Sprint(field.Interface), value) {
					return true
				}
				if field.String == value {
					return true
				}
			}
		}
	}
	return false
}

func testJob(correlationID string, status domain.TableExtractionStatus) domain.TableExtractionJob {
	return domain.TableExtractionJob{
		ID:            uuid.New(),
		DocumentID:    "doc-" + correlationID,
		Status:        status,
		CorrelationID: correlationID,
	}
}
