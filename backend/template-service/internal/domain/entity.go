package domain

import "time"

// DemoEntity — доменная сущность, агрегат
// Value — произвольное значение, BucketID и RegionID — value objects
// ID — уникальный идентификатор
// UserID — владелец

type DemoEntity struct {
	ID        string
	UserID    string
	BucketID  BucketID
	RegionID  RegionID
	Value     string
	Bucket    string
	CreatedAt time.Time
}

// DemoEvent — доменное событие
// Используется для публикации в NATS

type DemoEvent struct {
	ID        string
	Action    string
	TraceID   string
	Timestamp int64
}

// Value objects

type BucketID string

type RegionID string
