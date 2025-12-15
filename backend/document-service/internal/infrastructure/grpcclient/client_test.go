package grpcclient

import (
	"net"
	"testing"
	"time"

	"backend-document-service/pkg/config"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

func TestNewGRPCClientConnects(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	server := grpc.NewServer()
	t.Cleanup(server.Stop)

	go func() {
		_ = server.Serve(listener)
	}()

	cfg := testClientConfig()

	conn, err := NewGRPCClient(listener.Addr().String(), cfg)
	require.NoError(t, err)
	require.NotNil(t, conn)
	t.Cleanup(func() {
		_ = conn.Close()
	})
}

func TestNewGRPCClientTimeout(t *testing.T) {
	cfg := testClientConfig()
	cfg.Timeout = 150 * time.Millisecond

	start := time.Now()
	conn, err := NewGRPCClient("192.0.2.1:12345", cfg)
	duration := time.Since(start)

	if conn != nil {
		defer conn.Close()
	}

	require.Error(t, err)
	require.Less(t, duration, cfg.Timeout*3)
}

func testClientConfig() config.ServicesGRPC {
	return config.ServicesGRPC{
		Timeout: 2 * time.Second,
		Keepalive: config.KeepaliveConfig{
			Time:                30 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		},
		Backoff: config.BackoffConfig{
			BaseDelay:  500 * time.Millisecond,
			Multiplier: 1.6,
			Jitter:     0.2,
			MaxDelay:   2 * time.Second,
		},
	}
}
