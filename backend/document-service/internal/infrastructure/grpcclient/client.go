package grpcclient

import (
	"backend-document-service/pkg/config"
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

func NewGRPCClient(addr string, cfg config.ServicesGRPC) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	conn, err := grpc.DialContext(
		ctx,
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                cfg.Keepalive.Time,
			Timeout:             cfg.Keepalive.Timeout,
			PermitWithoutStream: cfg.Keepalive.PermitWithoutStream,
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(16*1024*1024)), // увеличивает до 16 МБ
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  cfg.Backoff.BaseDelay,
				Multiplier: cfg.Backoff.Multiplier,
				Jitter:     cfg.Backoff.Jitter,
				MaxDelay:   cfg.Backoff.MaxDelay,
			},
			MinConnectTimeout: cfg.Timeout,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", addr, err)
	}

	return conn, nil
}
