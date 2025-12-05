package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCommitMarkdownSuccess(t *testing.T) {
	var capturedCorrelation string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer token" {
			t.Fatalf("unexpected auth header: %s", got)
		}
		capturedCorrelation = r.Header.Get("X-Correlation-ID")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": "abc123"}})
	}))
	defer server.Close()

	client := New(server.Client(), server.URL, "token", "owner", "repo", "main", "X-Correlation-ID", "Bot", "bot@example.org")

	sha, err := client.CommitMarkdown(context.Background(), "docs/readme.md", "content", "message", "feature", "corr-1", "", "")
	if err != nil {
		t.Fatalf("commit error: %v", err)
	}
	if sha != "abc123" {
		t.Fatalf("unexpected sha: %s", sha)
	}
	if capturedCorrelation != "corr-1" {
		t.Fatalf("correlation header missing")
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
