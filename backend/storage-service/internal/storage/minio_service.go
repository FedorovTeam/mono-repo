package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/notification"
)

// Client описывает подмножество методов MinIO, используемых сервисом.
type Client interface {
	BucketExists(ctx context.Context, bucketName string) (bool, error)
	MakeBucket(ctx context.Context, bucketName string, opts minio.MakeBucketOptions) error
	PutObject(ctx context.Context, bucketName, objectName string, reader io.Reader, objectSize int64, opts minio.PutObjectOptions) error
	GetObject(ctx context.Context, bucketName, objectName string) (io.ReadCloser, error)
	StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
	PresignHeader(ctx context.Context, method, bucketName, objectName string, expires time.Duration, reqParams url.Values, extraHeaders http.Header) (*url.URL, error)
	SetBucketNotification(ctx context.Context, bucketName string, config notification.Configuration) error
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

// MakeBucket создаёт бакет, если он отсутствует.
func (m *MinioAPI) MakeBucket(ctx context.Context, bucketName string, opts minio.MakeBucketOptions) error {
	return m.client.MakeBucket(ctx, bucketName, opts)
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

// StatObject возвращает метаданные объекта.
func (m *MinioAPI) StatObject(ctx context.Context, bucketName, objectName string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	return m.client.StatObject(ctx, bucketName, objectName, opts)
}

// PresignHeader проксирует вызов к minio.Client с сохранением всех подписываемых заголовков.
func (m *MinioAPI) PresignHeader(ctx context.Context, method, bucketName, objectName string, expires time.Duration, reqParams url.Values, extraHeaders http.Header) (*url.URL, error) {
	return m.client.PresignHeader(ctx, method, bucketName, objectName, expires, reqParams, extraHeaders)
}

// SetBucketNotification проксирует конфигурацию уведомлений бакета.
func (m *MinioAPI) SetBucketNotification(ctx context.Context, bucketName string, config notification.Configuration) error {
	return m.client.SetBucketNotification(ctx, bucketName, config)
}

// Service инкапсулирует работу с MinIO и генерацией ключей.
type Service struct {
	client             Client
	keyGenerator       *ObjectKeyGenerator
	docxBucket         string
	markdownBucket     string
	allowedExtensions  map[string]struct{}
	maxUploadSizeBytes int64
	defaultUploadTTL   int64
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
)

// UploadValidationConfig задает ограничения на выдачу presigned URL.
type UploadValidationConfig struct {
	AllowedExtensions map[string]struct{}
	MaxSizeBytes      int64
	SignedURLTTL      int64
}

// GenerateUploadURLParams описывает данные для подготовки presigned PUT ссылки.
type GenerateUploadURLParams struct {
	OrgSlug          string
	ProjectSlug      string
	DocumentID       string
	FileName         string
	ContentLength    int64
	CorrelationID    string
	ExpiresInSeconds int64
}

// PresignedUpload содержит ссылку и ключ объекта для загрузки.
type PresignedUpload struct {
	UploadURL         *url.URL
	Bucket            string
	ObjectKey         string
	ExpiresInSeconds  int64
	MarkdownBucket    string
	MarkdownObjectKey string
}

// PresignedDownload содержит ссылку на скачивание объекта.
type PresignedDownload struct {
	DownloadURL      *url.URL
	ExpiresInSeconds int64
}

// ObjectLocation описывает размещение объекта в хранилище.
type ObjectLocation struct {
	Bucket string
	Key    string
}

// MinioClientConfig содержит параметры подключения и список обязательных бакетов.
type MinioClientConfig struct {
	Endpoint  string
	AccessKey string
	SecretKey string
	UseSSL    bool
	Buckets   []string
}

// NewMinioClient создает клиента MinIO и инициализирует указанные бакеты.
func NewMinioClient(ctx context.Context, cfg MinioClientConfig) (*MinioAPI, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		BucketLookup: minio.BucketLookupPath,
	})
	if err != nil {
		return nil, fmt.Errorf("не удалось инициализировать MinIO клиент: %w", err)
	}

	api := NewMinioAPI(client)
	if err := initializeBuckets(ctx, api, cfg.Buckets); err != nil {
		return nil, err
	}

	return api, nil
}

// NewService конструирует сервис работы с хранилищем.
func NewService(client Client, keyGenerator *ObjectKeyGenerator, docxBucket, markdownBucket string, validation UploadValidationConfig) *Service {
	allowed := validation.AllowedExtensions
	if len(allowed) == 0 {
		allowed = map[string]struct{}{
			"doc":  {},
			"docx": {},
			"pdf":  {},
		}
	}

	return &Service{
		client:             client,
		keyGenerator:       keyGenerator,
		docxBucket:         docxBucket,
		markdownBucket:     markdownBucket,
		allowedExtensions:  allowed,
		maxUploadSizeBytes: validation.MaxSizeBytes,
		defaultUploadTTL:   validation.SignedURLTTL,
	}
}

// UploadOriginal сохраняет исходный документ в приватный бакет knowledge-original.
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

// ObjectData содержит содержимое и метаданные объекта.
type ObjectData struct {
	Content   []byte
	ETag      string
	UpdatedAt time.Time
}

