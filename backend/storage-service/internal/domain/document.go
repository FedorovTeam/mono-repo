package domain

// Document описывает запись документа в БД.
type Document struct {
	ID           string
	Status       DocumentStatus
	ActualStatus DocumentStatus
	ErrorReason  string
}

// DocumentStatus фиксирует состояние документа в процессе обработки.
type DocumentStatus string

const (
	DocumentStatusStoredOriginal DocumentStatus = "StoredOriginal"
)
