package transformations

import (
	"path/filepath"
	"strings"
)

// BuildMarkdownKey формирует имя выходного файла Markdown из исходного пути.
func BuildMarkdownKey(originalKey string) string {
	ext := filepath.Ext(originalKey)
	base := strings.TrimSuffix(originalKey, ext)
	if base == "" {
		return originalKey + ".md"
	}

	return base + ".md"
}

// NormalizeExtension приводит расширение к нижнему регистру без точки.
func NormalizeExtension(value string) string {
	return strings.TrimPrefix(strings.ToLower(value), ".")
}
