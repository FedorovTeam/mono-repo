package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"backend-github-publisher-service/internal/config"
	githubpublisherv1 "backend-github-publisher-service/proto/backend/githubpublisher/v1"
	"github.com/dnaeon/go-vcr/cassette"
	"github.com/dnaeon/go-vcr/recorder"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type stubStorage struct {
	data   string
	bucket string
	key    string
	err    error
}

func (s *stubStorage) LoadMarkdown(ctx context.Context, bucket, key string) ([]byte, error) {
	s.bucket = bucket
	s.key = key
	if s.err != nil {
		return nil, s.err
	}
	return []byte(s.data), nil
}

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

	cassettePath := "testdata/cassettes/publish_success"
	mode := recorder.ModeReplaying
	if _, err := os.Stat(cassettePath + ".yaml"); os.IsNotExist(err) {
		mode = recorder.ModeRecording
	}

	rec, err := recorder.NewAsMode(cassettePath, mode, server.Client().Transport)
	if err != nil {
		t.Fatalf("recorder init: %v", err)
	}
	defer rec.Stop()
	rec.SetMatcher(func(r *http.Request, i cassette.Request) bool {
		recordedURL, err := url.Parse(i.URL)
		if err != nil {
			return false
		}

		return r.Method == i.Method && r.URL.Path == recordedURL.Path
	})

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
	storageStub := &stubStorage{data: "# Hi"}
	githubpublisherv1.RegisterGithubPublisherServiceServer(grpcServer, NewPublisherServer(cfg, &http.Client{Transport: rec}, storageStub))

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
		Bucket:        "md",
		ObjectKey:     "docs/a.md",
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
	if capturedCorrelation == "" {
		stored, err := cassette.Load(cassettePath)
		if err != nil {
			t.Fatalf("load cassette: %v", err)
		}
		if len(stored.Interactions) > 0 {
			capturedCorrelation = stored.Interactions[0].Request.Headers.Get("X-Correlation-ID")
		}
	}
	if capturedCorrelation != "corr-int" {
		t.Fatalf("correlation header not forwarded")
	}
	if storageStub.bucket != "md" || storageStub.key != "docs/a.md" {
		t.Fatalf("unexpected storage request: %s %s", storageStub.bucket, storageStub.key)
	}
}

func TestPublishMarkdownIntegration_GitHubError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": "fail"})
	}))
	defer server.Close()

	var logBuf bytes.Buffer
	originalWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(originalWriter) })

	cfg := &config.Config{
		GitHub: config.GitHubConfig{
			BaseURL:           server.URL,
			Owner:             "owner",
			Repository:        "repo",
			Token:             "token",
			DefaultBranch:     "main",
			CorrelationHeader: "X-Correlation-ID",
		},
	}

	lis := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	storageStub := &stubStorage{data: "# fail"}
	githubpublisherv1.RegisterGithubPublisherServiceServer(grpcServer, NewPublisherServer(cfg, server.Client(), storageStub))

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

	respCtx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "corr-error"))
	_, err = client.PublishMarkdown(respCtx, &githubpublisherv1.PublishMarkdownRequest{
		Path:          "docs/a.md",
		Bucket:        "md",
		ObjectKey:     "docs/a.md",
		CommitMessage: "Add doc",
	})
	if err == nil {
		t.Fatalf("expected error")
	}

	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("status not returned")
	}
	if st.Code() != codes.FailedPrecondition {
		t.Fatalf("unexpected code: %s", st.Code())
	}
	if !strings.Contains(logBuf.String(), "corr-error") {
		t.Fatalf("correlation id not logged")
	}
}
