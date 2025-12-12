package usecase

import (
	"strings"
	"sync"

	"backend-github-publisher-service/internal/domain"
)

type MemoryIdempotencyStore struct {
	mu    sync.RWMutex
	items map[string]*domain.PublishResult
}

func NewMemoryIdempotencyStore() *MemoryIdempotencyStore {
	return &MemoryIdempotencyStore{items: make(map[string]*domain.PublishResult)}
}

func (s *MemoryIdempotencyStore) Get(key string) (*domain.PublishResult, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	resp, ok := s.items[key]
	if !ok {
		return nil, false
	}
	return resp, true
}

func (s *MemoryIdempotencyStore) Save(key string, result *domain.PublishResult) {
	key = strings.TrimSpace(key)
	if key == "" || result == nil || result.Status != domain.PublishStatusSuccess {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.items[key] = result
}
