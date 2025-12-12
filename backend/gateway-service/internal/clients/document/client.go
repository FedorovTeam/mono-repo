package document

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	pb "backend-gateway-service/proto/backend/document/v1"
)

// Client оборачивает gRPC-клиент document-service.
type Client interface {
	CreateDocument(ctx context.Context, in *pb.CreateDocumentRequest, opts ...grpc.CallOption) (*pb.Document, error)
	GetDocument(ctx context.Context, in *pb.GetDocumentRequest, opts ...grpc.CallOption) (*pb.Document, error)
	ListDocuments(ctx context.Context, in *pb.ListDocumentsRequest, opts ...grpc.CallOption) (*pb.ListDocumentsResponse, error)
	ConfirmUpload(ctx context.Context, in *pb.ConfirmUploadRequest, opts ...grpc.CallOption) (*pb.Document, error)
	Close() error
}

type GRPCClient struct {
	pb.DocumentServiceClient
	conn *grpc.ClientConn
}

// NewGRPCClient создаёт gRPC-клиент с переданными настройками Dial.
func NewGRPCClient(ctx context.Context, address string, opts ...grpc.DialOption) (*GRPCClient, error) {
	conn, err := grpc.DialContext(ctx, address, opts...)
	if err != nil {
		return nil, err
	}
	client := pb.NewDocumentServiceClient(conn)
	return &GRPCClient{DocumentServiceClient: client, conn: conn}, nil
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

func (c *GRPCClient) CreateDocument(ctx context.Context, in *pb.CreateDocumentRequest, opts ...grpc.CallOption) (*pb.Document, error) {
	return c.DocumentServiceClient.CreateDocument(withMetadata(ctx), in, opts...)
}

func (c *GRPCClient) GetDocument(ctx context.Context, in *pb.GetDocumentRequest, opts ...grpc.CallOption) (*pb.Document, error) {
	return c.DocumentServiceClient.GetDocument(withMetadata(ctx), in, opts...)
}

func (c *GRPCClient) ListDocuments(ctx context.Context, in *pb.ListDocumentsRequest, opts ...grpc.CallOption) (*pb.ListDocumentsResponse, error) {
	return c.DocumentServiceClient.ListDocuments(withMetadata(ctx), in, opts...)
}

func (c *GRPCClient) ConfirmUpload(ctx context.Context, in *pb.ConfirmUploadRequest, opts ...grpc.CallOption) (*pb.Document, error) {
	return c.DocumentServiceClient.ConfirmUpload(withMetadata(ctx), in, opts...)
}
