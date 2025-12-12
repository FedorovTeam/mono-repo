package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"backend-gateway-service/internal/domain"
	infrawebhook "backend-gateway-service/internal/infrastructure/webhook"
	"backend-gateway-service/internal/usecase"
)

type stubDocumentClient struct {
	lastRequest domain.ConfirmUploadRequest
	calls       int
	err         error
}

func (s *stubDocumentClient) ConfirmUpload(ctx context.Context, req domain.ConfirmUploadRequest) (*domain.Document, error) {
	s.lastRequest = req
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	status := domain.DocumentStatusUploaded
	return &domain.Document{ID: req.DocumentID, Status: status}, nil
}

func (s *stubDocumentClient) CreateDocument(context.Context, domain.CreateDocumentRequest) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}

func (s *stubDocumentClient) GetDocument(context.Context, string) (*domain.Document, error) {
	return nil, errors.New("not implemented")
}

func (s *stubDocumentClient) ListDocuments(context.Context, domain.DocumentFilter, domain.Pagination) (*domain.DocumentList, error) {
	return nil, errors.New("not implemented")
}

func (s *stubDocumentClient) Close() error { return nil }

func newTestServer(docClient *stubDocumentClient) *Server {
	parser := infrawebhook.NewMinIOWebhookParser()
	uploadConfirmation := usecase.NewUploadConfirmationService(parser, docClient)

	return &Server{logger: zap.NewNop(), uploadConfirmationUC: uploadConfirmation}
}

func TestHandleStorageNotification_ConfirmUpload(t *testing.T) {
	docClient := &stubDocumentClient{}
	srv := newTestServer(docClient)

	payload := map[string]any{
		"Records": []map[string]any{
			{
				"s3": map[string]any{
					"bucket": map[string]any{"name": "knowledge-original"},
					"object": map[string]any{
						"key":          "org/project/1234.docx",
						"userMetadata": map[string]string{"x-amz-meta-correlation-id": "corr-1"},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/internal/storage/uploaded", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleStorageNotification(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rr.Code)
	}
	if docClient.calls != 1 {
		t.Fatalf("expected 1 confirm call, got %d", docClient.calls)
	}
	if docClient.lastRequest.CorrelationID == "" {
		t.Fatal("expected correlation id to be propagated")
	}
	if docClient.lastRequest.DocumentID != "1234" {
		t.Fatalf("unexpected document id: %s", docClient.lastRequest.DocumentID)
	}
}

func TestHandleStorageNotification_ConfirmUpload_URLEncodedKey(t *testing.T) {
	docClient := &stubDocumentClient{}
	srv := newTestServer(docClient)

	encodedKey := "org/project/3fa85f64-5717-4562-b3fc-2c963f66afa6%2Bv1.docx"
	payload := map[string]any{
		"Records": []map[string]any{
			{
				"s3": map[string]any{
					"bucket": map[string]any{"name": "knowledge-original"},
					"object": map[string]any{
						"key":          encodedKey,
						"userMetadata": map[string]string{"x-amz-meta-correlation-id": "corr-2"},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/internal/storage/uploaded", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleStorageNotification(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("expected status %d, got %d", http.StatusAccepted, rr.Code)
	}
	if docClient.calls != 1 {
		t.Fatalf("expected 1 confirm call, got %d", docClient.calls)
	}
	if docClient.lastRequest.DocumentID != "3fa85f64-5717-4562-b3fc-2c963f66afa6+v1" {
		t.Fatalf("unexpected document id: %s", docClient.lastRequest.DocumentID)
	}
	if docClient.lastRequest.ObjectKey != "org/project/3fa85f64-5717-4562-b3fc-2c963f66afa6+v1.docx" {
		t.Fatalf("unexpected object key: %s", docClient.lastRequest.ObjectKey)
	}
}

func TestHandleStorageNotification_ReplayForRecovery(t *testing.T) {
	docClient := &stubDocumentClient{}
	srv := newTestServer(docClient)

	payload := map[string]any{
		"Records": []map[string]any{{
			"s3": map[string]any{
				"bucket": map[string]any{"name": "knowledge-original"},
				"object": map[string]any{
					"key":          "org/project/replay.docx",
					"userMetadata": map[string]string{"x-amz-meta-correlation-id": "corr-replay"},
				},
			},
		}},
	}

	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/internal/storage/uploaded", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleStorageNotification(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code)

	req = httptest.NewRequest(http.MethodPost, "/internal/storage/uploaded", bytes.NewReader(body))
	rr = httptest.NewRecorder()

	srv.handleStorageNotification(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code)
	require.Equal(t, 2, docClient.calls)
}

func TestHandleStorageNotification_NoRecords(t *testing.T) {
	srv := newTestServer(&stubDocumentClient{})
	payload := map[string]any{
		"Records": []map[string]any{{"s3": map[string]any{"object": map[string]any{"key": "/"}}}},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/internal/storage/uploaded", bytes.NewReader(body))
	rr := httptest.NewRecorder()

	srv.handleStorageNotification(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rr.Code)
	}
}
