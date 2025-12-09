package grpcdial

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"backend-document-service/internal/config"

	"google.golang.org/grpc"
)

func TestDialContext_UsesBlockAndTimeout(t *testing.T) {
	cfg := config.GRPCClient{DialTimeout: 100 * time.Millisecond}
	cfg.Backoff = config.Backoff{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	cfg.Keepalive = config.Keepalive{Time: time.Second, Timeout: time.Second}

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}

	start := time.Now()
	_, err := DialContext(context.Background(), "bufnet", cfg, WithDialOptions(grpc.WithContextDialer(dialer)))
	duration := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if duration < cfg.DialTimeout {
		t.Fatalf("expected dial to respect timeout, took %v", duration)
	}
}

func TestDialContext_WithoutBlockDoesNotWrapContext(t *testing.T) {
	cfg := config.GRPCClient{DialTimeout: 10 * time.Millisecond}
	cfg.Backoff = config.Backoff{BaseDelay: time.Millisecond, MaxDelay: time.Millisecond}
	cfg.Keepalive = config.Keepalive{Time: time.Second, Timeout: time.Second}

	started := make(chan struct{}, 1)
	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		started <- struct{}{}
		return nil, errors.New("failed")
	}

	start := time.Now()
	conn, err := DialContext(context.Background(), "bufnet", cfg, WithDialOptions(grpc.WithContextDialer(dialer)), WithoutBlock())
	duration := time.Since(start)

	if conn != nil {
		_ = conn.Close()
	}
	if duration >= cfg.DialTimeout {
		t.Fatalf("expected non-blocking dial, took %v", duration)
	}

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("dialer was not invoked")
	}

	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected dial error: %v", err)
	}
}
