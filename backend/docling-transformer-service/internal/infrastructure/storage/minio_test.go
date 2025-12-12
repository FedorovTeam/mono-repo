package storage

import (
	"context"
	"fmt"
	"io"
	"testing"

	"github.com/minio/minio-go/v7"
)

type putObjectRecorder struct {
	lastBucket      string
	lastObjectKey   string
	lastSize        int64
	lastOptions     minio.PutObjectOptions
	shouldReturnErr error
}

func (r *putObjectRecorder) GetObject(ctx context.Context, bucketName, objectName string, opts minio.GetObjectOptions) (*minio.Object, error) {
	return nil, fmt.Errorf("get object not implemented for %s/%s", bucketName, objectName)
}

func (r *putObjectRecorder) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) (minio.UploadInfo, error) {
	_, _ = io.ReadAll(reader)

	r.lastBucket = bucketName
	r.lastObjectKey = objectName
	r.lastSize = objectSize
	r.lastOptions = opts

	return minio.UploadInfo{}, r.shouldReturnErr
}

func TestUploadPassesContentType(t *testing.T) {
	recorder := &putObjectRecorder{}
	storage := &MinioStorage{client: recorder}

	const (
		bucket      = "bucket"
		objectKey   = "object"
		contentType = "text/plain; charset=utf-8"
	)

	content := []byte("payload")

	if err := storage.Upload(context.Background(), bucket, objectKey, contentType, content); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if recorder.lastBucket != bucket || recorder.lastObjectKey != objectKey {
		t.Fatalf("unexpected destination: %s/%s", recorder.lastBucket, recorder.lastObjectKey)
	}

	if recorder.lastSize != int64(len(content)) {
		t.Fatalf("unexpected content size: %d", recorder.lastSize)
	}

	if recorder.lastOptions.ContentType != contentType {
		t.Fatalf("expected content type %q, got %q", contentType, recorder.lastOptions.ContentType)
	}
}
