package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	otelmetric "go.opentelemetry.io/otel/metric/noop"
	"google.golang.org/grpc"

	preprocessv1 "preprocess-service/proto/backend/preprocess/v1"
)

type GRPCServer struct {
	server   *grpc.Server
	listener net.Listener
}

func NewGRPCServer(port int, handler preprocessv1.PreprocessServiceServer, meter metric.Meter, logger *slog.Logger) (*GRPCServer, error) {
	if meter == nil {
		meter = otelmetric.NewMeterProvider().Meter("preprocess-service")
	}
	if logger == nil {
		logger = slog.Default()
	}

	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return nil, fmt.Errorf("listen grpc: %w", err)
	}

	srv := grpc.NewServer(
		grpc.StatsHandler(otelgrpc.NewServerHandler()),
		grpc.ChainUnaryInterceptor(
			correlationUnaryInterceptor(logger),
			metricsUnaryInterceptor(meter, logger),
		),
	)
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
