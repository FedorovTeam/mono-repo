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
	"time"

	"go.uber.org/zap"
)

func TestDoclingConverterSubmitAndPollSuccess(t *testing.T) {
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
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{JobID: "job-123"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			atomic.AddInt32(&statusCalls, 1)
			receivedCorrelationStatus = r.Header.Get(correlationIDHeader)

			w.Header().Set("Content-Type", "application/json")
			if atomic.LoadInt32(&statusCalls) == 1 {
				_ = json.NewEncoder(w).Encode(doclingStatusResponse{Status: "processing"})
				return
			}

			_ = json.NewEncoder(w).Encode(doclingStatusResponse{
				JobID:  path.Base(r.URL.Path),
				Status: "success",
				Document: struct {
					Filename       string  `json:"filename"`
					MdContent      *string `json:"md_content"`
					HtmlContent    *string `json:"html_content"`
					TextContent    *string `json:"text_content"`
					DoctagsContent *string `json:"doctags_content"`
				}{MdContent: ptr("# converted")},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{
		Endpoint:           server.URL,
		StatusPollInterval: 10 * time.Millisecond,
		StatusPollTimeout:  500 * time.Millisecond,
	}, zap.NewNop())

	result, err := conv.Convert(context.Background(), DoclingConvertRequest{
		FileName:    "sample.docx",
		Content:     []byte("docx-bytes"),
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got := string(result.Content); got != "# converted" {
		t.Fatalf("unexpected markdown: %s", got)
	}

	if result.MediaType != "text/markdown" {
		t.Fatalf("unexpected media type: %s", result.MediaType)
	}

	if atomic.LoadInt32(&submitCalls) != 1 {
		t.Fatalf("expected single submit call, got %d", submitCalls)
	}

	if atomic.LoadInt32(&statusCalls) != 2 {
		t.Fatalf("expected two status polls, got %d", statusCalls)
	}

	if receivedFilename != "sample.docx" {
		t.Fatalf("unexpected filename: %s", receivedFilename)
	}

	if receivedBody != "docx-bytes" {
		t.Fatalf("unexpected body: %s", receivedBody)
	}

	if receivedCorrelationSubmit == "" || receivedCorrelationSubmit != receivedCorrelationStatus {
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
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{JobID: "job-999"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingStatusResponse{
				JobID:  "job-999",
				Status: "success",
				Document: struct {
					Filename       string  `json:"filename"`
					MdContent      *string `json:"md_content"`
					HtmlContent    *string `json:"html_content"`
					TextContent    *string `json:"text_content"`
					DoctagsContent *string `json:"doctags_content"`
				}{MdContent: ptr("ok")},
			})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{Endpoint: server.URL}, zap.NewNop())

	result, err := conv.Convert(context.Background(), DoclingConvertRequest{Content: []byte("doc")})
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}

	if got := string(result.Content); got != "ok" {
		t.Fatalf("unexpected content: %s", got)
	}

	if atomic.LoadInt32(&submitAttempts) != 2 {
		t.Fatalf("expected retry on submit, attempts=%d", submitAttempts)
	}
}

func TestDoclingConverterPollTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingSubmitResponse{JobID: "job-timeout"})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(doclingStatusResponse{Status: "processing"})
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	conv := NewDoclingConverter(Config{
		Endpoint:           server.URL,
		StatusPollInterval: 20 * time.Millisecond,
		StatusPollTimeout:  60 * time.Millisecond,
	}, zap.NewNop())

	_, err := conv.Convert(context.Background(), DoclingConvertRequest{Content: []byte("doc")})
	if err == nil {
		t.Fatalf("expected timeout error")
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func ptr[T any](v T) *T {
	return &v
}
