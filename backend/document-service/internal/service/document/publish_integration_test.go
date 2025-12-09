package document

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc"
)

func TestPublishMarkdownWithStoredContentAndGithubResponses(t *testing.T) {
	t.Parallel()

	t.Run("publishes markdown from storage when repository exists", func(t *testing.T) {
		t.Parallel()

		repo := &integrationRepo{}
		publisher := &recordingPublisher{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/owner/repo" {
				t.Fatalf("unexpected validation path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		activities := NewActivities(repo, nil, nil, publisher, "owner", "repo", "main", "main", server.URL)
		activities.httpClient = server.Client()

		err := activities.PublishMarkdown(context.Background(), publishInput{
			DocumentID:        "doc-1",
			MarkdownBucket:    "bucket",
			MarkdownObjectKey: "object-key",
			Path:              "docs/doc-1.md",
			Version:           2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if repo.lastStatus != model.StatusPublishedToGitHub {
			t.Fatalf("expected status %v, got %v", model.StatusPublishedToGitHub, repo.lastStatus)
		}
		if repo.lastVersion == nil || repo.lastVersion.Version != 2 {
			t.Fatalf("unexpected version saved: %+v", repo.lastVersion)
		}

		if !publisher.called {
			t.Fatal("publisher was not called")
		}
		if publisher.lastReq == nil {
			t.Fatal("publisher request was not recorded")
		}
		if publisher.lastReq.GetContent() != "" {
			t.Fatalf("expected markdown to be read from storage, got inline content: %q", publisher.lastReq.GetContent())
		}
		if publisher.lastReq.GetBucket() != "bucket" || publisher.lastReq.GetObjectKey() != "object-key" {
			t.Fatalf("unexpected storage reference: %s/%s", publisher.lastReq.GetBucket(), publisher.lastReq.GetObjectKey())
		}
	})

	t.Run("fails when github repository not found", func(t *testing.T) {
		t.Parallel()

		repo := &integrationRepo{}
		publisher := &recordingPublisher{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/repos/owner/repo" {
				t.Fatalf("unexpected validation path: %s", r.URL.Path)
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer server.Close()

		activities := NewActivities(repo, nil, nil, publisher, "owner", "repo", "main", "main", server.URL)
		activities.httpClient = server.Client()

		err := activities.PublishMarkdown(context.Background(), publishInput{
			DocumentID:        "doc-404",
			MarkdownBucket:    "bucket",
			MarkdownObjectKey: "missing.md",
			Path:              "docs/missing.md",
			Version:           5,
		})
		if err == nil {
			t.Fatal("expected error for missing repository")
		}

		if repo.lastStatus != model.StatusFailed {
			t.Fatalf("expected failed status, got %v", repo.lastStatus)
		}
		if repo.lastError == "" {
			t.Fatal("expected github error to be preserved in repo")
		}
		if publisher.called {
			t.Fatal("publisher should not be called when validation fails")
		}
	})
}

type integrationRepo struct {
	lastStatus  model.Status
	lastVersion *model.DocumentVersion
	lastError   string
}

func (r *integrationRepo) CreateDocument(context.Context, *model.Document, *model.DocumentVersion, *model.DocumentStatusLog) (*model.Document, error) {
	return nil, nil
}

func (r *integrationRepo) GetDocument(_ context.Context, id string) (*model.Document, error) {
	return &model.Document{ID: id, Status: model.StatusStoredOriginal, MarkdownBucket: "bucket", MarkdownObjectKey: "object-key", Versions: []model.DocumentVersion{{Version: 1, MarkdownObjectKey: "object-key"}}}, nil
}

func (r *integrationRepo) ListDocuments(context.Context, int, int, *model.Status) ([]model.Document, error) {
	return nil, nil
}

func (r *integrationRepo) CountStatusLogsWithReasonPrefix(context.Context, string, model.Status, string) (int, error) {
	return 0, nil
}

func (r *integrationRepo) LastNonFailedStatus(context.Context, string) (model.Status, error) {
	return model.Status(""), nil
}

func (r *integrationRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	r.lastStatus = to
	r.lastVersion = version
	r.lastError = errorReason
	return &model.Document{ID: id, Status: to}, nil
}

func (r *integrationRepo) UpdateWorkflowID(context.Context, string, string) (*model.Document, error) {
	return nil, nil
}

type recordingPublisher struct {
	called  bool
	lastReq *githubpublisherv1.PublishMarkdownRequest
	err     error
}

func (p *recordingPublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	p.called = true
	p.lastReq = in
	if p.err != nil {
		return nil, p.err
	}
	return &githubpublisherv1.PublishMarkdownResponse{CommitSha: "integration-sha"}, nil
}
