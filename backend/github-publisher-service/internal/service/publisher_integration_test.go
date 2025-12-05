package service

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"backend-github-publisher-service/internal/config"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestPublishMarkdownIntegration(t *testing.T) {
	var capturedCorrelation string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlation := r.Header.Get("X-Correlation-ID")
		if correlation != "" {
			capturedCorrelation = correlation
		}

		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/repos/owner/repo/contents/docs/a.md":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]string{"sha": "abc123"}})
		case r.Method == http.MethodPost && r.URL.Path == "/repos/owner/repo/pulls":
			var payload map[string]string
			_ = json.NewDecoder(r.Body).Decode(&payload)
			if payload["head"] != "feature/doc" {
				t.Fatalf("unexpected head branch: %s", payload["head"])
			}
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"number": 7, "html_url": "https://example.com/pr/7"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := &config.Config{
		GitHub: config.GitHubConfig{
			BaseURL:           server.URL,
			Owner:             "owner",
			Repository:        "repo",
			Token:             "token",
			DefaultBranch:     "main",
			CorrelationHeader: "X-Correlation-ID",
			AuthorName:        "Bot",
			AuthorEmail:       "bot@example.org",
		},
	}

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	githubpublisherv1.RegisterGithubPublisherServiceServer(grpcServer, NewPublisherServer(cfg, server.Client()))

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(grpcServer.Stop)

	dialer := func(context.Context, string) (net.Conn, error) {
		return lis.Dial()
	}

	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	client := githubpublisherv1.NewGithubPublisherServiceClient(conn)

	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "corr-int"))
	resp, err := client.PublishMarkdown(ctx, &githubpublisherv1.PublishMarkdownRequest{
		Path:          "docs/a.md",
		Content:       "# Hi",
		CommitMessage: "Add doc",
		Branch:        "feature/doc",
		BaseBranch:    "main",
		CreatePr:      true,
		PrTitle:       "Publish doc",
		PrBody:        "Automated",
	})
	if err != nil {
		t.Fatalf("PublishMarkdown error: %v", err)
	}

	if resp.GetCommitSha() != "abc123" {
		t.Fatalf("unexpected commit sha: %s", resp.GetCommitSha())
	}
	if resp.GetPullRequestNumber() != 7 {
		t.Fatalf("unexpected pr number: %d", resp.GetPullRequestNumber())
	}
	if capturedCorrelation != "corr-int" {
		t.Fatalf("correlation header not forwarded")
	}
}
