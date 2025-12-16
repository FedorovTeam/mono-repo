package transport

import (
	"context"
	"fmt"
	"net"

	"google.golang.org/grpc"

	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

type GRPCServer struct {
	server   *grpc.Server
	listener net.Listener
}

func NewGRPCServer(port int, handler preprocessv1.PreprocessServiceServer) (*GRPCServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen grpc: %w", err)
	}

	srv := grpc.NewServer()
	preprocessv1.RegisterPreprocessServiceServer(srv, handler)

	return &GRPCServer{server: srv, listener: listener}, nil
}

func (s *GRPCServer) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		if err := s.server.Serve(s.listener); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		s.server.GracefulStop()
		return nil
	}
}
