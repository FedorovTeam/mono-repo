// backend/docling-transformer-service/internal/domain/content_type.go
package domain

import (
	"fmt"
	"strings"
)

const defaultExtension = "docx"

var extensionToContentType = map[string]string{
	"doc":  "application/msword",
	"docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"pdf":  "application/pdf",
}

var contentTypeToExtension = map[string]string{
	"application/msword": "doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/pdf": "pdf",
}

func ResolveContentTypeByExtension(fileExt string) (string, error) {
	normalized := strings.TrimPrefix(strings.ToLower(fileExt), ".")
	if normalized == "" {
		normalized = defaultExtension
	}

	contentType, ok := extensionToContentType[normalized]
	if !ok {
		return "", fmt.Errorf("unsupported file extension: %s", normalized)
	}

	return contentType, nil
}

func ResolveExtensionByContentType(contentType string) (string, bool) {
	ext, ok := contentTypeToExtension[strings.ToLower(contentType)]
	return ext, ok
}
