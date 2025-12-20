package transport

import (
	"context"
	"testing"

	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

func TestHTTPServerStartWithoutTelemetry(t *testing.T) {
	server := NewHTTPServer(0, nil, nil, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("http server returned error: %v", err)
	}
}

func TestGRPCServerStartWithoutTelemetry(t *testing.T) {
	server, err := NewGRPCServer(0, noopPreprocessHandler{}, nil, nil)
	if err != nil {
		t.Fatalf("create grpc server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	cancel()

	if err := <-errCh; err != nil {
		t.Fatalf("grpc server returned error: %v", err)
	}
}

type noopPreprocessHandler struct {
	preprocessv1.UnimplementedPreprocessServiceServer
}

func (noopPreprocessHandler) SubmitPreprocess(context.Context, *preprocessv1.SubmitPreprocessRequest) (*preprocessv1.SubmitPreprocessResponse, error) {
	return &preprocessv1.SubmitPreprocessResponse{}, nil
}

func (noopPreprocessHandler) GetPreprocessStatus(context.Context, *preprocessv1.GetPreprocessStatusRequest) (*preprocessv1.GetPreprocessStatusResponse, error) {
	return &preprocessv1.GetPreprocessStatusResponse{}, nil
}
