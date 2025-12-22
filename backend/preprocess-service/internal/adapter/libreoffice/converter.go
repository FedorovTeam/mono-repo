package libreoffice

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"go.uber.org/zap"

	"preprocess-service/internal/usecase/ports"
)

type ExecConverter struct {
	binaryPath string
	logger     *zap.Logger
}

const maxOutputLength = 4 * 1024

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

func (c *ExecConverter) Convert(ctx context.Context, sourcePath, outDir string) (ports.ConvertResult, error) {
	start := time.Now()
	logger := c.logger.With(
		zap.String("source_path", sourcePath),
		zap.String("output_dir", outDir),
	)
	logger.Info("starting libreoffice conversion")

	cmd := exec.CommandContext(ctx, c.binaryPath, "--headless", "--convert-to", "docx", "--outdir", outDir, sourcePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	duration := time.Since(start)
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}

	stdoutOut := truncateOutput(stdout.String(), maxOutputLength)
	stderrOut := truncateOutput(stderr.String(), maxOutputLength)
	result := ports.ConvertResult{
		ExitCode: exitCode,
		Output:   truncateOutput(combineOutput(stdoutOut, stderrOut), maxOutputLength),
		Stdout:   stdoutOut,
		Stderr:   stderrOut,
		Duration: duration,
	}

	if err != nil {
		logger.Error("libreoffice conversion failed",
			zap.Duration("duration", duration),
			zap.String("status", "error"),
			zap.Int("exit_code", exitCode),
			zap.String("output", result.Output),
			zap.String("stdout", result.Stdout),
			zap.String("stderr", result.Stderr),
			zap.Error(err),
		)
		return result, fmt.Errorf("libreoffice error (exit_code=%d): %w", exitCode, err)
	}

	logger.Info("libreoffice conversion finished",
		zap.Duration("duration", duration),
		zap.String("status", "success"),
		zap.Int("exit_code", exitCode),
		zap.String("output", result.Output),
		zap.String("stdout", result.Stdout),
		zap.String("stderr", result.Stderr),
	)
	return result, nil
}

func truncateOutput(output string, limit int) string {
	trimmed := strings.TrimSpace(output)
	if len(trimmed) <= limit {
		return trimmed
	}

	return trimmed[:limit] + "..."
}

func combineOutput(stdout, stderr string) string {
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return strings.Join([]string{stdout, stderr}, "\n")
	}
}
