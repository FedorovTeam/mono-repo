package converter

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"go.uber.org/zap"
)

type loggingTransport struct {
	base   http.RoundTripper
	logger *zap.Logger
}

func newLoggingTransport(base http.RoundTripper, logger *zap.Logger) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &loggingTransport{base: base, logger: logger}
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Чтобы можно было прочитать Body и при этом не сломать отправку — буферизуем.
	var bodyCopy []byte
	if req.Body != nil {
		bodyCopy, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(bodyCopy))
	}

	fields := []zap.Field{
		zap.String("method", req.Method),
		zap.String("url", req.URL.String()),
		zap.Any("headers", sanitizeHeaders(req.Header)),
	}

	// Попробуем красиво распарсить тело:
	ct := req.Header.Get("Content-Type")
	switch {
	case strings.HasPrefix(ct, "application/json"):
		fields = append(fields, zap.String("body", snippet(string(bodyCopy), 4096)))

	case strings.HasPrefix(ct, "multipart/form-data"):
		// Вытащим только form-поля (без файла), чтобы увидеть опции Docling
		opts, files := extractMultipartSummary(ct, bodyCopy)
		fields = append(fields,
			zap.Any("multipart_fields", opts),
			zap.Any("multipart_files", files),
		)

	default:
		if len(bodyCopy) > 0 {
			fields = append(fields, zap.String("body", snippet(string(bodyCopy), 2048)))
		}
	}

	t.logger.Info("docling http request", fields...)

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		t.logger.Warn("docling http request failed", zap.Error(err))
		return nil, err
	}
	return resp, nil
}

func sanitizeHeaders(h http.Header) map[string][]string {
	out := map[string][]string{}
	for k, v := range h {
		kl := strings.ToLower(k)
		// Спрячем ключи/секреты
		if kl == strings.ToLower(apiKeyHeader) || kl == "authorization" {
			out[k] = []string{"***"}
			continue
		}
		out[k] = v
	}
	return out
}

func snippet(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

type multipartFileInfo struct {
	FieldName string `json:"field"`
	FileName  string `json:"filename"`
	Size      int    `json:"size"`
}

func extractMultipartSummary(contentType string, body []byte) (map[string][]string, []multipartFileInfo) {
	outFields := map[string][]string{}
	var outFiles []multipartFileInfo

	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return outFields, outFiles
	}
	boundary := params["boundary"]
	if boundary == "" {
		return outFields, outFiles
	}

	r := multipart.NewReader(bytes.NewReader(body), boundary)
	for {
		p, err := r.NextPart()
		if err != nil {
			break
		}

		disp := p.Header.Get("Content-Disposition")
		_, dparams, _ := mime.ParseMediaType(disp)
		name := dparams["name"]
		filename := dparams["filename"]

		b, _ := io.ReadAll(p)
		if filename != "" {
			outFiles = append(outFiles, multipartFileInfo{
				FieldName: name,
				FileName:  filename,
				Size:      len(b),
			})
			continue
		}

		// form field
		val := strings.TrimSpace(string(b))
		if name != "" {
			outFields[name] = append(outFields[name], val)
		}
	}

	return outFields, outFiles
}
