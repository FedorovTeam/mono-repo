package storage

import (
	"context"
	"fmt"
	"net/url"
	"template-service/pkg/config"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type MinioFileStorage struct {
	Client *minio.Client
	cfg    config.MinioConfig
}

func NewMinioFileStorage(cfg config.MinioConfig) (*MinioFileStorage, error) {
	endpoint := cfg.Endpoint
	accessKeyID := cfg.AccessKey
	secretAccessKey := cfg.SecretKey
	useSSL := false
	client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, err
	}
	return &MinioFileStorage{Client: client}, nil
}

func (m *MinioFileStorage) UploadFile(bucketType, prefix, entityPath, filePath string, t int64, region, mime string) (bucket, key, etag string, size int64, err error) {
	bucket = fmt.Sprintf("%s-%s", bucketType, region)
	tm := time.Unix(t, 0)
	key = fmt.Sprintf("%s/year=%d/month=%02d/day=%02d/%s", prefix, tm.Year(), tm.Month(), tm.Day(), entityPath)
	ctx := context.Background()
	// Create bucket if not exists
	err = m.Client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: region})
	if err != nil {
		exists, errBucketExists := m.Client.BucketExists(ctx, bucket)
		if errBucketExists != nil || !exists {
			return "", "", "", 0, fmt.Errorf("bucket create error: %w", err)
		}
	}
	info, err := m.Client.FPutObject(ctx, bucket, key, filePath, minio.PutObjectOptions{ContentType: mime})
	if err != nil {
		return "", "", "", 0, err
	}
	return bucket, key, info.ETag, info.Size, nil
}

// GeneratePresignedURL реализует domain.FileStorage
func (m *MinioFileStorage) GeneratePresignedURL(bucket, objectName string, expiry int64) (string, error) {
	ctx := context.Background()
	reqParams := make(url.Values)
	presignedURL, err := m.Client.PresignedGetObject(ctx, bucket, objectName, time.Duration(expiry)*time.Second, reqParams)
	if err != nil {
		return "", err
	}
	return presignedURL.String(), nil
}
