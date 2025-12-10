package converter

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

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

			fileHeaders, ok := r.MultipartForm.File[doclingFileFieldName]
			if !ok || len(fileHeaders) != 1 {
				t.Fatalf("unexpected multipart fields: %+v", r.MultipartForm.File)
			}

			fileHeader := fileHeaders[0]
			contentDisposition := fileHeader.Header.Get("Content-Disposition")
			_, params, err := mime.ParseMediaType(contentDisposition)
			if err != nil {
				t.Fatalf("failed to parse content disposition: %v", err)
			}
			if params["name"] != doclingFileFieldName {
				t.Fatalf("unexpected multipart field name: %s", params["name"])
			}
			if fileHeader.Header.Get("Content-Type") != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
				t.Fatalf("unexpected content type: %s", fileHeader.Header.Get("Content-Type"))
			}

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
			_ = json.NewEncoder(w).Encode(taskStatusResponse{
				TaskID:     path.Base(r.URL.Path),
				TaskStatus: "success",
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(convertDocumentResponse{
				Status:   "success",
				Document: exportDocumentResponse{MdContent: ptr("# converted")},
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
			_ = json.NewEncoder(w).Encode(taskStatusResponse{
				TaskID:     "task-999",
				TaskStatus: "completed",
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(convertDocumentResponse{
				Status:   "completed",
				Document: exportDocumentResponse{MdContent: ptr("ok")},
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
			_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: "task-wait", TaskStatus: "processing"})
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

func TestDoclingConverterCheckStatusTaskStatusField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "task-async"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: "task-async", TaskStatus: "completed"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(convertDocumentResponse{
				Status:   "completed",
				Document: exportDocumentResponse{MdContent: ptr("# async")},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	jobID, err := conv.Submit(context.Background(), DoclingConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	status, err := conv.CheckStatus(context.Background(), jobID)
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Status != "completed" {
		t.Fatalf("expected normalized task status, got %s", status.Status)
	}

	if status.Result == nil || string(status.Result.Content) != "# async" {
		t.Fatalf("unexpected result: %+v", status.Result)
	}
}

func TestDoclingConverterCheckStatusMissingStatusDefaultsToPending(t *testing.T) {
	logCore, logs := observer.New(zap.DebugLevel)
	logger := zap.New(logCore)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "task-unknown"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: "task-unknown"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, logger)

	jobID, err := conv.Submit(context.Background(), DoclingConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	status, err := conv.CheckStatus(context.Background(), jobID)
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Status != "pending" {
		t.Fatalf("expected pending status fallback, got %s", status.Status)
	}

	warnings := logs.FilterMessage("docling status missing, defaulting to pending")
	if warnings.Len() == 0 {
		t.Fatalf("expected warning log for missing status")
	}
}

func TestDoclingConverterCheckStatusUnknownIncludesResponse(t *testing.T) {
	const responseBody = `{"task_status":"mystery","message":"unexpected"}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, doclingStatusPath) {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	_, err := conv.CheckStatus(context.Background(), "job-unknown")
	if err == nil {
		t.Fatal("expected error for unknown status")
	}

	if !strings.Contains(err.Error(), "response="+responseBody) {
		t.Fatalf("expected response body in error, got: %v", err)
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestDoclingConverterSubmitPDFWithOCR(t *testing.T) {
	var receivedQuery string
	var receivedLanguageField, receivedOCRField string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
			receivedQuery = r.URL.RawQuery

			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Fatalf("failed to parse submit form: %v", err)
			}

			receivedLanguageField = r.FormValue("language")
			receivedOCRField = r.FormValue("ocr")

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "pdf-task"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: "pdf-task", TaskStatus: "success"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(convertDocumentResponse{
				Status:   "success",
				Document: exportDocumentResponse{MdContent: ptr("# pdf")},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{
		Endpoint:           server.URL,
		PDFEnableOCR:       true,
		DefaultLanguage:    "ru",
		PDFDefaultLanguage: "sah",
	}, zap.NewNop())

	ctx, _ := telemetry.ContextWithCorrelationID(context.Background(), "pdf-corr")
	jobID, err := conv.Submit(ctx, DoclingConvertRequest{
		FileName:    "scan.pdf",
		Content:     []byte("pdf content"),
		ContentType: "application/pdf",
	})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	if jobID == "" {
		t.Fatalf("jobID must be returned")
	}

	status, err := conv.CheckStatus(ctx, jobID)
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Status != "success" {
		t.Fatalf("unexpected status: %s", status.Status)
	}

	query := statusFromRawQuery(receivedQuery)
	if query.Get("ocr") != "true" {
		t.Fatalf("expected ocr param in query, got %s", query.Get("ocr"))
	}

	if query.Get("language") != "sah" {
		t.Fatalf("expected pdf language override, got %s", query.Get("language"))
	}

	if receivedLanguageField != "sah" {
		t.Fatalf("expected language form field, got %s", receivedLanguageField)
	}

	if receivedOCRField != "true" {
		t.Fatalf("expected ocr form field, got %s", receivedOCRField)
	}
}

func statusFromRawQuery(rawQuery string) url.Values {
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return url.Values{}
	}

	return query
}
