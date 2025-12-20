package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	pb "github.com/mono-repo/table-extractor-service/proto/backend/tableextractor/v1"
)

type GRPCServer struct {
	logger *zap.Logger
	uc     ExtractionUseCase
	port   int
	tracer trace.TracerProvider
}

func NewGRPCServer(logger *zap.Logger, uc ExtractionUseCase, port int, tracerProvider trace.TracerProvider) *GRPCServer {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &GRPCServer{
		logger: logger,
		uc:     uc,
		port:   port,
		tracer: tracerProvider,
	}
}

func (s *GRPCServer) Run(ctx context.Context) error {
	address := fmt.Sprintf(":%d", s.port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}

	serverHandler := otelgrpc.NewServerHandler(
		otelgrpc.WithTracerProvider(s.tracer),
	)

	grpcServer := grpc.NewServer(
		grpc.StatsHandler(serverHandler),
		grpc.UnaryInterceptor(s.buildUnaryInterceptor()),
	)

	pb.RegisterTableExtractorServiceServer(grpcServer, NewHandler(s.uc, s.logger))

	go func() {
		<-ctx.Done()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.logger.Info("shutting down gRPC server", zap.String("address", address))
		grpcServer.GracefulStop()
		<-stopCtx.Done()
	}()

	s.logger.Info("starting gRPC server", zap.String("address", address))
	if err := grpcServer.Serve(lis); err != nil {
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	}

	return nil
}
