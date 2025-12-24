package document

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStenogramma01MergedInline(t *testing.T) {
	basePath := filepath.Join("..", "..", "..", "..", "test", "result", "12-24-2025-11-46-27_files_list", "98bd346c-cba7-4aba-a597-532fbab855a9", "65e531a1-2749-499b-a2ea-ef7cb61e0610.md")
	tablesPath := filepath.Join("..", "..", "..", "..", "test", "result", "12-24-2025-11-46-27_files_list", "98bd346c-cba7-4aba-a597-532fbab855a9", "65e531a1-2749-499b-a2ea-ef7cb61e0610", "tables.md")
	expectedPath := filepath.Join("..", "..", "..", "..", "test", "result", "stenogramma-01.md")

	base, err := os.ReadFile(basePath)
	if err != nil {
		t.Fatalf("read base markdown: %v", err)
	}
	tables, err := os.ReadFile(tablesPath)
	if err != nil {
		t.Fatalf("read tables markdown: %v", err)
	}
	expected, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read expected result: %v", err)
	}

	merged, err := mergeMarkdownWithTables(string(base), string(tables))
	if err != nil {
		t.Fatalf("merge markdown: %v", err)
	}

	if strings.TrimSpace(string(expected)) != strings.TrimSpace(merged) {
		t.Fatalf("unexpected merged content for stenogramma-01")
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
