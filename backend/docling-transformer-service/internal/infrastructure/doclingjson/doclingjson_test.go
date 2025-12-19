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

func TestCalculateVotingFillRate(t *testing.T) {
	tables := []Table{
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "№"}, {Text: "ФИО"}}},
				{Cells: []TableCell{{Text: "1"}, {Text: "Иванов Иван"}}},
				{Cells: []TableCell{{Text: "2"}, {Text: ""}}},
			},
		},
	}

	fillRate := CalculateVotingFillRate(tables)
	if fillRate != 0.5 {
		t.Fatalf("expected 0.5 fill rate, got %f", fillRate)
	}
}
