package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommitMarkdownSuccess(t *testing.T) {
	var capturedCorrelation string
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			requestCount++
			w.WriteHeader(http.StatusNotFound)
		case http.MethodPut:
			requestCount++
			if got := r.Header.Get("Authorization"); got != "Bearer token" {
				t.Fatalf("unexpected auth header: %s", got)
			}
			if got := r.Header.Get("If-Match"); got != "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			capturedCorrelation = r.Header.Get("X-Correlation-ID")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": "abc123"}})
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	client := New(server.Client(), server.URL, "token", "owner", "repo", "main", "X-Correlation-ID", "Bot", "bot@example.org")

	sha, err := client.CommitMarkdown(context.Background(), "docs/readme.md", "content", "message", "feature", "corr-1", "", "")
	if err != nil {
		t.Fatalf("commit error: %v", err)
	}
	if requestCount != 2 {
		t.Fatalf("expected get and put, got %d requests", requestCount)
	}
	if sha != "abc123" {
		t.Fatalf("unexpected sha: %s", sha)
	}
	if capturedCorrelation != "corr-1" {
		t.Fatalf("correlation header missing")
	}
}

func TestCommitMarkdownSkipsWhenContentSame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":      "existing-sha",
				"content":  base64.StdEncoding.EncodeToString([]byte("content")),
				"encoding": "base64",
			})
		case http.MethodPut:
			t.Fatalf("unexpected put request")
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	client := New(server.Client(), server.URL, "token", "owner", "repo", "main", "X-Correlation-ID", "Bot", "bot@example.org")

	sha, err := client.CommitMarkdown(context.Background(), "docs/readme.md", "content", "message", "main", "", "", "")
	if err != nil {
		t.Fatalf("commit error: %v", err)
	}
	if sha != "existing-sha" {
		t.Fatalf("unexpected sha: %s", sha)
	}
}

func TestCommitMarkdownUpdatesExistingFile(t *testing.T) {
	var receivedShaField string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sha":      "existing-sha",
				"content":  base64.StdEncoding.EncodeToString([]byte("old content")),
				"encoding": "base64",
			})
		case http.MethodPut:
			if match := r.Header.Get("If-Match"); match != "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if sha, ok := body["sha"].(string); ok {
				receivedShaField = sha
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": "new-commit-sha"}})
		default:
			t.Fatalf("unexpected method: %s", r.Method)
		}
	}))
	defer server.Close()

	client := New(server.Client(), server.URL, "token", "owner", "repo", "main", "X-Correlation-ID", "Bot", "bot@example.org")

	sha, err := client.CommitMarkdown(context.Background(), "docs/readme.md", "new content", "message", "main", "corr-2", "", "")
	if err != nil {
		t.Fatalf("commit error: %v", err)
	}
	if sha != "new-commit-sha" {
		t.Fatalf("unexpected sha: %s", sha)
	}
	if receivedShaField != "existing-sha" {
		t.Fatalf("sha field missing in request: %s", receivedShaField)
	}
}

func TestCreatePullRequestSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		w.WriteHeader(http.StatusCreated)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "html_url": "https://example.com/pr/42"})
	}))
	defer server.Close()

	client := New(server.Client(), server.URL, "token", "owner", "repo", "main", "X-Correlation-ID", "Bot", "bot@example.org")

	number, url, err := client.CreatePullRequest(context.Background(), "feature", "main", "title", "body", "corr-2")
	if err != nil {
		t.Fatalf("pr error: %v", err)
	}
	if number != 42 || url != "https://example.com/pr/42" {
		t.Fatalf("unexpected pr response")
	}
}

func TestCommitMarkdownUnexpectedStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	client := New(server.Client(), server.URL, "token", "owner", "repo", "main", "X-Correlation-ID", "Bot", "bot@example.org")

	if _, err := client.CommitMarkdown(context.Background(), "a.md", "content", "msg", "", "", "", ""); err == nil {
		t.Fatalf("expected error")
	}
}
