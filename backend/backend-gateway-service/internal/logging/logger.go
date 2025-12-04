package logging

import (
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New создаёт zap-логгер с заданным уровнем.
func New(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.Encoding = "console"
	cfg.EncoderConfig.TimeKey = "timestamp"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	if level != "" {
		if err := cfg.Level.UnmarshalText([]byte(strings.ToLower(level))); err != nil {
			return nil, err
		}
	}

	return cfg.Build()
}
