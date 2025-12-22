package libreoffice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestExecConverterLogsSuccess(t *testing.T) {
	t.Helper()

	core, logs := observer.New(zap.InfoLevel)
	converter, err := NewExecConverter("/bin/echo", zap.New(core))
	if err != nil {
		t.Fatalf("init converter: %v", err)
	}

	sourcePath := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(sourcePath, []byte("content"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	outDir := t.TempDir()
	if err := converter.Convert(context.Background(), sourcePath, outDir); err != nil {
		t.Fatalf("convert returned error: %v", err)
	}

	if logs.Len() == 0 {
		t.Fatalf("expected logger to be called during convert")
	}
}

func TestExecConverterLogsFailure(t *testing.T) {
	t.Helper()

	scriptPath := filepath.Join(t.TempDir(), "fail.sh")
	script := "#!/bin/sh\necho stdout\n echo stderr 1>&2\n exit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	core, logs := observer.New(zap.InfoLevel)
	converter, err := NewExecConverter(scriptPath, zap.New(core))
	if err != nil {
		t.Fatalf("init converter: %v", err)
	}

	outDir := t.TempDir()
	err = converter.Convert(context.Background(), filepath.Join(outDir, "input.doc"), outDir)
	if err == nil {
		t.Fatalf("expected convert to fail")
	}

	foundOutput := false
	for _, log := range logs.All() {
		if log.Message == "libreoffice conversion failed" {
			if output, ok := log.ContextMap()["output"].(string); ok && strings.Contains(output, "stdout") {
				foundOutput = true
			}
		}
	}

	if !foundOutput {
		t.Fatalf("expected converter to log combined output on failure")
	}
}
