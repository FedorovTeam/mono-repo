// backend/docling-transformer-service/internal/domain/conversion.go
package domain

import "context"

type ConvertRequest struct {
	FileName        string
	Content         []byte
	ContentType     string
	SourceBucket    string
	SourceObjectKey string
	TargetBucket    string
	TargetObjectKey string
	EnableOCR       *bool
	OCRLanguage     string
	PDFBackend      string
}

type ConvertResult struct {
	Content   []byte
	MediaType string
	Charset   string
}

type StatusCheckResult struct {
	Status string
	Result *ConvertResult
}

type DoclingIdentifiers struct {
	TaskID string
	JobID  string
}

func (d DoclingIdentifiers) PrimaryID() string {
	return d.TaskID
}

type Converter interface {
	Submit(ctx context.Context, req ConvertRequest) (DoclingIdentifiers, error)
	CheckStatus(ctx context.Context, taskID string) (StatusCheckResult, error)
}

type ObjectStorage interface {
	Download(ctx context.Context, bucket, objectKey string) ([]byte, error)
	Upload(ctx context.Context, bucket, objectKey, contentType string, content []byte) error
}
