package grpcdial

import (
	"backend-document-service/pkg/config"
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

type Option func(*dialSettings)

type dialSettings struct {
	useBlock   bool
	additional []grpc.DialOption
}

// WithoutBlock disables grpc.WithBlock() and skips dial timeout wrapping.
func WithoutBlock() Option {
	return func(settings *dialSettings) {
		settings.useBlock = false
	}
}

// WithDialOptions appends additional gRPC dial options to the default set.
func WithDialOptions(opts ...grpc.DialOption) Option {
	return func(settings *dialSettings) {
		settings.additional = append(settings.additional, opts...)
	}
}

// DialContext dials the target using secure defaults and client configuration.
func DialContext(ctx context.Context, target string, cfg config.GRPCClient, opts ...Option) (*grpc.ClientConn, error) {
	settings := dialSettings{useBlock: true}
	for _, opt := range opts {
		opt(&settings)
	}

	dialOptions := buildOptions(cfg, settings.useBlock)
	if len(settings.additional) > 0 {
		dialOptions = append(dialOptions, settings.additional...)
	}

	dialCtx := ctx
	cancel := func() {}
	if settings.useBlock && cfg.DialTimeout > 0 {
		dialCtx, cancel = context.WithTimeout(ctx, cfg.DialTimeout)
	}
	defer cancel()

	return grpc.DialContext(dialCtx, target, dialOptions...)
}

func buildOptions(cfg config.GRPCClient, useBlock bool) []grpc.DialOption {
	options := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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
	}

	if useBlock {
		options = append(options, grpc.WithBlock())
	}

	return options
}
