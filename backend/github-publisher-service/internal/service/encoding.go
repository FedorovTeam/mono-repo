package service

import (
	"backend-github-publisher-service/internal/charsetautodetect"
	"bytes"
	"fmt"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	xunicode "golang.org/x/text/encoding/unicode"
)

func convertToUTF8(data []byte) (string, error) {
	if utf8.Valid(data) {
		return string(data), nil
	}

	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		trimmed := data[3:]
		if utf8.Valid(trimmed) {
			return string(trimmed), nil
		}
	}

	if len(data) >= 2 {
		switch {
		case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
			decoded, err := xunicode.UTF16(xunicode.BigEndian, xunicode.ExpectBOM).NewDecoder().Bytes(data)
			if err == nil && utf8.Valid(decoded) {
				return string(decoded), nil
			}
		case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
			decoded, err := xunicode.UTF16(xunicode.LittleEndian, xunicode.ExpectBOM).NewDecoder().Bytes(data)
			if err == nil && utf8.Valid(decoded) {
				return string(decoded), nil
			}
		}
	}

	enc, err := charsetautodetect.DetectEncoding(data)
	if err != nil {
		return "", fmt.Errorf("content is not valid UTF-8 and encoding cannot be detected: %w", err)
	}

	decoded, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		return "", fmt.Errorf("convert from %s to utf-8: %w", encodingName(enc), err)
	}
	if utf8.Valid(decoded) {
		return string(decoded), nil
	}

	return "", fmt.Errorf("decoded content from %s is not valid UTF-8", encodingName(enc))
}

func encodingName(enc encoding.Encoding) string {
	if namer, ok := enc.(interface{ String() string }); ok {
		return namer.String()
	}
	return fmt.Sprintf("%T", enc)
}
