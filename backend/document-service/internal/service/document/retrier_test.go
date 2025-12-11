package document

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"

	"backend-document-service/internal/model"
)

type workflowRunStub struct{}

func (workflowRunStub) GetID() string    { return "run-123" }
func (workflowRunStub) GetRunID() string { return "run-123" }
func (workflowRunStub) Get(ctx context.Context, valuePtr interface{}) error {
	return nil
}
func (workflowRunStub) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	return nil
}

type workflowStarterStub struct {
	calls int
	run   client.WorkflowRun
	err   error
}

func (w *workflowStarterStub) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	w.calls++
	if w.run != nil {
		return w.run, w.err
	}
	return workflowRunStub{}, w.err
}

func TestWorkflowRetrierRestartsWorkflow(t *testing.T) {
	doc := model.Document{ID: uuid.NewString(), WorkflowID: "corr-id", ErrorReason: startWorkflowErrorPrefix + " network", Status: model.StatusFailed}
	repo := &repoMock{docs: []model.Document{doc}, doc: &doc}
	starter := &workflowStarterStub{run: workflowRunStub{}}
	retrier := NewWorkflowRetrier(repo, starter, "queue", 10, 3)

	retrier.reconcile(context.Background())

	require.Equal(t, 1, starter.calls)
	require.Equal(t, "run-123", repo.workflowID)
	require.Equal(t, model.StatusInProgress, repo.savedStatus)
}

func TestWorkflowRetrierRespectsAttemptLimit(t *testing.T) {
	doc := model.Document{ID: uuid.NewString(), ErrorReason: startWorkflowErrorPrefix + " network", Status: model.StatusFailed}
	repo := &repoMock{docs: []model.Document{doc}, doc: &doc, attempts: 3}
	starter := &workflowStarterStub{run: workflowRunStub{}}
	retrier := NewWorkflowRetrier(repo, starter, "queue", 10, 3)

	retrier.reconcile(context.Background())

	require.Zero(t, starter.calls)
}

func TestWorkflowRetrierLogsCorrelationIDOnFailure(t *testing.T) {
	doc := model.Document{ID: uuid.NewString(), WorkflowID: "wf-1", ErrorReason: startWorkflowErrorPrefix + " unavailable", Status: model.StatusFailed}
	repo := &repoMock{docs: []model.Document{doc}, doc: &doc}
	starter := &workflowStarterStub{err: errors.New("temporal down")}
	retrier := NewWorkflowRetrier(repo, starter, "queue", 10, 3)
	var logs []string
	retrier.logf = func(format string, args ...interface{}) { logs = append(logs, fmt.Sprintf(format, args...)) }

	retrier.reconcile(context.Background())

	require.Equal(t, model.StatusFailed, repo.savedStatus)
	require.True(t, strings.Contains(strings.Join(logs, "\n"), "correlation_id=wf-1"))
}
