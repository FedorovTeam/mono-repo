package libreoffice

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

type ExecConverter struct {
	binaryPath string
}

func NewExecConverter(binaryPath string) (*ExecConverter, error) {
	if strings.TrimSpace(binaryPath) == "" {
		return nil, fmt.Errorf("libreoffice path is required")
	}

	return &ExecConverter{binaryPath: binaryPath}, nil
}

func (c *ExecConverter) Convert(ctx context.Context, sourcePath, outDir string) error {
	cmd := exec.CommandContext(ctx, c.binaryPath, "--headless", "--convert-to", "docx", "--outdir", outDir, sourcePath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("libreoffice error: %w; output: %s", err, strings.TrimSpace(string(output)))
	}

	return nil
}
