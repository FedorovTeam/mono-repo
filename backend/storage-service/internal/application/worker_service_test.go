package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"backend-storage-service/internal/domain"
)

type stubTx struct {
	manager    *stubTxManager
	committed  bool
	rolledBack bool
}

func (s *stubTx) Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, nil
}
func (s *stubTx) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row { return nil }
func (s *stubTx) Commit(ctx context.Context) error {
	s.committed = true
	if s.manager != nil {
		s.manager.commits++
	}
	return nil
}
func (s *stubTx) Rollback(ctx context.Context) error {
	s.rolledBack = true
	if s.manager != nil {
		s.manager.rollbacks++
	}
	return nil
}

type stubTxManager struct {
	tx        *stubTx
	commits   int
	rollbacks int
}

type stubJobs struct {
	job           *domain.ProcessingJob
	err           error
	retriesCalled int
	calls         int
}

type stubDocuments struct{ err error }

func (m *stubTxManager) Begin(ctx context.Context) (Tx, error) {
	m.tx = &stubTx{manager: m}
	return m.tx, nil
}

func (s *stubJobs) SelectPendingForStage(ctx context.Context, tx Tx, stage string) (*domain.ProcessingJob, error) {
	s.calls++
	if s.calls > 1 && s.err == nil {
		return nil, ErrNoJobs
	}
	return s.job, s.err
}

func (s *stubJobs) MarkInProgress(ctx context.Context, tx Tx, jobID, stage, message string) error {
	return nil
}
func (s *stubJobs) MarkCompleted(ctx context.Context, tx Tx, jobID, stage, message string) error {
	return nil
}
func (s *stubJobs) RescheduleOrFail(ctx context.Context, tx Tx, job *domain.ProcessingJob, stage string, reason error, maxAttempts int, backoff time.Duration) error {
	s.retriesCalled++
	return nil
}

func (s *stubDocuments) MarkStatus(ctx context.Context, tx Tx, documentID string, status domain.DocumentStatus, payload map[string]any, reason string) error {
	return s.err
}

func TestWorkerService_SuccessfulJob(t *testing.T) {
	txManager := &stubTxManager{}
	jobs := &stubJobs{job: &domain.ProcessingJob{TaskID: "task", DocumentID: "doc"}}
	documents := &stubDocuments{}

	service := NewWorkerService(WorkerConfig{PollInterval: time.Millisecond * 10}, txManager, jobs, documents, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go service.Run(ctx)
	time.Sleep(time.Millisecond * 30)
	cancel()

	if txManager.commits == 0 {
		t.Fatalf("expected transaction commit")
	}
}

func TestWorkerService_DocumentErrorTriggersRetry(t *testing.T) {
	txManager := &stubTxManager{}
	jobs := &stubJobs{job: &domain.ProcessingJob{TaskID: "task", DocumentID: "doc", Attempts: 1}}
	documents := &stubDocuments{err: errors.New("boom")}

	service := NewWorkerService(WorkerConfig{PollInterval: time.Millisecond * 10}, txManager, jobs, documents, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go service.Run(ctx)
	time.Sleep(time.Millisecond * 30)
	cancel()

	if jobs.retriesCalled == 0 {
		t.Fatalf("expected retry to be scheduled")
	}
}

func TestWorkerService_NoJobsStopsLoop(t *testing.T) {
	txManager := &stubTxManager{}
	jobs := &stubJobs{err: ErrNoJobs}
	documents := &stubDocuments{}

	service := NewWorkerService(WorkerConfig{PollInterval: time.Millisecond * 5}, txManager, jobs, documents, nil)

	ctx, cancel := context.WithCancel(context.Background())
	go service.Run(ctx)
	time.Sleep(time.Millisecond * 20)
	cancel()

	if txManager.rollbacks == 0 {
		t.Fatalf("expected rollback on no jobs")
	}
}
