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

func TestMergeMarkdownWithTablesSkipsWhenDoclingEmpty(t *testing.T) {
	base := ""
	tables := "<!-- tables:start -->\n| a | b |\n|---|---|\n| 1 | 2 |\n<!-- tables:end -->"

	result, err := mergeMarkdownWithTablesWithOptions(base, tables, mergeOptions{
		TableMetadata: domain.TableMetadata{TablesCount: 1},
		Settings: domain.MergeSettings{
			MaxTablesShare:      3,
			SkipRatioThreshold:  3,
			SkipWhenDoclingZero: true,
			LogMode:             domain.MergeLogModeSilent,
		},
		DocumentID: "doc-empty",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TablesMerged {
		t.Fatalf("expected tables to be skipped when docling result is empty")
	}
	if result.SkipReason == "" {
		t.Fatalf("expected skip reason to be returned")
	}
	if !strings.Contains(result.SkipReason, "docling markdown is empty") {
		t.Fatalf("unexpected skip reason: %s", result.SkipReason)
	}
}

func TestMergeMarkdownWithTablesSkipsOnRatioThreshold(t *testing.T) {
	base := "Короткий текст"
	tables := "<!-- tables:start -->\n| a | b |\n|---|---|\n| 1 | 2 |\n<!-- tables:end -->"

	result, err := mergeMarkdownWithTablesWithOptions(base, tables, mergeOptions{
		TableMetadata: domain.TableMetadata{TablesCount: 1},
		Settings: domain.MergeSettings{
			MaxTablesShare:     0.5,
			SkipRatioThreshold: 0.5,
			LogMode:            domain.MergeLogModeSilent,
		},
		DocumentID: "doc-ratio",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.TablesMerged {
		t.Fatalf("expected tables to be skipped when ratio exceeds threshold")
	}
	if result.SkipReason == "" {
		t.Fatalf("expected skip reason for ratio threshold")
	}
	if !strings.Contains(result.SkipReason, "exceeds threshold") {
		t.Fatalf("unexpected skip reason: %s", result.SkipReason)
	}
}
