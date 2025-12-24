package document

import (
	"context"
	"errors"

	storagev1 "backend-document-service/proto/backend/storage/v1"

	"google.golang.org/grpc"
)

type stubStorageClient struct {
	attempts   int
	err        error
	objects    map[string]map[string][]byte
	lastUpload *storagev1.UploadMarkdownRequest
	lastGet    struct {
		bucket string
		key    string
	}
	gets []struct {
		bucket string
		key    string
	}
}

func (s *stubStorageClient) UploadOriginal(ctx context.Context, in *storagev1.UploadOriginalRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	return nil, errors.New("not implemented")
}

func (s *stubStorageClient) UploadMarkdown(ctx context.Context, in *storagev1.UploadMarkdownRequest, opts ...grpc.CallOption) (*storagev1.UploadObjectResponse, error) {
	s.lastUpload = in
	if s.objects == nil {
		s.objects = make(map[string]map[string][]byte)
	}
	bucket := "markdown-bucket"
	if _, ok := s.objects[bucket]; !ok {
		s.objects[bucket] = make(map[string][]byte)
	}
	objectKey := in.GetDocumentId() + ".md"
	s.objects[bucket][objectKey] = in.GetContent()
	return &storagev1.UploadObjectResponse{Bucket: bucket, ObjectKey: objectKey}, nil
}

func (s *stubStorageClient) GetObject(ctx context.Context, in *storagev1.GetObjectRequest, opts ...grpc.CallOption) (*storagev1.GetObjectResponse, error) {
	s.attempts++
	if s.err != nil {
		return nil, s.err
	}
	s.lastGet.bucket = in.GetBucket()
	s.lastGet.key = in.GetObjectKey()
	s.gets = append(s.gets, struct {
		bucket string
		key    string
	}{bucket: in.GetBucket(), key: in.GetObjectKey()})

	content := []byte{}
	if s.objects != nil {
		if bucketObjects, ok := s.objects[in.GetBucket()]; ok {
			if data, ok := bucketObjects[in.GetObjectKey()]; ok {
				content = data
			}
		}
	}

	return &storagev1.GetObjectResponse{Bucket: in.GetBucket(), ObjectKey: in.GetObjectKey(), Content: content}, nil
}

func (s *stubStorageClient) GenerateUploadURL(ctx context.Context, in *storagev1.GenerateUploadURLRequest, opts ...grpc.CallOption) (*storagev1.GenerateUploadURLResponse, error) {
	return nil, errors.New("not implemented")
}
