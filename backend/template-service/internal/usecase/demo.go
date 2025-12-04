package usecase

import (
	"fmt"
	"io/ioutil"
	"os"
	"template-service/internal/domain"
	"time"

	"github.com/google/uuid"
)

type DemoUseCase struct {
	Repo       domain.DemoRepository
	Storage    domain.FileStorage
	Publisher  domain.EventPublisher
	Subscriber domain.EventSubscriber
	Cache      domain.Cache
}

// NewDemoUseCase внедрение зависимостей через конструктор
func NewDemoUseCase(repo domain.DemoRepository, storage domain.FileStorage, publisher domain.EventPublisher, subscriber domain.EventSubscriber, cache domain.Cache) *DemoUseCase {
	return &DemoUseCase{
		Repo:       repo,
		Storage:    storage,
		Publisher:  publisher,
		Subscriber: subscriber,
		Cache:      cache,
	}
}

// CreateDemo создает DemoEntity, загружает файл, публикует событие
func (uc *DemoUseCase) CreateDemo(userID, value, filePath, bucketType, prefix, entityPath, region string, t time.Time, traceID string) (*domain.DemoEntity, error) {
	id := userID // или сгенерировать отдельно, если нужно

	// 1. Создать временный demo-файл
	tmpFile, err := ioutil.TempFile("", "demo-*.txt")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmpFile.Name())
	_, err = tmpFile.WriteString(value)
	if err != nil {
		tmpFile.Close()
		return nil, err
	}
	tmpFile.Close()

	// 2. Сформировать objectName и mime
	objectName := fmt.Sprintf("txt/%s.txt", id)
	mime := "text/plain"

	// 3. Загрузить файл в MinIO
	bucket, key, _, _, err := uc.Storage.UploadFile(bucketType, prefix, objectName, tmpFile.Name(), t.Unix(), region, mime)
	if err != nil {
		return nil, err
	}

	// 4. Сформировать DemoEntity с актуальным ключом
	entity := &domain.DemoEntity{
		ID:        id,
		UserID:    userID,
		BucketID:  domain.BucketID(userID),
		RegionID:  domain.RegionID(region),
		Value:     key,    // ключ файла в MinIO
		Bucket:    bucket, // бакет в minio
		CreatedAt: t,
	}

	// 5. Сохранить entity в БД
	if err := uc.Repo.Save(entity); err != nil {
		return nil, err
	}

	// 6. Публикуем событие в NATS
	event := domain.DemoEvent{
		ID:        entity.ID,
		Action:    "created",
		TraceID:   traceID,
		Timestamp: t.Unix(),
	}
	headers := map[string]string{"trace_id": traceID, "correlation_id": entity.ID}
	if err := uc.Publisher.PublishDemoEvent(event, headers); err != nil {
		return nil, err
	}
	fmt.Println("published: demo event saved")
	return entity, nil
}

// EnsureStream (заглушка, если нужно инициализировать стрим/subject)
func (uc *DemoUseCase) EnsureStream(subject, stream string) error {
	// Обычно делегируется инфраструктуре через отдельный сервис
	return nil
}

// EnsureStreamJetStream вызывает EnsureStream у EventPublisher
func (uc *DemoUseCase) EnsureStreamJetStream(streamName string, subjects []string) error {
	if ep, ok := uc.Publisher.(interface{ EnsureStream(string, []string) error }); ok {
		return ep.EnsureStream(streamName, subjects)
	}
	return nil
}

// SubscribeEvent подписка на события
func (uc *DemoUseCase) SubscribeEvent(subject string, handler func(data []byte, headers map[string]string) error) error {
	return uc.Subscriber.Subscribe(subject, handler)
}

// GeneratePresignedURL делегирует в FileStorage
func (uc *DemoUseCase) GeneratePresignedURL(bucket, objectName string, expiry int64) (string, error) {
	return uc.Storage.GeneratePresignedURL(bucket, objectName, expiry)
}

// SetPresignedURL делегирует в Cache
func (uc *DemoUseCase) SetPresignedURL(key, value string, ttl int64) error {
	return uc.Cache.Set(key, value, ttl)
}

// GetDemo получает DemoEntity по id, формирует presigned ссылку и возвращает DTO
func (uc *DemoUseCase) GetDemo(id string) (*domain.DemoEntity, error) {
	entity, err := uc.Repo.FindByID(id)
	if err != nil || entity == nil {
		return nil, err
	}
	// Генерируем presigned URL через uc.Storage
	if entity.BucketID != "" && entity.Value != "" {
		url, err := uc.Storage.GeneratePresignedURL(entity.Bucket, entity.Value, 3600)
		if err == nil {
			entity.Value = url // теперь Value содержит ссылку на скачивание файла
			fmt.Printf("\n[DEMO FILE LINK] Open in browser: %s\n\n", url)
		}
	}
	return entity, nil
}

// GenerateID генерирует уникальный ID (uuid)
func GenerateID() string {
	return uuid.New().String()
}

// generateBucketID генерирует bucketID на основе типа и региона
func generateBucketID(bucketType, region string) string {
	if bucketType == "" {
		bucketType = "demo"
	}
	if region == "" {
		region = "global"
	}
	return bucketType + "-" + region + "-" + uuid.New().String()
}
