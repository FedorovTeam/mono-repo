package storage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/notification"
)

type fakeNotificationClient struct {
	setCalled bool
	err       error
}

func (f *fakeNotificationClient) BucketExists(ctx context.Context, bucketName string) (bool, error) {
	return true, nil
}
func (f *fakeNotificationClient) MakeBucket(ctx context.Context, bucketName string, opts minio.MakeBucketOptions) error {
	return nil
}
func (f *fakeNotificationClient) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) error {
	return nil
}
func (f *fakeNotificationClient) GetObject(ctx context.Context, bucketName, objectName string) (io.ReadCloser, error) {
	return nil, nil
}
func (f *fakeNotificationClient) StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	return minio.ObjectInfo{}, nil
}
func (f *fakeNotificationClient) PresignHeader(ctx context.Context, method, bucketName, objectName string, expires time.Duration, reqParams url.Values, extraHeaders http.Header) (*url.URL, error) {
	return nil, nil
}
func (f *fakeNotificationClient) ListenBucketNotification(ctx context.Context, bucketName, prefix, suffix string, events []string) <-chan notification.Info {
	return nil
}
func (f *fakeNotificationClient) SetBucketNotification(ctx context.Context, bucketName string, config notification.Configuration) error {
	f.setCalled = true
	return f.err
}

func TestConfigureUploadNotification_Success(t *testing.T) {
	client := &fakeNotificationClient{}
	if err := ConfigureUploadNotification(context.Background(), client, "bucket", "arn:minio:sqs::1:webhook"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !client.setCalled {
		t.Fatal("expected SetBucketNotification to be called")
	}
}

func TestConfigureUploadNotification_Validation(t *testing.T) {
	client := &fakeNotificationClient{}
	if err := ConfigureUploadNotification(context.Background(), client, "", "arn:minio:sqs::1:webhook"); err == nil {
		t.Fatal("expected error for empty bucket")
	}
	if err := ConfigureUploadNotification(context.Background(), client, "bucket", ""); err == nil {
		t.Fatal("expected error for empty arn")
	}
	if err := ConfigureUploadNotification(context.Background(), client, "bucket", "invalid-arn"); err == nil {
		t.Fatal("expected error for invalid arn")
	}
}

func TestConfigureUploadNotification_SetBucketError(t *testing.T) {
	client := &fakeNotificationClient{err: errors.New("boom")}
	if err := ConfigureUploadNotification(context.Background(), client, "bucket", "arn:minio:sqs::1:webhook"); err == nil {
		t.Fatal("expected error from SetBucketNotification")
	}
}
