// tools/page-praser/file_name_formatter.go
package main

import (
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

func filenameFromHeaders(resp *http.Response, fileURL string) string {
	cd := resp.Header.Get("Content-Disposition")
	if cd != "" {
		_, params, err := mime.ParseMediaType(cd)
		if err == nil {
			// 1) Приоритет: filename* (RFC 5987) — обычно это правильное UTF-8 имя
			if v, ok := params["filename*"]; ok && v != "" {
				name := decodeRFC5987(v)
				name = sanitizeFilename(name)
				if name != "" {
					return name
				}
			}
			// 2) Запасной вариант: filename
			if v, ok := params["filename"]; ok && v != "" {
				name := sanitizeFilename(v)
				if name != "" {
					return name
				}
			}
		}
	}

	// 3) Последний запасной вариант: из URL
	u, _ := url.Parse(fileURL)
	base := filepath.Base(u.Path)
	base = sanitizeFilename(base)
	if base == "" || base == "." || base == "/" {
		return "download.bin"
	}
	return base
}

// decodeRFC5987 декодирует строки вида: UTF-8”11%20%D0%B8%D1%8E%D0%BD%D1%8F%202025%20...
func decodeRFC5987(v string) string {
	// формат: charset''percent-encoded
	parts := strings.SplitN(v, "''", 2)
	if len(parts) == 2 {
		enc := parts[1]
		if s, err := url.QueryUnescape(enc); err == nil {
			return s
		}
	}
	// если почему-то пришло просто percent-encoded
	if s, err := url.QueryUnescape(v); err == nil {
		return s
	}
	return v
}

var badChars = regexp.MustCompile(`[<>:"/\\|?*\x00-\x1F]`)

func sanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	name = strings.Trim(name, `"'`)
	name = badChars.ReplaceAllString(name, "_")
	// на Windows нельзя точку/пробел в конце
	name = strings.TrimRight(name, ". ")
	return name
}
