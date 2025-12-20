package server

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s *GRPCServer) buildUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, correlationID := s.cids.ExtractIncoming(ctx)
		ctx = s.cids.WithOutgoing(ctx, correlationID)

		start := time.Now()
		var (
			resp interface{}
			err  error
		)
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered",
					zap.Any("panic", recovered),
					zap.String("method", info.FullMethod),
					zap.String("correlation_id", correlationID),
				)
				err = status.Error(codes.Internal, "internal server error")
				resp = nil
			}
		}()

		resp, err = handler(ctx, req)
		duration := time.Since(start)

		if respWithCorrelation, ok := resp.(interface{ GetCorrelationId() string }); ok {
			if cid := respWithCorrelation.GetCorrelationId(); cid != "" {
				correlationID = cid
			}
		}

		fields := []zap.Field{
			zap.String("method", info.FullMethod),
			zap.Duration("duration", duration),
			zap.String("correlation_id", correlationID),
		}

		if err != nil {
			st, _ := status.FromError(err)
			fields = append(fields, zap.String("code", st.Code().String()))
			s.logger.Error("gRPC request failed", append(fields, zap.Error(err))...)
		} else {
			s.logger.Info("gRPC request handled", fields...)
		}

		return resp, err
	}
}
