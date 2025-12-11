package document

import (
	"context"
	"errors"

	storagev1 "backend-document-service/proto/backend/storage/v1"

	"google.golang.org/grpc"
)

type stubStorageClient struct {
	attempts int
	err      error
}

func (s *stubStorageClient) UploadOriginal(ctx context.Context, in *storagev1.UploadOriginalRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *stubStorageClient) UploadMarkdown(ctx context.Context, in *storagev1.UploadMarkdownRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *stubStorageClient) GetObject(ctx context.Context, in *storagev1.GetObjectRequest, opts ...grpc.CallOption) (*storagev1.GetObjectResponse, error) {
	s.attempts++
	if s.err != nil {
		return nil, s.err
	}

	return &storagev1.GetObjectResponse{Bucket: in.GetBucket(), ObjectKey: in.GetObjectKey()}, nil
}

func (s *stubStorageClient) GenerateUploadURL(ctx context.Context, in *storagev1.GenerateUploadURLRequest, opts ...grpc.CallOption) (*storagev1.GenerateUploadURLResponse, error) {
	return nil, errors.New("not implemented")
}
