package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"backend-gateway-service/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestApplicationRunAndStop(t *testing.T) {
	configPath := writeTestConfig(t)

	application, err := New(
		configPath,
		WithDocumentClient(&stubDocumentClient{}),
		WithStorageClient(&stubStorageClient{}),
		WithLogger(zaptest.NewLogger(t)),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		assert.NoError(t, application.Run(ctx))
		close(done)
	}()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	require.NoError(t, application.Stop(stopCtx))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("application did not stop in time")
	}
}

func writeTestConfig(t *testing.T) string {
	t.Helper()

	data := []byte(`server:
  port: 0
security:
  apiKey: test-key
documentService:
  address: 127.0.0.1:50051
  grpc:
    dialTimeout: 1s
    minConnectTimeout: 1s
storageService:
  address: 127.0.0.1:50052
  grpc:
    dialTimeout: 1s
    minConnectTimeout: 1s
uploads:
  signedUrlTtlSeconds: 60
observability:
  enabled: false
logging:
  level: debug
`)

	configPath := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(configPath, data, 0o644))

	return configPath
}

type stubDocumentClient struct{}

func (stubDocumentClient) CreateDocument(_ context.Context, req domain.CreateDocumentRequest) (*domain.Document, error) {
	return &domain.Document{ID: req.ID, FileName: req.FileName}, nil
}

func (stubDocumentClient) GetDocument(_ context.Context, id string) (*domain.Document, error) {
	return &domain.Document{ID: id}, nil
}

func (stubDocumentClient) ListDocuments(context.Context, domain.DocumentFilter, domain.Pagination) (*domain.DocumentList, error) {
	return &domain.DocumentList{}, nil
}

func (stubDocumentClient) ConfirmUpload(_ context.Context, req domain.ConfirmUploadRequest) (*domain.Document, error) {
	return &domain.Document{ID: req.DocumentID}, nil
}

func (stubDocumentClient) Close() error { return nil }

type stubStorageClient struct{}

func (stubStorageClient) GenerateUploadURL(_ context.Context, req domain.GenerateUploadURLRequest) (*domain.UploadURL, error) {
	return &domain.UploadURL{URL: req.DocumentID}, nil
}

func (stubStorageClient) Close() error { return nil }
