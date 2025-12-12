package storage

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"backend-gateway-service/internal/domain"
	storagepb "backend-gateway-service/proto/backend/storage/v1"
)

// GRPCClient реализует domain.StorageClient через gRPC storage-service.
type GRPCClient struct {
	storagepb.StorageServiceClient
	conn *grpc.ClientConn
}

// NewGRPCClient создаёт gRPC-клиент с переданными настройками Dial.
func NewGRPCClient(ctx context.Context, address string, opts ...grpc.DialOption) (*GRPCClient, error) {
	conn, err := grpc.DialContext(ctx, address, opts...)
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

// GenerateUploadURL запрашивает presigned ссылку в storage-service.
func (c *GRPCClient) GenerateUploadURL(ctx context.Context, req domain.GenerateUploadURLRequest) (*domain.UploadURL, error) {
	resp, err := c.StorageServiceClient.GenerateUploadURL(withMetadata(ctx), &storagepb.GenerateUploadURLRequest{
		DocumentId:       req.DocumentID,
		FileName:         req.FileName,
		ContentLength:    req.ContentLength,
		CorrelationId:    req.CorrelationID,
		ExpiresInSeconds: req.ExpiresInSeconds,
	})
	if err != nil {
		return nil, err
	}

	return &domain.UploadURL{
		URL:               resp.GetUploadUrl(),
		Bucket:            resp.GetBucket(),
		ObjectKey:         resp.GetObjectKey(),
		MarkdownBucket:    resp.GetMarkdownBucket(),
		MarkdownObjectKey: resp.GetMarkdownObjectKey(),
	}, nil
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