// DownloadObject возвращает содержимое объекта и его метаданные для указанного бакета.
func (s *Service) DownloadObject(ctx context.Context, bucket, key string) (ObjectData, error) {
	if err := s.ensureBucket(ctx, bucket); err != nil {
		return ObjectData{}, err
	}

	info, err := s.client.StatObject(ctx, bucket, key, minio.StatObjectOptions{})
	if err != nil {
		return ObjectData{}, fmt.Errorf("failed to stat object: %w", err)
	}
	updatedAt := info.LastModified
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}

	reader, err := s.client.GetObject(ctx, bucket, key)
	if err != nil {
		return ObjectData{}, fmt.Errorf("failed to fetch object: %w", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		return ObjectData{}, fmt.Errorf("failed to read object: %w", err)
	}

	return ObjectData{
		Content:   data,
		ETag:      info.ETag,
		UpdatedAt: updatedAt,
	}, nil
}

// GenerateUploadURL валидирует параметры и подписывает ссылку для загрузки исходного документа.
func (s *Service) GenerateUploadURL(ctx context.Context, bucket string, params GenerateUploadURLParams) (PresignedUpload, error) {
	if params.DocumentID == "" {
		return PresignedUpload{}, fmt.Errorf("%w: document_id is required", ErrInvalidArgument)
	}

	if params.FileName == "" {
		return PresignedUpload{}, fmt.Errorf("%w: file_name is required", ErrInvalidArgument)
	}

	if params.ContentLength <= 0 {
		return PresignedUpload{}, fmt.Errorf("%w: content_length must be greater than zero", ErrInvalidArgument)
	}

	expiresInSeconds := params.ExpiresInSeconds
	if expiresInSeconds <= 0 {
		expiresInSeconds = s.defaultUploadTTL
	}

	if expiresInSeconds <= 0 {
		return PresignedUpload{}, fmt.Errorf("%w: expires_in_seconds must be greater than zero", ErrInvalidArgument)
	}

	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(params.FileName)), ".")
	if extension == "" {
		return PresignedUpload{}, fmt.Errorf("%w: invalid file extension", ErrInvalidArgument)
	}

	if _, ok := s.allowedExtensions[extension]; !ok {
		return PresignedUpload{}, fmt.Errorf("%w: unsupported file extension %s", ErrInvalidArgument, extension)
	}

	if s.maxUploadSizeBytes > 0 && params.ContentLength > s.maxUploadSizeBytes {
		return PresignedUpload{}, fmt.Errorf("%w: content_length exceeds limit %d", ErrInvalidArgument, s.maxUploadSizeBytes)
	}

	if err := s.ensureBucket(ctx, bucket); err != nil {
		return PresignedUpload{}, err
	}
	if err := s.ensureBucket(ctx, s.markdownBucket); err != nil {
		return PresignedUpload{}, err
	}

	key, err := s.keyGenerator.BuildKey(params.OrgSlug, params.ProjectSlug, params.DocumentID, extension)
	if err != nil {
		return PresignedUpload{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	markdownKey := strings.TrimSuffix(key, filepath.Ext(key)) + ".md"

	headers := http.Header{}
	if params.CorrelationID != "" {
		headers.Set("x-amz-meta-correlation-id", params.CorrelationID)
	}

	expires := time.Duration(expiresInSeconds) * time.Second
	url, err := s.client.PresignHeader(ctx, http.MethodPut, bucket, key, expires, nil, headers)
	if err != nil {
		return PresignedUpload{}, fmt.Errorf("cannot presign upload url: %w", err)
	}

	return PresignedUpload{
		UploadURL:         url,
		Bucket:            bucket,
		ObjectKey:         key,
		MarkdownBucket:    s.markdownBucket,
		MarkdownObjectKey: markdownKey,
		ExpiresInSeconds:  expiresInSeconds,
	}, nil
}

// GenerateDownloadURL готовит presigned GET ссылку для уже существующего объекта.
func (s *Service) GenerateDownloadURL(ctx context.Context, bucket, objectKey string, expiresInSeconds int64, correlationID string) (PresignedDownload, error) {
	if bucket == "" {
		return PresignedDownload{}, fmt.Errorf("%w: bucket is required", ErrInvalidArgument)
	}

	if objectKey == "" {
		return PresignedDownload{}, fmt.Errorf("%w: object_key is required", ErrInvalidArgument)
	}

	if expiresInSeconds <= 0 {
		expiresInSeconds = s.defaultUploadTTL
	}

	if expiresInSeconds <= 0 {
		return PresignedDownload{}, fmt.Errorf("%w: expires_in_seconds must be greater than zero", ErrInvalidArgument)
	}

	if err := s.ensureBucket(ctx, bucket); err != nil {
		return PresignedDownload{}, err
	}
	if _, err := s.client.StatObject(ctx, bucket, objectKey, minio.StatObjectOptions{}); err != nil {
		return PresignedDownload{}, fmt.Errorf("failed to stat object: %w", err)
	}

	headers := http.Header{}
	if correlationID != "" {
		headers.Set("x-amz-meta-correlation-id", correlationID)
	}

	expires := time.Duration(expiresInSeconds) * time.Second
	url, err := s.client.PresignHeader(ctx, http.MethodGet, bucket, objectKey, expires, nil, headers)
	if err != nil {
		return PresignedDownload{}, fmt.Errorf("cannot presign download url: %w", err)
	}

	return PresignedDownload{
		DownloadURL:      url,
		ExpiresInSeconds: expiresInSeconds,
	}, nil
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

func initializeBuckets(ctx context.Context, client Client, buckets []string) error {
	for _, bucket := range buckets {
		if bucket == "" {
			return fmt.Errorf("bucket name must not be empty")
		}

		exists, err := client.BucketExists(ctx, bucket)
		if err != nil {
			return fmt.Errorf("не удалось проверить наличие бакета %s: %w", bucket, err)
		}

		if exists {
			continue
		}

		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{}); err != nil {
			return fmt.Errorf("не удалось создать бакет %s: %w", bucket, err)
		}
	}

	return nil
}
