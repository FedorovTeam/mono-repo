package domain

import "context"

// CreateDocumentRequest описывает параметры создания документа.
type CreateDocumentRequest struct {
	ID                string
	FileName          string
	Repository        string
	RepositoryOwner   *string
	Branch            string
	Path              string
	CorrelationID     string
	ContentLength     int64
	UploadBucket      string
	UploadObjectKey   string
	MarkdownBucket    string
	MarkdownObjectKey string
	ForcePdfOcr       bool
}

// ConfirmUploadRequest описывает входные данные подтверждения загрузки.
type ConfirmUploadRequest struct {
	DocumentID    string
	Bucket        string
	ObjectKey     string
	CorrelationID string
}

// GenerateUploadURLRequest описывает параметры генерации ссылки загрузки.
type GenerateUploadURLRequest struct {
	DocumentID       string
	FileName         string
	ContentLength    int64
	CorrelationID    string
	ExpiresInSeconds int64
}

// UploadURL содержит данные для загрузки файла.
type UploadURL struct {
	URL               string
	Bucket            string
	ObjectKey         string
	MarkdownBucket    string
	MarkdownObjectKey string
}

// DocumentClient описывает порт для работы с сервисом документов.
type DocumentClient interface {
	CreateDocument(ctx context.Context, req CreateDocumentRequest) (*Document, error)
	GetDocument(ctx context.Context, id string) (*Document, error)
	ListDocuments(ctx context.Context, filter DocumentFilter, pagination Pagination) (*DocumentList, error)
	ConfirmUpload(ctx context.Context, req ConfirmUploadRequest) (*Document, error)
	Close() error
}

// StorageClient описывает порт для работы со storage-service.
type StorageClient interface {
	GenerateUploadURL(ctx context.Context, req GenerateUploadURLRequest) (*UploadURL, error)
	Close() error
}
