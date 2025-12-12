package document

import (
	"context"
	"testing"

	"backend-document-service/internal/domain"
)

type statusRepoStub struct {
	doc                *domain.Document
	lastUpdateTo       domain.DocumentStatus
	lastReason         string
	lastErrorReason    string
	lastVersion        *domain.DocumentVersion
	updateErr          error
	updateCount        int
	lastNonFailed      domain.DocumentStatus
	lastNonFailedCalls int
}

func (s *statusRepoStub) GetDocument(context.Context, string) (*domain.Document, error) {
	return s.doc, nil
}

func (s *statusRepoStub) UpdateStatus(_ context.Context, _ string, to domain.DocumentStatus, reason, errorReason string, version *domain.DocumentVersion) (*domain.Document, error) {
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
	repo := &statusRepoStub{doc: &domain.Document{ID: "doc-1", Status: domain.DocumentStatusUploaded}}
	app := NewStatusApplication(repo)

	version := &domain.DocumentVersion{Version: 1, OriginalObjectKey: "orig"}
	updated, err := app.TransitionByID(context.Background(), repo.doc.ID, domain.DocumentStatusStoredOriginal, "stored", "", version)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if updated == nil {
		t.Fatalf("expected updated document")
	}
	if repo.updateCount != 1 {
		t.Fatalf("expected single update, got %d", repo.updateCount)
	}
	if repo.lastUpdateTo != domain.DocumentStatusStoredOriginal {
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
		doc:           &domain.Document{ID: "doc-2", Status: domain.DocumentStatusFailed},
		lastNonFailed: domain.DocumentStatusConvertedToMarkdown,
	}
	app := NewStatusApplication(repo)

	if _, err := app.Transition(context.Background(), repo.doc, domain.DocumentStatusPublishedToGitHub, "retry", "", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if repo.lastNonFailedCalls == 0 {
		t.Fatal("expected last non failed status to be requested")
	}
	if repo.lastUpdateTo != domain.DocumentStatusPublishedToGitHub {
		t.Fatalf("unexpected transition target: %s", repo.lastUpdateTo)
	}
	if repo.lastReason != "retry" {
		t.Fatalf("expected retry reason to be logged, got %q", repo.lastReason)
	}
}
