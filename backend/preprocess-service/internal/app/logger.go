package app

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"preprocess-service/internal/config"
)

const (
	logFormatConsole = "console"
	logFormatJSON    = "json"
)

// NewLogger создаёт zap-логгер с учётом уровня и формата из конфига.
func NewLogger(cfg config.LoggingConfig) (*zap.Logger, *zap.SugaredLogger, error) {
	level, err := parseLogLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}

	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderCfg.EncodeDuration = zapcore.StringDurationEncoder
	encoderCfg.EncodeLevel = func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
		if l == zapcore.Level(-2) {
			enc.AppendString("TRACE")
			return
		}
		zapcore.CapitalLevelEncoder(l, enc)
	}

	encoder, err := encoderByFormat(cfg.Format, encoderCfg)
	if err != nil {
		return nil, nil, err
	}

	core := zapcore.NewCore(
		encoder,
		zapcore.Lock(os.Stdout),
		level,
	)

	logger := zap.New(core, zap.AddCaller())
	return logger, logger.Sugar(), nil
}

func parseLogLevel(level string) (zapcore.Level, error) {
	normalized := strings.TrimSpace(strings.ToLower(level))
	switch normalized {
	case "", "info":
		return zapcore.InfoLevel, nil
	case "trace":
		return zapcore.Level(-2), nil
	case "debug":
		return zapcore.DebugLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	case "dpanic":
		return zapcore.DPanicLevel, nil
	case "panic":
		return zapcore.PanicLevel, nil
	case "fatal":
		return zapcore.FatalLevel, nil
	default:
		var lvl zapcore.Level
		if err := lvl.Set(level); err != nil {
			return zapcore.InfoLevel, fmt.Errorf("unsupported log level: %s", level)
		}
		return lvl, nil
	}
}

func encoderByFormat(format string, cfg zapcore.EncoderConfig) (zapcore.Encoder, error) {
	normalized := strings.TrimSpace(strings.ToLower(format))
	switch normalized {
	case "", logFormatConsole:
		return zapcore.NewConsoleEncoder(cfg), nil
	case logFormatJSON:
		return zapcore.NewJSONEncoder(cfg), nil
	default:
		return nil, fmt.Errorf("unsupported log format: %s", format)
	}
}
