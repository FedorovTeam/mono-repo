package converter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"path"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/mono-repo/docling-transformer-service/internal/infrastructure/telemetry"
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

			formats := r.MultipartForm.Value["to_formats"]
			if !reflect.DeepEqual(formats, []string{"md"}) {
				t.Fatalf("unexpected to_formats: %+v", formats)
			}

			if fromFormats := r.MultipartForm.Value["from_formats"]; !reflect.DeepEqual(fromFormats, []string{"docx"}) {
				t.Fatalf("unexpected from_formats: %+v", fromFormats)
			}

			if _, exists := r.MultipartForm.Value["do_ocr"]; exists {
				t.Fatalf("did not expect do_ocr field in minimal contract")
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
				Document: exportDocumentResponse{MdContent: ptr("# converted"), Charset: ptr("utf-16")},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	ctx, _ := telemetry.ContextWithCorrelationID(context.Background(), "corr-submit")

	ids, err := conv.Submit(ctx, domain.ConvertRequest{
		FileName:    "sample.docx",
		Content:     []byte("docx-bytes"),
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	if ids.TaskID != "task-123" || ids.JobID != "" {
		t.Fatalf("unexpected ids: %+v", ids)
	}

	status, err := conv.CheckStatus(ctx, ids.TaskID)
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

	if status.Result.Charset != "utf-16" {
		t.Fatalf("unexpected charset: %s", status.Result.Charset)
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

func TestDoclingConverterSubmitSourceAsyncSuccess(t *testing.T) {
	var submitCalls, statusCalls int32
	var receivedRequest convertDocumentsRequest

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSourceSubmitPath:
			atomic.AddInt32(&submitCalls, 1)
			if r.Header.Get(apiKeyHeader) != "token" {
				t.Fatalf("unexpected api key: %s", r.Header.Get(apiKeyHeader))
			}
			if r.Header.Get(correlationIDHeader) == "" {
				t.Fatalf("expected correlation header")
			}

			if err := json.NewDecoder(r.Body).Decode(&receivedRequest); err != nil {
				t.Fatalf("failed to decode submit body: %v", err)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "s3-task"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			atomic.AddInt32(&statusCalls, 1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: path.Base(r.URL.Path), TaskStatus: "success"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(convertDocumentResponse{Status: "success", Document: exportDocumentResponse{MdContent: ptr("# ok")}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{
		Endpoint:        server.URL,
		APIKey:          "token",
		DefaultLanguage: "ru",
		Mode:            "source_async",
		Storage: StorageConfig{
			Endpoint:  "minio:9000",
			AccessKey: "acc",
			SecretKey: "sec",
			Bucket:    "bucket",
			Region:    "us-east-1",
			UseSSL:    true,
		},
	}, zap.NewNop())

	ctx, _ := telemetry.ContextWithCorrelationID(context.Background(), "corr-source")
	ids, err := conv.Submit(ctx, domain.ConvertRequest{
		FileName:        "source.pdf",
		ContentType:     "application/pdf",
		SourceBucket:    "src-bucket",
		SourceObjectKey: "path/to/file.pdf",
		TargetBucket:    "md-bucket",
		TargetObjectKey: "path/to/result.md",
	})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	if ids.TaskID != "s3-task" || ids.JobID != "" {
		t.Fatalf("unexpected ids: %+v", ids)
	}

	status, err := conv.CheckStatus(ctx, ids.TaskID)
	if err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}

	if status.Result == nil || string(status.Result.Content) != "# ok" {
		t.Fatalf("unexpected result: %+v", status.Result)
	}

	if submitCalls != 1 || statusCalls != 1 {
		t.Fatalf("unexpected call counts: submit=%d status=%d", submitCalls, statusCalls)
	}

	if len(receivedRequest.Sources) != 1 {
		t.Fatalf("expected single source, got %+v", receivedRequest.Sources)
	}

	source := receivedRequest.Sources[0]
	if source.Kind != "s3" || source.Bucket != "src-bucket" || source.KeyPrefix != "path/to/file.pdf" || !source.VerifySSL {
		t.Fatalf("unexpected source payload: %+v", source)
	}
	if source.Endpoint != "minio:9000" || source.AccessKey != "acc" || source.SecretKey != "sec" || source.Region != "us-east-1" {
		t.Fatalf("unexpected source credentials: %+v", source)
	}

	if receivedRequest.Target == nil || receivedRequest.Target.Bucket != "md-bucket" || receivedRequest.Target.KeyPrefix != "path/to/result.md" {
		t.Fatalf("unexpected target payload: %+v", receivedRequest.Target)
	}
	if receivedRequest.Target.Endpoint != "minio:9000" || receivedRequest.Target.AccessKey != "acc" || receivedRequest.Target.SecretKey != "sec" || receivedRequest.Target.Region != "us-east-1" || !receivedRequest.Target.VerifySSL {
		t.Fatalf("unexpected target credentials: %+v", receivedRequest.Target)
	}

	if !reflect.DeepEqual(receivedRequest.Options.ToFormats, []string{"md"}) {
		t.Fatalf("unexpected to_formats: %+v", receivedRequest.Options.ToFormats)
	}

	if !reflect.DeepEqual(receivedRequest.Options.FromFormats, []string{"pdf"}) {
		t.Fatalf("unexpected from_formats: %+v", receivedRequest.Options.FromFormats)
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

	ids, err := conv.Submit(ctx, domain.ConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error after retry: %v", err)
	}

	status, err := conv.CheckStatus(ctx, ids.TaskID)
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

	ids, err := conv.Submit(ctx, domain.ConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	status, err := conv.CheckStatus(ctx, ids.TaskID)
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

func TestCheckStatusMarks5xxAsTemporary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, "docling unavailable", http.StatusBadGateway)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	_, err := conv.CheckStatus(context.Background(), "job-5xx")
	if err == nil {
		t.Fatal("expected error from status check")
	}

	var tmp interface {
		Temporary() bool
		StatusCode() int
	}
	if !errors.As(err, &tmp) || !tmp.Temporary() {
		t.Fatalf("expected temporary error, got: %v", err)
	}

	if tmp.StatusCode() != http.StatusBadGateway {
		t.Fatalf("unexpected status code: %d", tmp.StatusCode())
	}
}

func TestCheckStatusMarks404AsTemporary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, "not ready", http.StatusNotFound)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	_, err := conv.CheckStatus(context.Background(), "job-404")
	if err == nil {
		t.Fatal("expected error from status check")
	}

	var tmp interface {
		Temporary() bool
		StatusCode() int
	}
	if !errors.As(err, &tmp) || !tmp.Temporary() {
		t.Fatalf("expected temporary error, got: %v", err)
	}

	if tmp.StatusCode() != http.StatusNotFound {
		t.Fatalf("unexpected status code: %d", tmp.StatusCode())
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

	ids, err := conv.Submit(context.Background(), domain.ConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	status, err := conv.CheckStatus(context.Background(), ids.TaskID)
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

	ids, err := conv.Submit(context.Background(), domain.ConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected submit error: %v", err)
	}

	status, err := conv.CheckStatus(context.Background(), ids.TaskID)
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
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(responseBody))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"errors":[{"module_name":"stub"}]}`))
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
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

func TestDoclingConverterSubmitSourceAsyncTemporaryError(t *testing.T) {
	var attempts int32
	var receivedOptions doclingOptions

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSourceSubmitPath:
			if atomic.AddInt32(&attempts, 1) == 1 {
				http.Error(w, "unavailable", http.StatusBadGateway)
				return
			}

			var reqBody convertDocumentsRequest
			if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
				t.Fatalf("failed to decode request: %v", err)
			}
			receivedOptions = reqBody.Options

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "retry-task"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: "retry-task", TaskStatus: "success"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(convertDocumentResponse{Status: "success", Document: exportDocumentResponse{MdContent: ptr("res")}})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{
		Endpoint: server.URL,
		Mode:     "source_async",
		Storage:  StorageConfig{Endpoint: "minio", AccessKey: "a", SecretKey: "b", Bucket: "bucket"},
	}, zap.NewNop())

	ids, err := conv.Submit(context.Background(), domain.ConvertRequest{
		FileName:        "file.docx",
		ContentType:     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		SourceBucket:    "bucket",
		SourceObjectKey: "file.docx",
	})
	if err != nil {
		t.Fatalf("unexpected submit error after retry: %v", err)
	}

	if attempts != 2 {
		t.Fatalf("expected retry, got %d attempts", attempts)
	}

	if !reflect.DeepEqual(receivedOptions.ToFormats, []string{"md"}) {
		t.Fatalf("unexpected to_formats: %+v", receivedOptions.ToFormats)
	}
	if !reflect.DeepEqual(receivedOptions.FromFormats, []string{"docx"}) {
		t.Fatalf("unexpected from_formats: %+v", receivedOptions.FromFormats)
	}

	if _, err := conv.CheckStatus(context.Background(), ids.TaskID); err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}
}
