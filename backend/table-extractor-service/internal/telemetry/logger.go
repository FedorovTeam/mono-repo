package telemetry

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func NewLogger(enabled bool, level string) (*zap.Logger, error) {
	if !enabled {
		return zap.NewNop(), nil
	}

	parsedLevel, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, fmt.Errorf("invalid log level %q: %w", level, err)
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(parsedLevel)
	cfg.Encoding = "json"

	return cfg.Build()
}
