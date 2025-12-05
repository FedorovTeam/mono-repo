package converter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDoclingConverterSuccess(t *testing.T) {
	var receivedCorrelation string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/convert" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		receivedCorrelation = r.Header.Get("X-Correlation-Id")
		if receivedCorrelation == "" {
			t.Fatalf("missing correlation id")
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# converted"))
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL)

	markdown, err := conv.Convert(context.Background(), ConvertRequest{
		FileName:    "sample.docx",
		Content:     []byte("docx-bytes"),
		ContentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if markdown != "# converted" {
		t.Fatalf("unexpected markdown: %s", markdown)
	}

	if receivedCorrelation == "" {
		t.Fatal("correlation id not propagated")
	}
}

func TestDoclingConverterErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "docling failed", http.StatusBadGateway)
	}))
	defer server.Close()

	conv := NewDoclingConverter(server.URL)
	_, err := conv.Convert(context.Background(), ConvertRequest{Content: []byte("docx")})
	if err == nil {
		t.Fatalf("expected error from Docling")
	}
}

func TestDoclingConverterEmptyContent(t *testing.T) {
	conv := NewDoclingConverter("http://docling")
	_, err := conv.Convert(context.Background(), ConvertRequest{Content: nil})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}
