package document

import (
	"context"
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

func (f *fakeRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason string, version *model.DocumentVersion) (*model.Document, error) {
	f.lastStatus = to
	f.lastVersion = version
	return &model.Document{ID: id, Status: to}, nil
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

func TestConvertToMarkdownUsesUpdatedStub(t *testing.T) {
	repo := &fakeRepo{}
	transformer := &fakeTransformerClient{resp: &transformerv1.ConvertDocxToMarkdownResponse{MarkdownContent: []byte("md"), MediaType: "text/markdown", MarkdownObjectKey: "md-key"}}
	storage := &fakeStorageClient{}

	activities := NewActivities(repo, storage, transformer, fakePublisher{}, "repo", "main")

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
