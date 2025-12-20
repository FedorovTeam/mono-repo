package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"preprocess-service/internal/usecase/ports"
)

type FSManager struct {
	workingDir string
	outputDir  string
}

func NewFSManager(workingDir, outputDir string) (*FSManager, error) {
	if workingDir == "" {
		workingDir = filepath.Join(os.TempDir(), "preprocess")
	}
	if outputDir == "" {
		outputDir = filepath.Join(workingDir, "output")
	}

	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare working dir: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return nil, fmt.Errorf("prepare output dir: %w", err)
	}

	return &FSManager{
		workingDir: workingDir,
		outputDir:  outputDir,
	}, nil
}

func (m *FSManager) Prepare(jobID string) (ports.Workspace, error) {
	jobComponent, err := sanitizeName(jobID)
	if err != nil {
		return nil, fmt.Errorf("sanitize job id: %w", err)
	}

	tempDir, err := os.MkdirTemp(m.workingDir, fmt.Sprintf("job-%s-", jobComponent))
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}

	jobOutputDir := filepath.Join(m.outputDir, jobComponent)
	if err := os.MkdirAll(jobOutputDir, 0o755); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("prepare output dir: %w", err)
	}

	return &fsWorkspace{
		tempDir:   tempDir,
		outputDir: jobOutputDir,
	}, nil
}

type fsWorkspace struct {
	tempDir   string
	outputDir string
}

func (w *fsWorkspace) SourcePath(sourceKey string) (string, error) {
	filename, err := sanitizeFileName(sourceKey)
	if err != nil {
		return "", err
	}

	return filepath.Join(w.tempDir, filename), nil
}

func (w *fsWorkspace) OutputPath(sourceKey string) (string, error) {
	filename, err := sanitizeFileName(sourceKey)
	if err != nil {
		return "", err
	}

	base := strings.TrimSuffix(filename, filepath.Ext(filename))
	if base == "" {
		return "", ports.ErrInvalidObjectKey
	}

	if err := os.MkdirAll(w.outputDir, 0o755); err != nil {
		return "", fmt.Errorf("ensure output dir: %w", err)
	}

	return filepath.Join(w.outputDir, base+".docx"), nil
}

func (w *fsWorkspace) OutputDir() string {
	return w.outputDir
}

func (w *fsWorkspace) EnsureExists(path string) error {
	_, err := os.Stat(path)
	return err
}

func (w *fsWorkspace) Cleanup() error {
	return os.RemoveAll(w.tempDir)
}

func sanitizeFileName(key string) (string, error) {
	base := filepath.Base(key)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", ports.ErrInvalidObjectKey
	}

	normalized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return r
		case r == '.' || r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, base)

	normalized = strings.Trim(normalized, "._-")
	if normalized == "" {
		return "", ports.ErrInvalidObjectKey
	}

	return normalized, nil
}

func sanitizeName(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", ports.ErrInvalidObjectKey
	}

	sanitized := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return r
		case r == '-' || r == '_':
			return r
		default:
			return '_'
		}
	}, value)

	sanitized = strings.Trim(sanitized, "._-")
	if sanitized == "" {
		return "", errors.New("empty sanitized value")
	}

	return sanitized, nil
}
