package converter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestDoclingConverterSuccess(t *testing.T) {
	var receivedCorrelation, receivedEndpoint, receivedContentType string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("Docling expects POST, got %s", r.Method)
		}

		if r.URL.Path != "/convert" {
			t.Fatalf("unexpected Docling path: %s", r.URL.Path)
		}

		receivedCorrelation = r.Header.Get("X-Correlation-Id")
		receivedEndpoint = r.Header.Get("X-Docling-Endpoint")
		receivedContentType = r.Header.Get("Content-Type")

		if receivedCorrelation == "" {
			t.Fatalf("missing correlation id")
		}

		w.Header().Set("Content-Type", "text/markdown")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# converted by docling"))
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, zap.NewNop())

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

	if receivedContentType != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Fatalf("Docling content-type mismatch: %s", receivedContentType)
	}
}

func TestDoclingConverterErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "docling failed", http.StatusBadGateway)
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, zap.NewNop())
	_, err := conv.Convert(context.Background(), DoclingConvertRequest{Content: []byte("docx")})
	if err == nil {
		t.Fatalf("expected error from Docling")
	}

	if err.Error() != "docling api returned status 502: docling failed\n" {
		t.Fatalf("unexpected Docling error: %v", err)
	}
}

func TestDoclingConverterEmptyContent(t *testing.T) {
	conv := NewDoclingConverter("http://docling", zap.NewNop())
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
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL, zap.NewNop())
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
