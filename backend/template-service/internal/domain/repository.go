package domain

// DemoRepository определяет интерфейс работы с DemoEntity

type DemoRepository interface {
	Save(entity *DemoEntity) error
	FindByID(id string) (*DemoEntity, error)
}

// FileStorage определяет интерфейс работы с файлами

type FileStorage interface {
	UploadFile(bucketType, prefix, entityPath, filePath string, t int64, region, mime string) (bucket, key, etag string, size int64, err error)
	GeneratePresignedURL(bucket, objectName string, expiry int64) (string, error)
}

// EventPublisher определяет интерфейс публикации событий

type EventPublisher interface {
	PublishDemoEvent(event DemoEvent, headers map[string]string) error
}

// EventSubscriber определяет интерфейс подписки на события

type EventSubscriber interface {
	Subscribe(subject string, handler func(data []byte, headers map[string]string) error) error
}

// Cache определяет интерфейс кэша (например, для presigned URL)
type Cache interface {
	Set(key, value string, ttl int64) error
	Get(key string) (string, error)
}
