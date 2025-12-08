package document

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"
	"google.golang.org/grpc"
)

type fakeRepo struct {
	lastStatus  model.Status
	lastVersion *model.DocumentVersion
	lastError   string
}

func (f *fakeRepo) CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error) {
	return nil, nil
}

func (f *fakeRepo) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	return nil, nil
}

func (f *fakeRepo) ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error) {
	return nil, nil
}

func (f *fakeRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	f.lastStatus = to
	f.lastVersion = version
	f.lastError = errorReason
	return &model.Document{ID: id, Status: to}, nil
}

func (f *fakeRepo) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	return &model.Document{ID: id, WorkflowID: workflowID}, nil
}

type fakeTransformerClient struct {
	lastRequest *transformerv1.ConvertDocxToMarkdownRequest
	resp        *transformerv1.ConvertDocxToMarkdownResponse
	err         error
}

func (f *fakeTransformerClient) ConvertDocxToMarkdown(ctx context.Context, in *transformerv1.ConvertDocxToMarkdownRequest, opts ...grpc.CallOption) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	f.lastRequest = in
	return f.resp, f.err
}

type fakeStorageClient struct {
	markdownReq *storagev1.UploadMarkdownRequest
	resp        *storagev1.UploadObjectResponse
	err         error
}

func (f *fakeStorageClient) UploadOriginal(ctx context.Context, in *storagev1.UploadOriginalRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	return nil, nil
}

func (f *fakeStorageClient) UploadMarkdown(ctx context.Context, in *storagev1.UploadMarkdownRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	f.markdownReq = in
	return f.resp, f.err
}

func (f *fakeStorageClient) GetObject(ctx context.Context, in *storagev1.GetObjectRequest, opts ...grpc.CallOption) (*storagev1.GetObjectResponse, error) {
	return nil, nil
}

func (f *fakeStorageClient) GenerateUploadURL(ctx context.Context, in *storagev1.GenerateUploadURLRequest, opts ...grpc.CallOption) (*storagev1.GenerateUploadURLResponse, error) {
	return nil, nil
}

type fakePublisher struct{}

func (fakePublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	return nil, nil
}

type trackingPublisher struct {
	called bool
}

func (p *trackingPublisher) PublishMarkdown(ctx context.Context, in *githubpublisherv1.PublishMarkdownRequest, opts ...grpc.CallOption) (*githubpublisherv1.PublishMarkdownResponse, error) {
	p.called = true
	return &githubpublisherv1.PublishMarkdownResponse{}, nil
}

func TestConvertToMarkdownUsesUpdatedStub(t *testing.T) {
	repo := &fakeRepo{}
	transformer := &fakeTransformerClient{resp: &transformerv1.ConvertDocxToMarkdownResponse{MarkdownContent: []byte("md"), MediaType: "text/markdown", MarkdownObjectKey: "md-key"}}
	storage := &fakeStorageClient{}

	activities := NewActivities(repo, storage, transformer, fakePublisher{}, "owner", "repo", "main", "main", "https://api.github.com")

	result, err := activities.ConvertToMarkdown(context.Background(), convertInput{
		DocumentID:        "doc-1",
		OriginalBucket:    "orig-bucket",
		MarkdownBucket:    "md-bucket",
		OriginalObjectKey: "path/doc-1.docx",
		MarkdownObjectKey: "path/doc-1.md",
		Version:           3,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if transformer.lastRequest == nil {
		t.Fatal("transformer request was not sent")
	}

	if transformer.lastRequest.GetDocumentId() != "doc-1" || transformer.lastRequest.GetOriginalBucket() != "orig-bucket" {
		t.Fatalf("unexpected transformer request: %+v", transformer.lastRequest)
	}

	if transformer.lastRequest.GetMarkdownObjectKey() != "path/doc-1.md" {
		t.Fatalf("unexpected markdown key: %s", transformer.lastRequest.GetMarkdownObjectKey())
	}

	if result.MediaType != "text/markdown" {
		t.Fatalf("unexpected media type: %s", result.MediaType)
	}

	if repo.lastStatus != model.StatusConvertedToMarkdown {
		t.Fatalf("status was not updated, got %v", repo.lastStatus)
	}

	if repo.lastVersion == nil || repo.lastVersion.Version != 3 || repo.lastVersion.MarkdownObjectKey != "md-key" {
		t.Fatalf("version was not saved correctly: %+v", repo.lastVersion)
	}
}

func TestPublishMarkdownValidatesRepositoryBeforePublish(t *testing.T) {
	repo := &fakeRepo{}
	publisher := &trackingPublisher{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	activities := NewActivities(repo, nil, nil, publisher, "owner", "repo", "main", "main", server.URL)
	activities.httpClient = server.Client()

	err := activities.PublishMarkdown(context.Background(), publishInput{
		DocumentID:        "doc-1",
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object",
		Path:              "doc.md",
	})
	if err == nil {
		t.Fatal("expected validation error")
	}

	if repo.lastStatus != model.StatusFailed {
		t.Fatalf("expected failed status, got %v", repo.lastStatus)
	}
	if publisher.called {
		t.Fatal("publisher should not be called after failed validation")
	}
}

func TestPublishMarkdownContinuesWhenRepositoryAccessible(t *testing.T) {
	repo := &fakeRepo{}
	publisher := &trackingPublisher{}
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	activities := NewActivities(repo, nil, nil, publisher, "owner", "repo", "main", "main", server.URL)
	activities.httpClient = server.Client()

	err := activities.PublishMarkdown(context.Background(), publishInput{
		DocumentID:        "doc-2",
		DocumentName:      "doc title",
		MarkdownBucket:    "bucket",
		MarkdownObjectKey: "object",
		Path:              "doc.md",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if requestedPath != "/repos/owner/repo" {
		t.Fatalf("unexpected validation path: %s", requestedPath)
	}
	if !publisher.called {
		t.Fatal("publisher was not called")
	}
	if repo.lastStatus != model.StatusPublishedToGitHub {
		t.Fatalf("expected published status, got %v", repo.lastStatus)
	}
}
