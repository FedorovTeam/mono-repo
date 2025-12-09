package converter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDoclingConverterSuccess(t *testing.T) {
	var receivedCorrelation, receivedEndpoint, receivedContentType string
	var receivedFileName string
	var receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("Docling expects POST, got %s", r.Method)
		}

		if r.URL.Path != "/v1/convert/file" {
			t.Fatalf("unexpected Docling path: %s", r.URL.Path)
		}

		receivedCorrelation = r.Header.Get("X-Correlation-Id")
		receivedEndpoint = r.Header.Get("X-Docling-Endpoint")
		receivedContentType = r.Header.Get("Content-Type")

		if receivedCorrelation == "" {
			t.Fatalf("missing correlation id")
		}

		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("failed to parse multipart form: %v", err)
		}

		file, header, err := r.FormFile("files")
		if err != nil {
			t.Fatalf("missing files form field: %v", err)
		}
		defer file.Close()

		receivedFileName = header.Filename
		receivedBodyBytes, _ := io.ReadAll(file)
		receivedBody = string(receivedBodyBytes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"document": map[string]any{
				"filename":   receivedFileName,
				"md_content": "# converted by docling",
			},
			"status":          "success",
			"processing_time": 0.1,
		})
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, "", zap.NewNop())

	result, err := conv.Convert(context.Background(), DoclingConvertRequest{
		FileName:    "sample.docx",
		Content:     []byte("docx-bytes"),
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result.Content) != "# converted by docling" {
		t.Fatalf("unexpected markdown: %s", string(result.Content))
	}

	if result.MediaType != "text/markdown" {
		t.Fatalf("unexpected media type: %s", result.MediaType)
	}

	if receivedCorrelation == "" {
		t.Fatal("Docling correlation id not propagated")
	}

	if receivedEndpoint != server.URL {
		t.Fatalf("Docling endpoint header is missing: %s", receivedEndpoint)
	}

	if !strings.HasPrefix(receivedContentType, "multipart/form-data") {
		t.Fatalf("Docling content-type mismatch: %s", receivedContentType)
	}

	if receivedFileName != "sample.docx" {
		t.Fatalf("Docling file name mismatch: %s", receivedFileName)
	}

	if receivedBody != "docx-bytes" {
		t.Fatalf("Docling body mismatch: %s", receivedBody)
	}
}

func TestDoclingConverterPDF(t *testing.T) {
	var receivedFileName, receivedPartContentType, receivedSourceHeader, receivedBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatalf("failed to parse multipart form: %v", err)
		}

		fileHeader := r.MultipartForm.File["files"][0]
		receivedFileName = fileHeader.Filename
		receivedPartContentType = fileHeader.Header.Get("Content-Type")
		receivedSourceHeader = r.Header.Get("X-Docling-Source-Filename")

		file, err := fileHeader.Open()
		if err != nil {
			t.Fatalf("failed to open file: %v", err)
		}
		defer file.Close()

		bodyBytes, _ := io.ReadAll(file)
		receivedBody = string(bodyBytes)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"document": map[string]any{
				"filename":   receivedFileName,
				"md_content": "# pdf markdown",
			},
			"status":          "success",
			"processing_time": 0.2,
		})
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, "", zap.NewNop())
	result, err := conv.Convert(context.Background(), DoclingConvertRequest{
		Content:     []byte("pdf-bytes"),
		ContentType: "application/pdf",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.MediaType != "text/markdown" {
		t.Fatalf("unexpected media type: %s", result.MediaType)
	}

	if string(result.Content) != "# pdf markdown" {
		t.Fatalf("unexpected markdown: %s", string(result.Content))
	}

	if receivedPartContentType != "application/pdf" {
		t.Fatalf("unexpected part content-type: %s", receivedPartContentType)
	}

	if receivedFileName != "document.pdf" {
		t.Fatalf("unexpected file name: %s", receivedFileName)
	}

	if receivedSourceHeader != "document.pdf" {
		t.Fatalf("unexpected source header: %s", receivedSourceHeader)
	}

	if receivedBody != "pdf-bytes" {
		t.Fatalf("unexpected body: %s", receivedBody)
	}
}

func TestDoclingConverterErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "docling failed", http.StatusBadGateway)
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, "", zap.NewNop())
	_, err := conv.Convert(context.Background(), DoclingConvertRequest{Content: []byte("docx")})
	if err == nil {
		t.Fatalf("expected error from Docling")
	}

	if err.Error() != "docling api returned status 502: docling failed\n" {
		t.Fatalf("unexpected Docling error: %v", err)
	}
}

func TestDoclingConverterEmptyContent(t *testing.T) {
	conv := NewDoclingConverter("http://docling", "", zap.NewNop())
	_, err := conv.Convert(context.Background(), DoclingConvertRequest{Content: nil})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestDoclingConverterRetryOnFailure(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			http.Error(w, "temporary", http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"document":{"filename":"file.docx","md_content":"ok"},"status":"success","processing_time":0.01}`)
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, "", zap.NewNop())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, err := conv.Convert(ctx, DoclingConvertRequest{Content: []byte("docx")})
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}

	if string(result.Content) != "ok" {
		t.Fatalf("unexpected content: %s", string(result.Content))
	}

	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestDoclingConverterApiKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "secret" {
			t.Fatalf("expected api key header, got %s", r.Header.Get("X-Api-Key"))
		}

		_ = r.ParseMultipartForm(1 << 20)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"document":{"filename":"file.docx","md_content":"ok"},"status":"success","processing_time":0.01}`)
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, "secret", zap.NewNop())
	if _, err := conv.Convert(context.Background(), DoclingConvertRequest{Content: []byte("doc")}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
