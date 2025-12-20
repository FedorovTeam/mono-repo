package testutil

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/mono-repo/docling-transformer-service/internal/domain"
)

func TestSaveResultCreatesMarkdownFileInEnvDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(docOutputDirEnv, dir)

	path := SaveResult(t, sampleResult(), "docs/result")

	if filepath.Dir(path) != filepath.Join(dir, "docs") {
		t.Fatalf("ожидалась директория %s, получено %s", filepath.Join(dir, "docs"), filepath.Dir(path))
	}

	if filepath.Ext(path) != ".md" {
		t.Fatalf("ожидалось расширение .md, получено %s", filepath.Ext(path))
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("не удалось прочитать файл %s: %v", path, err)
	}

	if string(content) != "# result" {
		t.Fatalf("неожиданное содержимое: %s", string(content))
	}
}

func TestSaveResultPrefersFlagOverEnv(t *testing.T) {
	envDir := t.TempDir()
	flagDir := t.TempDir()

	t.Setenv(docOutputDirEnv, envDir)

	restore := setFlagValue(t, docOutputDirFlagName, flagDir)
	defer restore()

	path := SaveResult(t, sampleResult(), "out")

	if filepath.Dir(path) != flagDir {
		t.Fatalf("ожидалось использование флага %s, но файл записан в %s", flagDir, filepath.Dir(path))
	}
}

func TestSaveResultUsesTempDirByDefault(t *testing.T) {
	path := SaveResult(t, sampleResult(), "default")

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("файл не создан: %v", err)
	}

	if filepath.Ext(path) != ".md" {
		t.Fatalf("ожидалось расширение .md, получено %s", filepath.Ext(path))
	}
}

func sampleResult() domain.ConvertResult {
	return domain.ConvertResult{Content: []byte("# result"), MediaType: "text/markdown", Charset: "utf-8"}
}

func setFlagValue(t *testing.T, name, value string) func() {
	t.Helper()

	flagObj := flag.Lookup(name)
	if flagObj == nil {
		t.Fatalf("флаг %s не найден", name)
	}

	original := flagObj.Value.String()
	if err := flag.Set(name, value); err != nil {
		t.Fatalf("не удалось установить флаг %s: %v", name, err)
	}

	return func() {
		_ = flag.Set(name, original)
	}
}
