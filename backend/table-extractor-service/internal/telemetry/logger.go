package telemetry

import (
	"go.uber.org/zap"
)

func NewLogger(enabled bool) (*zap.Logger, error) {
	if !enabled {
		return zap.NewNop(), nil
	}
	cfg := zap.NewProductionConfig()
	cfg.Encoding = "json"
	return cfg.Build()
}
