package libreoffice

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"
)

type ExecConverter struct {
	binaryPath string
	logger     *zap.Logger
}

func NewExecConverter(binaryPath string, logger *zap.Logger) (*ExecConverter, error) {
	if strings.TrimSpace(binaryPath) == "" {
		return nil, fmt.Errorf("libreoffice path is required")
	}

	if logger == nil {
		logger = zap.NewNop()
	}

	return &ExecConverter{
		binaryPath: binaryPath,
		logger:     logger,
	}, nil
}

func (c *ExecConverter) Convert(ctx context.Context, sourcePath, outDir string) error {
	start := time.Now()
	logger := c.logger.With(
		zap.String("source_path", sourcePath),
		zap.String("output_dir", outDir),
	)
	logger.Info("starting libreoffice conversion")

	cmd := exec.CommandContext(ctx, c.binaryPath, "--headless", "--convert-to", "docx", "--outdir", outDir, sourcePath)

	output, err := cmd.CombinedOutput()
	logOutput := truncateOutput(string(output), 1024)
	duration := time.Since(start)
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	if err != nil {
		logger.Error("libreoffice conversion failed",
			zap.Duration("duration", duration),
			zap.String("status", "error"),
			zap.Int("exit_code", exitCode),
			zap.String("output", logOutput),
			zap.Error(err),
		)
		return fmt.Errorf("libreoffice error: %w; output: %s", err, strings.TrimSpace(string(output)))
	}

	logger.Info("libreoffice conversion finished",
		zap.Duration("duration", duration),
		zap.String("status", "success"),
		zap.Int("exit_code", exitCode),
		zap.String("output", logOutput),
	)
	return nil
}

func truncateOutput(output string, limit int) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) <= limit {
		return trimmed
	}

	return trimmed[:limit] + "..."
}
