package storage

import (
	"context"
	"fmt"

	storagev1 "backend-document-service/proto/backend/storage/v1"
)

// Client адаптирует gRPC-клиент StorageService к интерфейсу usecase.
type Client struct {
	client storagev1.StorageServiceClient
}

func New(client storagev1.StorageServiceClient) *Client {
	return &Client{client: client}
}

func (c *Client) ValidateObject(ctx context.Context, bucket, objectKey string) (string, error) {
	if c.client == nil {
		return "", fmt.Errorf("storage client is not configured")
	}
	resp, err := c.client.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: bucket, ObjectKey: objectKey})
	if err != nil {
		return "", err
	}
	return resp.GetEtag(), nil
}
