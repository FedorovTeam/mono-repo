package storage

import (
	"context"
	"errors"

	storagev1 "backend-document-service/proto/backend/storage/v1"
)

type Client struct {
	storagev1.StorageServiceClient
}

func New(client storagev1.StorageServiceClient) *Client {
	return &Client{StorageServiceClient: client}
}

func (c *Client) ValidateObject(ctx context.Context, bucket, objectKey string) error {
	if c == nil || c.StorageServiceClient == nil {
		return errors.New("storage client is not configured")
	}
	_, err := c.StorageServiceClient.GetObject(ctx, &storagev1.GetObjectRequest{Bucket: bucket, ObjectKey: objectKey})
	return err
}
