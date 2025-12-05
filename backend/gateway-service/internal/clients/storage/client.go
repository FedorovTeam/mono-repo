package storage

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"backend-gateway-service/internal/ctxutils"
	storagepb "backend-gateway-service/proto/backend/storage/v1"
)

// Client оборачивает gRPC-клиент storage-service.
type Client interface {
	GenerateUploadURL(ctx context.Context, in *storagepb.GenerateUploadURLRequest, opts ...grpc.CallOption) (*storagepb.GenerateUploadURLResponse, error)
	Close() error
}

type GRPCClient struct {
	storagepb.StorageServiceClient
	conn *grpc.ClientConn
}

// NewGRPCClient создаёт gRPC-клиент с insecure транспортом для локальной разработки.
func NewGRPCClient(address string, opts ...grpc.DialOption) (*GRPCClient, error) {
	if len(opts) == 0 {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	conn, err := grpc.Dial(address, opts...)
	if err != nil {
		return nil, err
	}
	client := storagepb.NewStorageServiceClient(conn)
	return &GRPCClient{StorageServiceClient: client, conn: conn}, nil
}

// Close закрывает соединение.
func (c *GRPCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

func withMetadata(ctx context.Context) context.Context {
	correlationID := ctxutils.CorrelationID(ctx)
	if correlationID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-correlation-id", correlationID)
	}
	if apiKey := ctxutils.APIKey(ctx); apiKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "x-api-key", apiKey)
	}
	return ctx
}

// GenerateUploadURL запрашивает presigned ссылку в storage-service.
func (c *GRPCClient) GenerateUploadURL(ctx context.Context, in *storagepb.GenerateUploadURLRequest, opts ...grpc.CallOption) (*storagepb.GenerateUploadURLResponse, error) {
	return c.StorageServiceClient.GenerateUploadURL(withMetadata(ctx), in, opts...)
}
