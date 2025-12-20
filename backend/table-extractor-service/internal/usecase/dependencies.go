package usecase

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/mono-repo/table-extractor-service/internal/domain"
)

// QueueRepo описывает хранилище очереди задач извлечения таблиц.
type QueueRepo interface {
	UpsertOrGetActive(ctx context.Context, job domain.TableExtractionJob, queueLimit int) (domain.TableExtractionJob, bool, error)
	AcquirePendingBatch(ctx context.Context, limit int, now time.Time) ([]domain.TableExtractionJob, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.TableExtractionStatus, statusReason, errorCode, errorMessage string, finishedAt *time.Time) error
	Postpone(ctx context.Context, id uuid.UUID, nextRunAt time.Time, statusReason string) error
	ListProcessingBefore(ctx context.Context, updatedBefore time.Time) ([]domain.TableExtractionJob, error)
	GetByID(ctx context.Context, id uuid.UUID) (domain.TableExtractionJob, bool, error)
	CountActive(ctx context.Context) (int, error)
	Cleanup(ctx context.Context, finishedBefore time.Time) (int, error)
	RetryFailed(ctx context.Context, maxAttempts int, retryDelay time.Duration, now time.Time) (int, error)
}

// StorageClient инкапсулирует работу с MinIO/объектным хранилищем.
type StorageClient interface {
	Download(ctx context.Context, bucket, objectKey string) (io.ReadCloser, error)
	Upload(ctx context.Context, bucket, objectKey, contentType string, content io.Reader, size int64) error
}

// FileCache отвечает за подготовку локальной копии исходного файла для извлечения таблиц.
type FileCache interface {
	Fetch(ctx context.Context, original domain.ArtifactLocation) (CachedFile, error)
}

// CachedFile представляет локальную копию исходника с функцией очистки ресурсов.
type CachedFile struct {
	Path    string
	Cleanup func()
}

// Extractor — Poppler-пайплайн, который получает путь к локальному PDF и возвращает артефакты.
type Extractor interface {
	Extract(ctx context.Context, pdfPath string) (ExtractionResult, error)
}

// ExtractionResult содержит сгенерированные артефакты для загрузки в MinIO.
type ExtractionResult struct {
	RawJSON        []byte
	NormalizedJSON []byte
	Markdown       []byte
	Manifest       []byte
	DebugFiles     map[string][]byte
}

// Clock абстрагирует получение текущего времени (для тестов/детерминизма).
type Clock interface {
	Now() time.Time
}

// IDGenerator отвечает за генерацию идентификаторов задач.
type IDGenerator interface {
	New() uuid.UUID
}

type realClock struct{}

func (realClock) Now() time.Time {
	return time.Now().UTC()
}

type uuidGenerator struct{}

func (uuidGenerator) New() uuid.UUID {
	return uuid.New()
}

func resolveClock(clock Clock) Clock {
	if clock == nil {
		return realClock{}
	}
	return clock
}

func resolveIDGenerator(generator IDGenerator) IDGenerator {
	if generator == nil {
		return uuidGenerator{}
	}
	return generator
}
