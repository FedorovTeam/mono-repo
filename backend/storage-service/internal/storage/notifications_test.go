package storage

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/minio/minio-go/v7/pkg/notification"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	documentv1 "backend-storage-service/proto/backend/document/v1"
)

type fakeDocumentClient struct {
	lastRequest *documentv1.ConfirmUploadRequest
	err         error
}

func (f *fakeDocumentClient) ConfirmUpload(ctx context.Context, in *documentv1.ConfirmUploadRequest, opts ...grpc.CallOption) (*documentv1.Document, error) {
	f.lastRequest = in
	return &documentv1.Document{}, nil
}

func TestExtractDocumentID(t *testing.T) {
	require.Equal(t, "doc-1", extractDocumentID("org/x/doc-1.docx"))
	require.Equal(t, "", extractDocumentID("/"))
}

func TestHandleNotificationSendsConfirm(t *testing.T) {
	client := &fakeDocumentClient{}
	handler := NewUploadEventHandler(client, nil)

	var info notification.Info
	payload := []byte(`{"Records":[{"s3":{"bucket":{"name":"bucket"},"object":{"key":"path/doc-2.docx"}}}]}`)
	require.NoError(t, json.Unmarshal(payload, &info))

	handler.handleNotification(context.Background(), info)

	require.NotNil(t, client.lastRequest)
	require.Equal(t, "doc-2", client.lastRequest.DocumentId)
	require.Equal(t, "path/doc-2.docx", client.lastRequest.ObjectKey)
}
