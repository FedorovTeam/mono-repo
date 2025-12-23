package graph

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"

	"backend-gateway-service/graph/model"
	"backend-gateway-service/internal/domain"
	"backend-gateway-service/internal/usecase"
)

type stubUploader struct {
	lastCtx context.Context
	input   usecase.UploadInput
	result  *usecase.UploadResult
	err     error
}

func (s *stubUploader) UploadKnowledgeDocument(ctx context.Context, input usecase.UploadInput) (*usecase.UploadResult, error) {
	s.lastCtx = ctx
	s.input = input
	return s.result, s.err
}

type stubProvider struct {
	getFunc  func(ctx context.Context, id string) (*domain.Document, error)
	listFunc func(ctx context.Context, filter domain.DocumentFilter, pagination domain.Pagination) (*domain.DocumentList, error)
}

func (s *stubProvider) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	return s.getFunc(ctx, id)
}

func (s *stubProvider) ListDocuments(ctx context.Context, filter domain.DocumentFilter, pagination domain.Pagination) (*domain.DocumentList, error) {
	return s.listFunc(ctx, filter, pagination)
}

func TestUploadRequiresAPIKey(t *testing.T) {
	resolver := &Resolver{DocumentUploader: &stubUploader{}, DocumentService: &stubProvider{}, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	_, err := mr.UploadKnowledgeDocument(context.Background(), model.UploadKnowledgeDocumentInput{})
	if err == nil {
		t.Fatalf("expected api key error")
	}
}

func TestUploadUsesUseCaseAndCorrelationID(t *testing.T) {
	uploader := &stubUploader{result: &usecase.UploadResult{Document: &domain.Document{ID: "doc-1", FileName: "file.docx", Repository: "org/repo", Branch: "main", Path: "docs/file.md", Status: domain.DocumentStatusUploaded, ExtractTables: true, ConversionStrategy: domain.ConversionStrategyDoclingWithTables}, UploadURL: "http://upload", UploadMethod: "PUT"}}
	resolver := &Resolver{DocumentUploader: uploader, DocumentService: &stubProvider{}, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	ctx := ctxutils.WithAPIKey(context.Background(), "secret")
	path := strPtr("docs/file.md")
	extract := true
	strategy := model.ConversionStrategyDoclingWithTables
	payload, err := mr.UploadKnowledgeDocument(ctx, model.UploadKnowledgeDocumentInput{FileName: "file.docx", Repository: "org/repo", Branch: "main", Path: path, FileSizeBytes: 100, ExtractTables: &extract, ConversionStrategy: &strategy})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if payload.UploadURL != "http://upload" || payload.UploadMethod != "PUT" {
		t.Fatalf("unexpected upload data: %+v", payload)
	}

	if payload.Document == nil || payload.Document.ID != "doc-1" {
		t.Fatalf("document not mapped: %+v", payload.Document)
	}
	if !payload.Document.ExtractTables {
		t.Fatalf("extractTables should be true in response")
	}
	if payload.Document.ConversionStrategy == nil || *payload.Document.ConversionStrategy != model.ConversionStrategyDoclingWithTables {
		t.Fatalf("conversionStrategy not mapped: %+v", payload.Document.ConversionStrategy)
	}

	if ctxutils.CorrelationID(uploader.lastCtx) == "" {
		t.Fatalf("correlation id not ensured")
	}
	if !uploader.input.ExtractTables {
		t.Fatalf("extractTables not passed to usecase")
	}
	if uploader.input.ConversionStrategy != domain.ConversionStrategyDoclingWithTables {
		t.Fatalf("conversionStrategy not passed to usecase")
	}
}

func TestUploadValidationErrorFromUseCase(t *testing.T) {
	uploader := &stubUploader{err: errors.New("fileName is required")}
	resolver := &Resolver{DocumentUploader: uploader, DocumentService: &stubProvider{}, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	ctx := ctxutils.WithAPIKey(context.Background(), "secret")
	_, err := mr.UploadKnowledgeDocument(ctx, model.UploadKnowledgeDocumentInput{})
	if err == nil || err.Error() != "fileName is required" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestKnowledgeDocumentMapping(t *testing.T) {
	provider := &stubProvider{getFunc: func(ctx context.Context, id string) (*domain.Document, error) {
		status := domain.DocumentStatusPublishedToGithub
		job := "job-1"
		return &domain.Document{ID: id, FileName: "file.docx", Repository: "org/repo", Branch: "main", Path: "docs/file.md", Status: status, JobID: &job}, nil
	}, listFunc: func(ctx context.Context, filter domain.DocumentFilter, pagination domain.Pagination) (*domain.DocumentList, error) {
		st := domain.DocumentStatusFailed
		return &domain.DocumentList{Documents: []domain.Document{{ID: "id2", FileName: "f2.docx", Repository: "org/repo", Branch: "main", Path: "p", Status: st}}, TotalCount: 1}, nil
	}}

	resolver := &Resolver{DocumentUploader: &stubUploader{}, DocumentService: provider, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: zap.NewNop()}
	qr := &queryResolver{resolver}

	ctx := ctxutils.WithCorrelationID(context.Background(), "corr")
	doc, err := qr.KnowledgeDocument(ctx, "doc-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc.Status != model.KnowledgeDocumentStatusPublishedToGithub {
		t.Fatalf("unexpected status: %s", doc.Status)
	}

	list, err := qr.KnowledgeDocuments(ctx, nil, &model.Pagination{})
	if err != nil {
		t.Fatalf("unexpected list error: %v", err)
	}
	if len(list.Nodes) != 1 || list.TotalCount != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func strPtr(value string) *string {
	return &value
}
