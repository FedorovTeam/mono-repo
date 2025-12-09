package grpcdial

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"
	"unsafe"

	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"

	"backend-gateway-service/internal/config"
	"backend-gateway-service/internal/observability"
)

func TestDefaultOptionsIncludesConnectionSettings(t *testing.T) {
	cfg := config.GRPCClientConfig{
		Insecure:          boolPtr(true),
		DialTimeout:       2 * time.Second,
		MinConnectTimeout: time.Second,
		Keepalive: config.GRPCKeepaliveConfig{
			Time:                15 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		},
		Backoff: config.GRPCBackoffConfig{
			BaseDelay:  500 * time.Millisecond,
			Multiplier: 1.5,
			Jitter:     0.3,
			MaxDelay:   5 * time.Second,
		},
	}

	lis := bufconn.Listen(1024)
	server := grpc.NewServer()
	t.Cleanup(server.Stop)

	go func() {
		_ = server.Serve(lis)
	}()

	options := DefaultOptions(cfg, observability.Providers{})
	options = append(options, grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	}))

	ctx, cancel := context.WithTimeout(context.Background(), cfg.DialTimeout)
	defer cancel()

	conn, err := grpc.DialContext(ctx, "bufconn", options...)
	if err != nil {
		t.Fatalf("dial failed: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	dopts := dialOptions(conn)

	if !dopts.FieldByName("block").Bool() {
		t.Fatalf("expected WithBlock option to be set")
	}

	minConnTimeoutField := dopts.FieldByName("minConnectTimeout")
	minConnectTimeout := reflect.NewAt(minConnTimeoutField.Type(), unsafe.Pointer(minConnTimeoutField.UnsafeAddr())).Elem().Call(nil)[0].Interface().(time.Duration)
	if minConnectTimeout != cfg.MinConnectTimeout {
		t.Fatalf("minConnectTimeout mismatch: got %s want %s", minConnectTimeout, cfg.MinConnectTimeout)
	}

	if dopts.FieldByName("bs").IsNil() {
		t.Fatalf("backoff strategy must be set")
	}

	copts := reflect.NewAt(dopts.FieldByName("copts").Type(), unsafe.Pointer(dopts.FieldByName("copts").UnsafeAddr())).Elem()
	keepaliveParams := copts.FieldByName("KeepaliveParams")
	if keepaliveParams.FieldByName("Time").Interface().(time.Duration) != cfg.Keepalive.Time {
		t.Fatalf("keepalive time mismatch")
	}
	if keepaliveParams.FieldByName("Timeout").Interface().(time.Duration) != cfg.Keepalive.Timeout {
		t.Fatalf("keepalive timeout mismatch")
	}

	if copts.FieldByName("TransportCredentials").IsNil() {
		t.Fatalf("transport credentials should be configured")
	}

	if !hasWaitForReady(dopts.FieldByName("callOptions")) {
		t.Fatalf("expected default WaitForReady call option")
	}
}

func hasWaitForReady(callOptions reflect.Value) bool {
	options := reflect.NewAt(callOptions.Type(), unsafe.Pointer(callOptions.UnsafeAddr())).Elem()
	for i := 0; i < options.Len(); i++ {
		opt := options.Index(i).Interface()
		if ff, ok := opt.(grpc.FailFastCallOption); ok && !ff.FailFast {
			return true
		}
	}

	return false
}

func dialOptions(conn *grpc.ClientConn) reflect.Value {
	clientConn := reflect.ValueOf(conn).Elem()
	optionsField := clientConn.FieldByName("dopts")
	return reflect.NewAt(optionsField.Type(), unsafe.Pointer(optionsField.UnsafeAddr())).Elem()
}

func boolPtr(v bool) *bool {
	return &v
}
