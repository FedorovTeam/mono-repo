package document

import (
	"strings"
	"testing"

	"backend-document-service/internal/domain"
)

func mergeMarkdownForActivities(baseMarkdown, tablesMarkdown string) (string, error) {
	result, err := mergeMarkdownWithTablesWithOptions(baseMarkdown, tablesMarkdown, mergeOptions{
		TableMetadata: domain.TableMetadata{TablesCount: 1},
		Settings:      domain.MergeSettings{MaxTablesShare: 3, LogMode: domain.MergeLogModeSilent},
	})
	if err != nil {
		return "", err
	}
	return result.Content, nil
}

func TestMergeMarkdownWithTablesPrefersMarkedBlocks(t *testing.T) {
	base := "Введение\n\n<!-- tables:start -->\n| old | table |\n<!-- tables:end -->\n\nЗавершение"
	tables := "<!-- tables:start -->\n| new | table |\n<!-- tables:end -->\n\n## Таблицы\n\n| legacy | table |"

	merged, err := mergeMarkdownForActivities(base, tables)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Count(merged, "<!-- tables:start") != 1 || strings.Count(merged, "<!-- tables:end") != 1 {
		t.Fatalf("expected single marker block, got %q", merged)
	}
	if strings.Contains(merged, "legacy | table") {
		t.Fatalf("expected legacy appendix to be ignored, got %q", merged)
	}
	if !strings.Contains(merged, "| new | table |") {
		t.Fatalf("expected new table content to be merged, got %q", merged)
	}
}
