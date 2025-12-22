package logger

import (
	"backend-github-publisher-service/pkg/config"
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Init настраивает zap-логгер на основе конфигурации и устанавливает его глобально.
func Init(cfg config.LoggingConfig) (*zap.Logger, *zap.SugaredLogger, func() error, error) {
	level := zap.NewAtomicLevel()
	if err := level.UnmarshalText([]byte(cfg.Level)); err != nil {
		return nil, nil, nil, fmt.Errorf("parse logging.level: %w", err)
	}

	zapCfg := zap.NewProductionConfig()
	zapCfg.Level = level
	zapCfg.Encoding = cfg.Encoding
	zapCfg.Development = cfg.Development
	if cfg.Development {
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.Level = level
		zapCfg.Encoding = cfg.Encoding
	}

	zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := zapCfg.Build()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("build zap logger: %w", err)
	}

	zap.ReplaceGlobals(logger)

	return logger, logger.Sugar(), logger.Sync, nil
}
