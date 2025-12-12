package document

import (
	"context"
	"testing"

	"backend-document-service/internal/domain"
	"backend-document-service/internal/model"
)

type statusRepoStub struct {
	doc                *model.Document
	lastUpdateTo       domain.DocumentStatus
	lastReason         string
	lastErrorReason    string
	lastVersion        *model.DocumentVersion
	updateErr          error
	updateCount        int
	lastNonFailed      domain.DocumentStatus
	lastNonFailedCalls int
}

func (s *statusRepoStub) GetDocument(context.Context, string) (*model.Document, error) {
	return s.doc, nil
}

func (s *statusRepoStub) UpdateStatus(_ context.Context, _ string, to domain.DocumentStatus, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	s.updateCount++
	s.lastUpdateTo = to
	s.lastReason = reason
	s.lastErrorReason = errorReason
	s.lastVersion = version
	return s.doc, s.updateErr
}

func (s *statusRepoStub) LastNonFailedStatus(context.Context, string) (domain.DocumentStatus, error) {
	s.lastNonFailedCalls++
	return s.lastNonFailed, nil
}

func TestStatusApplicationTransitionWritesReason(t *testing.T) {
	repo := &statusRepoStub{doc: &model.Document{ID: "doc-1", Status: model.StatusUploaded}}
	app := NewStatusApplication(repo)

	version := &model.DocumentVersion{Version: 1, OriginalObjectKey: "orig"}
	updated, err := app.TransitionByID(context.Background(), repo.doc.ID, model.StatusStoredOriginal, "stored", "", version)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatalf("expected updated document")
	}
	if repo.updateCount != 1 {
		t.Fatalf("expected single update, got %d", repo.updateCount)
	}
	if repo.lastUpdateTo != model.StatusStoredOriginal {
		t.Fatalf("unexpected target status: %s", repo.lastUpdateTo)
	}
	if repo.lastReason != "stored" {
		t.Fatalf("expected reason to be propagated, got %q", repo.lastReason)
	}
	if repo.lastVersion == nil || repo.lastVersion.Version != 1 || repo.lastVersion.OriginalObjectKey != "orig" {
		t.Fatalf("unexpected version payload: %+v", repo.lastVersion)
	}
}

func TestStatusApplicationRetriesFromFailed(t *testing.T) {
	repo := &statusRepoStub{
		doc:           &model.Document{ID: "doc-2", Status: model.StatusFailed},
		lastNonFailed: domain.DocumentStatusConvertedToMarkdown,
	}
	app := NewStatusApplication(repo)

	if _, err := app.Transition(context.Background(), repo.doc, model.StatusPublishedToGitHub, "retry", "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.lastNonFailedCalls == 0 {
		t.Fatal("expected last non failed status to be requested")
	}
	if repo.lastUpdateTo != model.StatusPublishedToGitHub {
		t.Fatalf("unexpected transition target: %s", repo.lastUpdateTo)
	}
	if repo.lastReason != "retry" {
		t.Fatalf("expected retry reason to be logged, got %q", repo.lastReason)
	}
}
