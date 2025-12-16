package usecase

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"backend-gateway-service/internal/domain"
)

// UploadInput описывает входные данные загрузки документа.
type UploadInput struct {
	FileName      string
	Repository    string
	Branch        string
	Path          string
	FileSizeBytes int64
	EnablePdfOcr  bool
}

// UploadResult описывает результат сценария загрузки документа.
type UploadResult struct {
	Document     *domain.Document
	UploadURL    string
	UploadMethod string
	EnablePdfOcr bool
}

// UploadConfig содержит настройки сценария загрузки.
type UploadConfig struct {
	SignedURLTTLSeconds int64
}

// DocumentUploader отвечает за сценарий загрузки документа.
type DocumentUploader interface {
	UploadKnowledgeDocument(ctx context.Context, input UploadInput) (*UploadResult, error)
}

// UploadService реализует сценарий UploadKnowledgeDocument.
type UploadService struct {
	documents domain.DocumentClient
	storage   domain.StorageClient
	config    UploadConfig
}

// NewUploadService создаёт UploadService.
func NewUploadService(docClient domain.DocumentClient, storageClient domain.StorageClient, cfg UploadConfig) *UploadService {
	return &UploadService{documents: docClient, storage: storageClient, config: cfg}
}

// UploadKnowledgeDocument формирует ссылку загрузки и создаёт документ в document-service.
func (s *UploadService) UploadKnowledgeDocument(ctx context.Context, input UploadInput) (*UploadResult, error) {
	if err := validateUploadInput(input); err != nil {
		return nil, err
	}

	ctx, correlationID := ctxutils.EnsureCorrelationID(ctx)
	documentID := uuid.NewString()
	path := strings.TrimSpace(input.Path)
	if path == "" {
		path = defaultDocumentPath(documentID)
	}

	uploadURL, err := s.storage.GenerateUploadURL(ctx, domain.GenerateUploadURLRequest{
		DocumentID:       documentID,
		FileName:         input.FileName,
		ContentLength:    input.FileSizeBytes,
		CorrelationID:    correlationID,
		ExpiresInSeconds: s.config.SignedURLTTLSeconds,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot generate upload url: %w", err)
	}

	if err := validateUploadURL(uploadURL); err != nil {
		return nil, err
	}

	created, err := s.documents.CreateDocument(ctx, domain.CreateDocumentRequest{
		ID:                documentID,
		FileName:          input.FileName,
		Repository:        input.Repository,
		Branch:            input.Branch,
		Path:              path,
		CorrelationID:     correlationID,
		ContentLength:     input.FileSizeBytes,
		UploadBucket:      uploadURL.Bucket,
		UploadObjectKey:   uploadURL.ObjectKey,
		MarkdownBucket:    uploadURL.MarkdownBucket,
		MarkdownObjectKey: uploadURL.MarkdownObjectKey,
		EnablePdfOcr:      input.EnablePdfOcr,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot create document: %w", err)
	}

	return &UploadResult{
		Document:     created,
		UploadURL:    uploadURL.URL,
		UploadMethod: http.MethodPut,
		EnablePdfOcr: input.EnablePdfOcr,
	}, nil
}

func validateUploadInput(input UploadInput) error {
	if strings.TrimSpace(input.FileName) == "" {
		return errors.New("fileName is required")
	}

	if strings.TrimSpace(input.Repository) == "" {
		return errors.New("repository is required")
	}

	if strings.TrimSpace(input.Branch) == "" {
		return errors.New("branch is required")
	}
	if input.FileSizeBytes <= 0 {
		return errors.New("fileSizeBytes must be positive")
	}
	return nil
}

func defaultDocumentPath(id string) string {
	return fmt.Sprintf("knowledge/docs/%s.md", id)
}

func validateUploadURL(uploadURL *domain.UploadURL) error {
	if uploadURL == nil {
		return errors.New("cannot generate upload url: empty response")
	}

	missingFields := make([]string, 0, 4)
	if uploadURL.Bucket == "" {
		missingFields = append(missingFields, "bucket")
	}
	if uploadURL.ObjectKey == "" {
		missingFields = append(missingFields, "object_key")
	}
	if uploadURL.MarkdownBucket == "" {
		missingFields = append(missingFields, "markdown_bucket")
	}
	if uploadURL.MarkdownObjectKey == "" {
		missingFields = append(missingFields, "markdown_object_key")
	}

	if len(missingFields) > 0 {
		return fmt.Errorf("cannot generate upload url: storage service returned empty %s", strings.Join(missingFields, ", "))
	}

	return nil
}
