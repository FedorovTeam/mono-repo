// tools/page-praser/ensure_extension.go
package main

import (
	"mime"
	"path/filepath"
	"strings"
)

func ensureExtensionByContentType(filename string, contentType string) string {
	ext := filepath.Ext(filename)
	if ext != "" && ext != "." {
		return filename
	}

	// Content-Type часто приходит с charset: "application/pdf; charset=utf-8"
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	}
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))

	// Явный маппинг самых частых типов
	ctToExt := map[string]string{
		"application/pdf":    ".pdf",
		"application/msword": ".doc",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
		"application/vnd.ms-excel": ".xls",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":         ".xlsx",
		"application/vnd.ms-powerpoint":                                             ".ppt",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation": ".pptx",
		"application/zip":              ".zip",
		"application/x-zip-compressed": ".zip",
		"application/octet-stream":     "", // тут лучше не угадывать
	}

	if e, ok := ctToExt[mediaType]; ok && e != "" {
		return filename + e
	}

	// Попробуем стандартный механизм mime.ExtensionsByType
	if exts, err := mime.ExtensionsByType(mediaType); err == nil && len(exts) > 0 {
		// Берём первое “самое стандартное”
		return filename + exts[0]
	}

	return filename
}
