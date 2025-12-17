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
	ForceOCR        *bool
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

type Converter interface {
	Submit(ctx context.Context, req ConvertRequest) (string, error)
	CheckStatus(ctx context.Context, jobID string) (StatusCheckResult, error)
}

type ObjectStorage interface {
	Download(ctx context.Context, bucket, objectKey string) ([]byte, error)
	Upload(ctx context.Context, bucket, objectKey, contentType string, content []byte) error
}
