package converter

import (
	"context"
	"encoding/json"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
	"github.com/mono-repo/docling-transformer-service/internal/testutil"
	"go.uber.org/zap"
)

func TestDoclingConverterOCROptionsAndSeparateOutputs(t *testing.T) {
	outputDir := t.TempDir()
	restoreFlag := setDocOutputDirFlag(t, outputDir)
	defer restoreFlag()

	savedPaths := make(map[string]string)

	t.Run("text layer pdf skips ocr", func(t *testing.T) {
		var receivedEngines, receivedLangs []string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatalf("failed to parse submit form: %v", err)
				}

				receivedEngines = r.MultipartForm.Value["ocr_engine"]
				receivedLangs = r.MultipartForm.Value["ocr_lang"]

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "task-text"})
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: "task-text", TaskStatus: "success"})
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(convertDocumentResponse{Status: "success", Document: exportDocumentResponse{MdContent: ptr("# text pdf")}})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		conv := NewDoclingConverter(Config{
			Endpoint:         server.URL,
			SkipOCROnTextPDF: ptr(true),
			OCREngine:        "easyocr",
			OCRLang:          []string{"ru"},
		}, zap.NewNop())

		ids, err := conv.Submit(context.Background(), domain.ConvertRequest{
			FileName:    "text.pdf",
			Content:     pdfWithTextLayer(),
			ContentType: "application/pdf",
		})
		if err != nil {
			t.Fatalf("unexpected submit error: %v", err)
		}

		status, err := conv.CheckStatus(context.Background(), ids.TaskID)
		if err != nil {
			t.Fatalf("unexpected status error: %v", err)
		}

		if status.Result == nil {
			t.Fatalf("expected non-nil result")
		}

		savedPaths["text"] = testutil.SaveResult(t, *status.Result, filepath.Join("docling", "text-"+ids.TaskID))

		if len(receivedEngines) != 0 || len(receivedLangs) != 0 {
			t.Fatalf("expected no ocr options for text layer pdf, engines=%v langs=%v", receivedEngines, receivedLangs)
		}

		content, err := os.ReadFile(savedPaths["text"])
		if err != nil {
			t.Fatalf("failed to read saved result: %v", err)
		}
		if string(content) != "# text pdf" {
			t.Fatalf("unexpected saved content: %s", string(content))
		}
	})

	t.Run("image pdf uses ocr languages", func(t *testing.T) {
		var receivedEngines, receivedLangs []string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodPost && r.URL.Path == doclingSubmitPath:
				if err := r.ParseMultipartForm(1 << 20); err != nil {
					t.Fatalf("failed to parse submit form: %v", err)
				}

				receivedEngines = r.MultipartForm.Value["ocr_engine"]
				receivedLangs = r.MultipartForm.Value["ocr_lang"]

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusAccepted)
				_ = json.NewEncoder(w).Encode(doclingSubmitResponse{TaskID: "task-image"})
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingStatusPath):
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(taskStatusResponse{TaskID: "task-image", TaskStatus: "success"})
			case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, doclingResultPath):
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(convertDocumentResponse{Status: "success", Document: exportDocumentResponse{MdContent: ptr("# image pdf")}})
			default:
				t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
			}
		}))
		defer server.Close()

		conv := NewDoclingConverter(Config{
			Endpoint:         server.URL,
			SkipOCROnTextPDF: ptr(false),
			OCREngine:        "rapidocr",
			OCRLang:          []string{"en", "de"},
		}, zap.NewNop())

		ids, err := conv.Submit(context.Background(), domain.ConvertRequest{
			FileName:    "image.pdf",
			Content:     imageOnlyPDF(),
			ContentType: "application/pdf",
		})
		if err != nil {
			t.Fatalf("unexpected submit error: %v", err)
		}

		status, err := conv.CheckStatus(context.Background(), ids.TaskID)
		if err != nil {
			t.Fatalf("unexpected status error: %v", err)
		}

		if status.Result == nil {
			t.Fatalf("expected non-nil result")
		}

		savedPaths["image"] = testutil.SaveResult(t, *status.Result, filepath.Join("docling", "image-"+ids.TaskID))

		if !reflect.DeepEqual(receivedEngines, []string{"rapidocr"}) {
			t.Fatalf("unexpected ocr_engine: %v", receivedEngines)
		}

		if !reflect.DeepEqual(receivedLangs, []string{"en", "de"}) {
			t.Fatalf("unexpected ocr_lang: %v", receivedLangs)
		}

		content, err := os.ReadFile(savedPaths["image"])
		if err != nil {
			t.Fatalf("failed to read saved result: %v", err)
		}
		if string(content) != "# image pdf" {
			t.Fatalf("unexpected saved content: %s", string(content))
		}
	})

	textPath := savedPaths["text"]
	imagePath := savedPaths["image"]

	if textPath == "" || imagePath == "" {
		t.Fatalf("expected both results to be saved: text=%s image=%s", textPath, imagePath)
	}

	if textPath == imagePath {
		t.Fatalf("expected different output files for different pdf kinds")
	}
}

func setDocOutputDirFlag(t *testing.T, dir string) func() {
	t.Helper()

	flagObj := flag.Lookup("doc_output_dir")
	if flagObj == nil {
		t.Fatalf("doc_output_dir flag is not registered")
	}

	previous := flagObj.Value.String()
	if err := flag.Set("doc_output_dir", dir); err != nil {
		t.Fatalf("failed to set doc_output_dir: %v", err)
	}

	return func() {
		_ = flag.Set("doc_output_dir", previous)
	}
}
