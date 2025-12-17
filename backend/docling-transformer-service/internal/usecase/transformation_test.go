package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/mono-repo/docling-transformer-service/internal/backoff"
	"github.com/mono-repo/docling-transformer-service/internal/domain"
	transformations "github.com/mono-repo/docling-transformer-service/internal/domain/transformations"
)

type pollingRepository struct {
	pollingRepoStub
	tasks []*transformations.TransformationTask
	calls int
}

func (r *pollingRepository) GetByID(context.Context, uuid.UUID) (*transformations.TransformationTask, error) {
	r.calls++
	if len(r.tasks) == 0 {
		return nil, nil
	}
	if r.calls-1 < len(r.tasks) {
		return r.tasks[r.calls-1], nil
	}

	return r.tasks[len(r.tasks)-1], nil
}

type checkingConverter struct {
	domain.Converter
	statuses []domain.StatusCheckResult
	calls    int
}

func (c *checkingConverter) Submit(context.Context, domain.ConvertRequest) (domain.DoclingIdentifiers, error) {
	return domain.DoclingIdentifiers{TaskID: "job"}, nil
}

func (c *checkingConverter) CheckStatus(context.Context, domain.DoclingIdentifiers) (domain.StatusCheckResult, error) {
	c.calls++
	if c.calls-1 < len(c.statuses) {
		return c.statuses[c.calls-1], nil
	}

	return c.statuses[len(c.statuses)-1], nil
}

func TestTransformerServiceUsesSharedBackoffLimits(t *testing.T) {
	policy := backoff.Policy{
		InitialInterval: time.Millisecond,
		MaxInterval:     time.Millisecond,
		MaxAttempts:     2,
		Factor:          2,
	}

	repoStub := &pollingRepository{
		tasks: []*transformations.TransformationTask{{Status: transformations.StatusPending}},
	}
	conv := &checkingConverter{statuses: []domain.StatusCheckResult{{Status: "processing"}}}
	service := NewTransformerService(repoStub, conv, TransformationConfig{StatusBackoff: policy}, nil, nil)

	if _, err := service.WaitForResult(context.Background(), uuid.New()); err != ErrResultNotReady {
		t.Fatalf("expected result not ready, got %v", err)
	}

	if repoStub.calls != policy.MaxAttempts {
		t.Fatalf("expected %d repository calls, got %d", policy.MaxAttempts, repoStub.calls)
	}

	if _, err := service.pollUntilReady(context.Background(), domain.DoclingIdentifiers{TaskID: "job"}); err != ErrResultNotReady {
		t.Fatalf("expected result not ready from docling, got %v", err)
	}

	if conv.calls != policy.MaxAttempts {
		t.Fatalf("expected %d status checks, got %d", policy.MaxAttempts, conv.calls)
	}
}
