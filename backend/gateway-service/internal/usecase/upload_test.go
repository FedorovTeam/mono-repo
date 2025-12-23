package usecase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"backend-gateway-service/internal/domain"
)

type stubDocumentClient struct {
	lastRequest domain.CreateDocumentRequest
}

func (s *stubDocumentClient) CreateDocument(_ context.Context, req domain.CreateDocumentRequest) (*domain.Document, error) {
	s.lastRequest = req
	status := domain.DocumentStatusUploaded
	return &domain.Document{
		ID:                 req.ID,
		FileName:           req.FileName,
		Repository:         req.Repository,
		Branch:             req.Branch,
		Path:               req.Path,
		Status:             status,
		ExtractTables:      req.ExtractTables,
		ConversionStrategy: domain.NormalizeConversionStrategy(req.ConversionStrategy),
	}, nil
}

func (s *stubDocumentClient) GetDocument(context.Context, string) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}

func (s *stubDocumentClient) ListDocuments(context.Context, domain.DocumentFilter, domain.Pagination) (*domain.DocumentList, error) {
	return nil, errors.New("not implemented")
}

func (s *stubDocumentClient) ConfirmUpload(context.Context, domain.ConfirmUploadRequest) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}

func (s *stubDocumentClient) Close() error { return nil }

type stubStorageClient struct{}

func (stubStorageClient) GenerateUploadURL(context.Context, domain.GenerateUploadURLRequest) (*domain.UploadURL, error) {
	return &domain.UploadURL{
		URL:               "http://storage/upload",
		Bucket:            "original-bucket",
		ObjectKey:         "object-key",
		MarkdownBucket:    "markdown-bucket",
		MarkdownObjectKey: "markdown-key",
		RequiredHeaders: map[string]string{
			"x-amz-meta-correlation-id": "corr-1",
		},
	}, nil
}

func (stubStorageClient) GenerateDownloadURL(context.Context, domain.GenerateDownloadURLRequest) (*domain.DownloadURL, error) {
	return nil, errors.New("not implemented")
}

func (stubStorageClient) Close() error { return nil }

func TestUploadKnowledgeDocument_DefaultPathGenerated(t *testing.T) {
	docClient := &stubDocumentClient{}
	storageClient := stubStorageClient{}
	svc := NewUploadService(docClient, storageClient, UploadConfig{SignedURLTTLSeconds: 60})

	result, err := svc.UploadKnowledgeDocument(context.Background(), UploadInput{
		FileName:      "report.docx",
		Repository:    "org/repo",
		Branch:        "main",
		Path:          "  ",
		FileSizeBytes: 256,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, docClient.lastRequest.Path)
	require.True(t, strings.HasPrefix(docClient.lastRequest.Path, "knowledge/docs/"))
	require.True(t, strings.HasSuffix(docClient.lastRequest.Path, ".md"))
	require.Equal(t, docClient.lastRequest.Path, result.Document.Path)
}

func TestUploadKnowledgeDocument_ForwardsTableOptions(t *testing.T) {
	docClient := &stubDocumentClient{}
	storageClient := stubStorageClient{}
	svc := NewUploadService(docClient, storageClient, UploadConfig{SignedURLTTLSeconds: 60})

	result, err := svc.UploadKnowledgeDocument(context.Background(), UploadInput{
		FileName:           "report.pdf",
		Repository:         "org/repo",
		Branch:             "main",
		Path:               "docs/report.md",
		FileSizeBytes:      256,
		ExtractTables:      true,
		ConversionStrategy: domain.ConversionStrategyTablesOnly,
	})

	require.NoError(t, err)
	require.True(t, docClient.lastRequest.ExtractTables)
	require.Equal(t, string(domain.ConversionStrategyTablesOnly), docClient.lastRequest.ConversionStrategy)
	require.True(t, result.Document.ExtractTables)
	require.Equal(t, domain.ConversionStrategyTablesOnly, result.Document.ConversionStrategy)
}

func TestUploadKnowledgeDocument_FailsWhenRequiredHeaderMissing(t *testing.T) {
	docClient := &stubDocumentClient{}
	storageClient := stubStorageClient{}
	svc := NewUploadService(docClient, storageClient, UploadConfig{SignedURLTTLSeconds: 60})

	_, err := svc.UploadKnowledgeDocument(context.Background(), UploadInput{
		FileName:      "report.pdf",
		Repository:    "org/repo",
		Branch:        "main",
		Path:          "docs/report.md",
		FileSizeBytes: 256,
	})
	require.NoError(t, err)

	svcMissingHeaders := NewUploadService(docClient, storageClientWithoutHeaders{}, UploadConfig{SignedURLTTLSeconds: 60})
	_, err = svcMissingHeaders.UploadKnowledgeDocument(context.Background(), UploadInput{
		FileName:      "report.pdf",
		Repository:    "org/repo",
		Branch:        "main",
		Path:          "docs/report.md",
		FileSizeBytes: 256,
	})
	require.EqualError(t, err, "cannot generate upload url: x-amz-meta-correlation-id header is required")
}

type storageClientWithoutHeaders struct{}

func (storageClientWithoutHeaders) GenerateUploadURL(context.Context, domain.GenerateUploadURLRequest) (*domain.UploadURL, error) {
	return &domain.UploadURL{
		URL:               "http://storage/upload",
		Bucket:            "original-bucket",
		ObjectKey:         "object-key",
		MarkdownBucket:    "markdown-bucket",
		MarkdownObjectKey: "markdown-key",
	}, nil
}

func (storageClientWithoutHeaders) GenerateDownloadURL(context.Context, domain.GenerateDownloadURLRequest) (*domain.DownloadURL, error) {
	return nil, errors.New("not implemented")
}

func (storageClientWithoutHeaders) Close() error { return nil }
