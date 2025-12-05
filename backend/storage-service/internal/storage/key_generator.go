package storage

import (
	"fmt"
	"strings"
	"time"
)

// TimeProvider позволяет подменять источник времени в тестах.
type TimeProvider func() time.Time

// ObjectKeyGenerator формирует ключи объектов в MinIO по заданному формату.
type ObjectKeyGenerator struct {
	now TimeProvider
}

// NewObjectKeyGenerator создает генератор ключей с настраиваемым источником времени.
func NewObjectKeyGenerator(now TimeProvider) *ObjectKeyGenerator {
	provider := now
	if provider == nil {
		provider = time.Now
	}

	return &ObjectKeyGenerator{now: provider}
}

// BuildKey формирует ключ вида
// org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>.
func (g *ObjectKeyGenerator) BuildKey(orgSlug, projectSlug, documentID, extension string) (string, error) {
	if orgSlug == "" || projectSlug == "" || documentID == "" {
		return "", fmt.Errorf("orgSlug, projectSlug и documentID должны быть заполнены")
	}

	ext := strings.TrimPrefix(extension, ".")
	if ext == "" {
		return "", fmt.Errorf("extension не должен быть пустым")
	}

	now := g.now()
	return fmt.Sprintf(
		"org/%s/project/%s/year=%04d/month=%02d/day=%02d/%s.%s",
		orgSlug,
		projectSlug,
		now.Year(),
		now.Month(),
		now.Day(),
		documentID,
		ext,
	), nil
}
