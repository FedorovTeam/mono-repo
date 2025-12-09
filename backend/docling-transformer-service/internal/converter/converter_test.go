package converter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"

	"github.com/mono-repo/docling-transformer-service/internal/telemetry"
)

func TestDoclingConverterSubmitAndCheckStatusSuccess(t *testing.T) {
	var submitCalls, statusCalls int32
	var receivedCorrelationSubmit, receivedCorrelationStatus string
	var receivedFilename, receivedBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
			atomic.AddInt32(&submitCalls, 1)

			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("failed to parse submit form: %v", err)
			}

			receivedCorrelationSubmit = r.Header.Get(correlationIDHeader)
			if receivedCorrelationSubmit == "" {
				t.Fatal("submit correlation id is empty")
			}

			fileHeader := r.MultipartForm.File["files"][0]
			receivedFilename = fileHeader.Filename

			file, err := fileHeader.Open()
			if err != nil {
				t.Fatalf("failed to open file: %v", err)
			}
			defer file.Close()

			bodyBytes, _ := io.ReadAll(file)
			receivedBody = string(bodyBytes)

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "task-123"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			atomic.AddInt32(&statusCalls, 1)
			receivedCorrelationStatus = r.Header.Get(correlationIDHeader)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingStatusResponse{
				TaskID:  path.Base(r.URL.Path),
				Status:  "SUCCESS",
				Content: doclingStatusContent{Markdown: ptr("# converted")},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	ctx, _ := telemetry.ContextWithCorrelationID(context.Background(), "corr-submit")

	jobID, err := conv.Submit(ctx, DoclingConvertRequest{
		FileName:    "sample.docx",
		Content:     []byte("docx-bytes"),
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	status, err := conv.CheckStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Result == nil {
		t.Fatalf("expected result to be present")
	}

	if got := string(status.Result.Content); got != "# converted" {
		t.Fatalf("unexpected markdown: %s", got)
	}

	if status.Result.MediaType != "text/markdown" {
		t.Fatalf("unexpected media type: %s", status.Result.MediaType)
	}

	if atomic.LoadInt32(&submitCalls) != 1 {
		t.Fatalf("expected single submit call, got %d", submitCalls)
	}

	if atomic.LoadInt32(&statusCalls) != 1 {
		t.Fatalf("expected single status call, got %d", statusCalls)
	}

	if receivedFilename != "sample.docx" {
		t.Fatalf("unexpected filename: %s", receivedFilename)
	}

	if receivedBody != "docx-bytes" {
		t.Fatalf("unexpected body: %s", receivedBody)
	}

	if receivedCorrelationSubmit == "" || receivedCorrelationSubmit != receivedCorrelationStatus || receivedCorrelationSubmit != "corr-submit" {
		t.Fatalf("correlation id was not propagated across requests")
	}
}

func TestDoclingConverterRetryOnSubmitFailure(t *testing.T) {
	var submitAttempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
			attempt := atomic.AddInt32(&submitAttempts, 1)
			if attempt == 1 {
				http.Error(w, "temporary", http.StatusBadGateway)
				return
			}

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "task-999"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingStatusResponse{
				TaskID:  "task-999",
				Status:  "completed",
				Content: doclingStatusContent{Markdown: ptr("ok")},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	ctx, _ := telemetry.ContextWithCorrelationID(context.Background(), "retry-corr")

	jobID, err := conv.Submit(ctx, DoclingConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error after retry: %v", err)
	}

	status, err := conv.CheckStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Result == nil || string(status.Result.Content) != "ok" {
		t.Fatalf("unexpected content: %+v", status.Result)
	}

	if atomic.LoadInt32(&submitAttempts) != 2 {
		t.Fatalf("expected retry on submit, attempts=%d", submitAttempts)
	}
}

func TestDoclingConverterCheckStatusPending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "task-wait"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingStatusResponse{TaskID: "task-wait", Status: "processing"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	ctx, _ := telemetry.ContextWithCorrelationID(context.Background(), "pending-corr")

	jobID, err := conv.Submit(ctx, DoclingConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	status, err := conv.CheckStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Result != nil {
		t.Fatalf("expected nil result for processing status")
	}

	if status.Status == "" {
		t.Fatalf("status should be propagated")
	}
}

func ptr[T any](v T) *T {
	return &v
}
