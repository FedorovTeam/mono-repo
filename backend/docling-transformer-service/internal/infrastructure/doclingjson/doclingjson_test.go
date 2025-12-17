package doclingjson

import (
	"strings"
	"testing"
)

func TestRenderMarkdownReplacesMarks(t *testing.T) {
	jsonContent := map[string]any{
		"tables": []any{
			map[string]any{
				"id": "table-1",
				"rows": []any{
					map[string]any{"cells": []any{map[string]any{"text": "H1"}, map[string]any{"text": "H2"}}},
					map[string]any{"cells": []any{map[string]any{"text": "C1"}, map[string]any{"text": "C2"}}},
				},
			},
			map[string]any{
				"name":     "table-2",
				"markdown": "|A|B|\n|-|-|\n|1|2|",
			},
		},
		"marks": []any{
			map[string]any{"mark": "{{table-1}}", "target_id": "table-1"},
			map[string]any{"mark": "{{TABLE-2}}", "target_id": "TABLE-2"},
		},
	}

	fallback := "Intro\n\n{{table-1}}\n\nOutro"

	got, ok := RenderMarkdown(jsonContent, &fallback)
	if !ok {
		t.Fatalf("expected markdown rendered from JSON content")
	}

	expected := "| H1 | H2 |\n| --- | --- |\n| C1 | C2 |"
	if !strings.Contains(got, expected) {
		t.Fatalf("expected table from rows, got: %s", got)
	}

	if !strings.Contains(got, "|A|B|\n|-|-|\n|1|2|") {
		t.Fatalf("expected markdown table appended, got: %s", got)
	}
}

func TestRenderMarkdownWithoutFallback(t *testing.T) {
	jsonContent := map[string]any{
		"tables": []any{
			map[string]any{
				"id":       "table-1",
				"markdown": "|X|Y|\n|-|-|\n|7|8|",
			},
		},
		"marks": []any{
			map[string]any{"mark": "TABLE-1"},
		},
	}

	got, ok := RenderMarkdown(jsonContent, nil)
	if !ok {
		t.Fatalf("expected markdown rendered from JSON content")
	}

	if got != "|X|Y|\n|-|-|\n|7|8|" {
		t.Fatalf("unexpected markdown: %s", got)
	}
}

func TestParseAcceptsStringPayload(t *testing.T) {
	payload := `{"tables":[{"id":"t1","markdown":"|a|"}]}`

	doc, err := Parse(payload)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if len(doc.Tables) != 1 {
		t.Fatalf("expected single table, got %d", len(doc.Tables))
	}

	if normalizeIdentifier(doc.Tables[0].ID) != "t1" {
		t.Fatalf("unexpected table id: %s", doc.Tables[0].ID)
	}
}

func TestMergeVotingTablesFillsEmptyCells(t *testing.T) {
	passA := []Table{
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "№"}, {Text: "ФИО"}, {Text: "Голоса"}}},
				{Cells: []TableCell{{Text: "1"}, {Text: "Иванов Иван"}, {Text: ""}}},
			},
		},
	}
	passB := ParseMarkdownTables("|№|ФИО|Голоса|\n|-|-|-|\n|1|Иванов Иван|42|")
	t.Logf("parsed pass B: %+v", passB)

	if len(passB) == 0 {
		t.Fatalf("expected table parsed from markdown")
	}

	if numA, fioA := detectVotingColumns(passA[0]); numA == -1 || fioA == -1 {
		t.Fatalf("expected voting columns in pass A, got %d %d", numA, fioA)
	}
	if numB, fioB := detectVotingColumns(passB[0]); numB == -1 || fioB == -1 {
		t.Fatalf("expected voting columns in pass B, got %d %d", numB, fioB)
	}

	merged, stats := MergeVotingTables(passA, passB, 0.8)

	if stats.MergedRows != 1 || stats.MatchedRows != 1 || stats.TotalRows != 1 {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	if got := merged[0].Rows[1].Cells[2].Text; strings.TrimSpace(got) != "42" {
		t.Fatalf("expected merged value from pass B, got %s", got)
	}
}
