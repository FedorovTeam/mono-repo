package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"backend-document-service/internal/domain"
)

func mergeStenogram(baseMarkdown, tablesMarkdown string) (string, error) {
	result, err := mergeMarkdownWithTablesWithOptions(baseMarkdown, tablesMarkdown, mergeOptions{
		TableMetadata: domain.TableMetadata{TablesCount: 1},
		Settings:      domain.MergeSettings{MaxTablesShare: 3, LogMode: domain.MergeLogModeSilent},
	})
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

func TestStenogramma01MergedInline(t *testing.T) {
	basePath := filepath.Join("..", "..", "..", "..", "test", "runs", "stenogramma-04", "base.md")
	tablesPath := filepath.Join("..", "..", "..", "..", "test", "runs", "stenogramma-04", "tables.md")
	base, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("read base markdown: %v", err)
	}
	tables, err := os.ReadFile(tablesPath)
	if err != nil {
		t.Fatalf("read tables markdown: %v", err)
	}

	merged, err := mergeStenogram(string(base), string(tables))
	if err != nil {
		t.Fatalf("merge markdown: %v", err)
	}

	if strings.TrimSpace(merged) == "" {
		t.Fatalf("merged content is empty")
	}
	if strings.Contains(merged, "## Таблицы") {
		t.Fatalf("expected inline placement without appendix header")
	}
	if strings.Count(merged, "<!-- tables:start -->") != 1 || strings.Count(merged, "<!-- tables:end -->") != 1 {
		t.Fatalf("expected single tables marker block, got start=%d end=%d", strings.Count(merged, "<!-- tables:start -->"), strings.Count(merged, "<!-- tables:end -->"))
	}
}

func TestStenogramma04HasSingleInlineTablesBlock(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "test", "result", "stenogramma-04.md"))
	if err != nil {
		t.Fatalf("read stenogramma-04: %v", err)
	}
	merged := string(content)
	if strings.Contains(merged, "## Таблицы") {
		t.Fatalf("expected tables to be inlined without appendix header")
	}
	if strings.Count(merged, "<!-- tables:start -->") != 1 || strings.Count(merged, "<!-- tables:end -->") != 1 {
		t.Fatalf("expected single tables block, got start=%d end=%d", strings.Count(merged, "<!-- tables:start -->"), strings.Count(merged, "<!-- tables:end -->"))
	}
	if strings.Contains(merged, "tables:appended") {
		t.Fatalf("expected placement to be resolved without fallback annotation")
	}
}
