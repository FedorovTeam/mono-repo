package grpcdial

import (
	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"

	"backend-gateway-service/internal/config"
	"backend-gateway-service/internal/observability"
)

// DefaultOptions подготавливает общий набор опций для gRPC Dial с учётом конфигурации.
func DefaultOptions(cfg config.GRPCClientConfig, providers observability.Providers) []grpc.DialOption {
	creds := credentials.TransportCredentials(insecure.NewCredentials())
	if !cfg.InsecureOrDefault() {
		creds = credentials.NewClientTLSFromCert(nil, "")
	}

	options := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  cfg.Backoff.BaseDelay,
				Multiplier: cfg.Backoff.Multiplier,
				Jitter:     cfg.Backoff.Jitter,
				MaxDelay:   cfg.Backoff.MaxDelay,
			},
			MinConnectTimeout: cfg.MinConnectTimeout,
		}),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                cfg.Keepalive.Time,
			Timeout:             cfg.Keepalive.Timeout,
			PermitWithoutStream: cfg.Keepalive.PermitWithoutStream,
		}),
		grpc.WithDefaultCallOptions(grpc.WaitForReady(true)),
		grpc.WithBlock(),
	}

	return append(options, observability.GRPCDialOptions(providers)...)
}
