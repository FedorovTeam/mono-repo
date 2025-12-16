package document

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"backend-gateway-service/internal/domain"
	pb "backend-gateway-service/proto/backend/document/v1"
)

// GRPCClient реализует domain.DocumentClient через gRPC document-service.
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

// CreateDocument создает документ в document-service.
func (c *GRPCClient) CreateDocument(ctx context.Context, req domain.CreateDocumentRequest) (*domain.Document, error) {
	resp, err := c.DocumentServiceClient.CreateDocument(withMetadata(ctx), &pb.CreateDocumentRequest{
		Id:                req.ID,
		FileName:          req.FileName,
		Repository:        req.Repository,
		Branch:            req.Branch,
		Path:              req.Path,
		CorrelationId:     req.CorrelationID,
		ContentLength:     req.ContentLength,
		UploadBucket:      req.UploadBucket,
		UploadObjectKey:   req.UploadObjectKey,
		MarkdownBucket:    req.MarkdownBucket,
		MarkdownObjectKey: req.MarkdownObjectKey,
		Owner:             valueOrEmpty(req.RepositoryOwner),
		OcrEnabled:        req.EnablePdfOcr,
	})
	if err != nil {
		return nil, err
	}
	return mapDocument(resp), nil
}

// GetDocument возвращает документ по идентификатору.
func (c *GRPCClient) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	resp, err := c.DocumentServiceClient.GetDocument(withMetadata(ctx), &pb.GetDocumentRequest{Id: id})
	if err != nil {
		return nil, err
	}
	return mapDocument(resp), nil
}

// ListDocuments возвращает список документов по фильтру и пагинации.
func (c *GRPCClient) ListDocuments(ctx context.Context, filter domain.DocumentFilter, pagination domain.Pagination) (*domain.DocumentList, error) {
	req := &pb.ListDocumentsRequest{Limit: int32(pagination.Limit), Offset: int32(pagination.Offset)}
	if filter.Status != nil {
		req.FilterByStatus = true
		req.StatusFilter = protoStatusFromDomain(*filter.Status)
	}
	if filter.Repository != nil {
		req.Repository = *filter.Repository
	}

	resp, err := c.DocumentServiceClient.ListDocuments(withMetadata(ctx), req)
	if err != nil {
		return nil, err
	}

	docs := make([]domain.Document, 0, len(resp.GetDocuments()))
	for _, doc := range resp.GetDocuments() {
		mapped := mapDocument(doc)
		if mapped != nil {
			docs = append(docs, *mapped)
		}
	}

	return &domain.DocumentList{Documents: docs, TotalCount: resp.GetTotalCount()}, nil
}

// ConfirmUpload подтверждает успешную загрузку файла.
func (c *GRPCClient) ConfirmUpload(ctx context.Context, req domain.ConfirmUploadRequest) (*domain.Document, error) {
	resp, err := c.DocumentServiceClient.ConfirmUpload(withMetadata(ctx), &pb.ConfirmUploadRequest{
		DocumentId:    req.DocumentID,
		Bucket:        req.Bucket,
		ObjectKey:     req.ObjectKey,
		CorrelationId: req.CorrelationID,
	})
	if err != nil {
		return nil, err
	}
	return mapDocument(resp), nil
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

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
