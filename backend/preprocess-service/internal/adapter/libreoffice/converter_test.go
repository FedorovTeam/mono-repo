package libreoffice

import (
	"context"
	"fmt"
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
	result, err := converter.Convert(context.Background(), sourcePath, outDir)
	if err != nil {
		t.Fatalf("convert returned error: %v", err)
	}

	if logs.Len() == 0 {
		t.Fatalf("expected logger to be called during convert")
	}

	if result.ExitCode != 0 {
		t.Fatalf("expected zero exit code, got %d", result.ExitCode)
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
	result, err := converter.Convert(context.Background(), filepath.Join(outDir, "input.doc"), outDir)
	if err == nil {
		t.Fatalf("expected convert to fail")
	}

	foundOutput := false
	for _, log := range logs.All() {
		if log.Message == "libreoffice conversion failed" {
			ctx := log.ContextMap()
			output, okOutput := ctx["output"].(string)
			stdout, okStdout := ctx["stdout"].(string)
			stderr, okStderr := ctx["stderr"].(string)
			foundOutput = (okOutput && strings.Contains(output, "stdout") && strings.Contains(output, "stderr")) ||
				(okStdout && okStderr && strings.Contains(stdout, "stdout") && strings.Contains(stderr, "stderr"))
		}
	}

	if !foundOutput {
		t.Fatalf("expected converter to log combined output on failure")
	}

	if result.ExitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Output, "stdout") || !strings.Contains(result.Output, "stderr") {
		t.Fatalf("expected result output to contain stdout and stderr, got %+v", result)
	}
	if !strings.Contains(result.Stdout, "stdout") || !strings.Contains(result.Stderr, "stderr") {
		t.Fatalf("expected result to contain stdout and stderr, got %+v", result)
	}
}

func TestExecConverterTruncatesOutput(t *testing.T) {
	t.Helper()

	longLine := strings.Repeat("a", 5000)
	scriptPath := filepath.Join(t.TempDir(), "long.sh")
	script := fmt.Sprintf("#!/bin/sh\necho %s\n echo %s 1>&2\n exit 1\n", longLine, longLine)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	converter, err := NewExecConverter(scriptPath, zap.NewExample())
	if err != nil {
		t.Fatalf("init converter: %v", err)
	}

	outDir := t.TempDir()
	result, err := converter.Convert(context.Background(), filepath.Join(outDir, "input.doc"), outDir)
	if err == nil {
		t.Fatalf("expected convert to fail")
	}

	for name, out := range map[string]string{
		"output": result.Output,
		"stdout": result.Stdout,
		"stderr": result.Stderr,
	} {
		if len(out) > maxOutputLength+3 {
			t.Fatalf("%s was not truncated, length=%d", name, len(out))
		}
		if strings.Contains(out, longLine) {
			t.Fatalf("expected %s to be trimmed, got %s", name, out)
		}
	}
}
