package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"time"

	"github.com/minio/minio-go/v7"
)

// Client описывает подмножество методов MinIO, используемых сервисом.
type Client interface {
	BucketExists(ctx context.Context, bucketName string) (bool, error)
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) error
	GetObject(ctx context.Context, bucketName, objectName string) (io.ReadCloser, error)
	PresignHeader(ctx context.Context, method, bucketName, objectName string, expires time.Duration, reqParams url.Values, extraHeaders http.Header) (*url.URL, error)
}

// MinioAPI адаптирует minio.Client к локальному интерфейсу.
type MinioAPI struct {
	client *minio.Client
}

// NewMinioAPI создает адаптер над minio.Client.
func NewMinioAPI(client *minio.Client) *MinioAPI {
	return &MinioAPI{client: client}
}

// BucketExists проксирует вызов к minio.Client.
func (m *MinioAPI) BucketExists(ctx context.Context, bucketName string) (bool, error) {
	return m.client.BucketExists(ctx, bucketName)
}

// PutObject сохраняет объект в MinIO и игнорирует UploadInfo.
func (m *MinioAPI) PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) error {
	_, err := m.client.PutObject(ctx, bucketName, objectName, reader, objectSize, opts)
	return err
}

// GetObject считывает объект и возвращает io.ReadCloser.
func (m *MinioAPI) GetObject(ctx context.Context, bucketName, objectName string) (io.ReadCloser, error) {
	object, err := m.client.GetObject(ctx, bucketName, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	return object, nil
}

// PresignHeader проксирует вызов к minio.Client с сохранением всех подписываемых заголовков.
func (m *MinioAPI) PresignHeader(ctx context.Context, method, bucketName, objectName string, expires time.Duration, reqParams url.Values, extraHeaders http.Header) (*url.URL, error) {
	return m.client.PresignHeader(ctx, method, bucketName, objectName, expires, reqParams, extraHeaders)
}

// Service инкапсулирует работу с MinIO и генерацией ключей.
type Service struct {
	client         Client
	keyGenerator   *ObjectKeyGenerator
	docxBucket     string
	markdownBucket string
}

// ObjectLocation описывает размещение объекта в хранилище.
type ObjectLocation struct {
	Bucket string
	Key    string
}

// NewService конструирует сервис работы с хранилищем.
func NewService(client Client, keyGenerator *ObjectKeyGenerator, docxBucket, markdownBucket string) *Service {
	return &Service{
		client:         client,
		keyGenerator:   keyGenerator,
		docxBucket:     docxBucket,
		markdownBucket: markdownBucket,
	}
}

// UploadOriginal сохраняет исходный документ в приватный бакет knowledge-docx.
func (s *Service) UploadOriginal(ctx context.Context, orgSlug, projectSlug, documentID, extension string, content []byte) (ObjectLocation, error) {
	if err := s.ensureBucket(ctx, s.docxBucket); err != nil {
		return ObjectLocation{}, err
	}

	key, err := s.keyGenerator.BuildKey(orgSlug, projectSlug, documentID, extension)
	if err != nil {
		return ObjectLocation{}, err
	}

	opts := minio.PutObjectOptions{ContentType: detectContentType(extension)}
	if err := s.client.PutObject(ctx, s.docxBucket, key, bytes.NewReader(content), int64(len(content)), opts); err != nil {
		return ObjectLocation{}, fmt.Errorf("failed to upload original: %w", err)
	}

	return ObjectLocation{Bucket: s.docxBucket, Key: key}, nil
}

// UploadMarkdown сохраняет Markdown-версию документа в бакет knowledge-md.
func (s *Service) UploadMarkdown(ctx context.Context, orgSlug, projectSlug, documentID string, content []byte) (ObjectLocation, error) {
	if err := s.ensureBucket(ctx, s.markdownBucket); err != nil {
		return ObjectLocation{}, err
	}

	key, err := s.keyGenerator.BuildKey(orgSlug, projectSlug, documentID, "md")
	if err != nil {
		return ObjectLocation{}, err
	}

	opts := minio.PutObjectOptions{ContentType: "text/markdown"}
	if err := s.client.PutObject(ctx, s.markdownBucket, key, bytes.NewReader(content), int64(len(content)), opts); err != nil {
		return ObjectLocation{}, fmt.Errorf("failed to upload markdown: %w", err)
	}

	return ObjectLocation{Bucket: s.markdownBucket, Key: key}, nil
}

// DownloadObject возвращает содержимое объекта для указанного бакета.
func (s *Service) DownloadObject(ctx context.Context, bucket, key string) ([]byte, error) {
	if err := s.ensureBucket(ctx, bucket); err != nil {
		return nil, err
	}

	reader, err := s.client.GetObject(ctx, bucket, key)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch object: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read object: %w", err)
	}

	return data, nil
}

func (s *Service) ensureBucket(ctx context.Context, bucket string) error {
	exists, err := s.client.BucketExists(ctx, bucket)
	if err != nil {
		return fmt.Errorf("cannot check bucket %s: %w", bucket, err)
	}

	if !exists {
		return fmt.Errorf("bucket %s does not exist or is not accessible", bucket)
	}

	return nil
}

func detectContentType(extension string) string {
	ext := extension
	if ext != "" && ext[0] != '.' {
		ext = "." + extension
	}

	if ctype := mime.TypeByExtension(ext); ctype != "" {
		return ctype
	}

	return "application/octet-stream"
}
