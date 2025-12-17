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

	got, ok, conflicts := RenderMarkdownWithConflicts(jsonContent, &fallback)
	if !ok {
		t.Fatalf("expected markdown rendered from JSON content")
	}

	if len(conflicts) > 0 {
		t.Fatalf("expected no conflicts, got %v", conflicts)
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

	got, ok, conflicts := RenderMarkdownWithConflicts(jsonContent, nil)
	if !ok {
		t.Fatalf("expected markdown rendered from JSON content")
	}

	if len(conflicts) > 0 {
		t.Fatalf("expected no conflicts, got %v", conflicts)
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

func TestMergeVotingTablesChoosesBestMatchingCandidate(t *testing.T) {
	passA := []Table{
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "№"}, {Text: "ФИО"}, {Text: "За"}, {Text: "Против"}}},
				{Cells: []TableCell{{Text: "1"}, {Text: "Петров Петр"}, {Text: ""}, {Text: ""}}},
			},
		},
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "№"}, {Text: "ФИО"}, {Text: "Комментарий"}}},
				{Cells: []TableCell{{Text: "1"}, {Text: "Наблюдатель"}, {Text: ""}}},
			},
		},
	}

	passB := []Table{
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "№"}, {Text: "ФИО"}, {Text: "Комментарий"}}},
				{Cells: []TableCell{{Text: "1"}, {Text: "Наблюдатель"}, {Text: "есть"}}},
			},
		},
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "№"}, {Text: "Фамилия Имя"}, {Text: "За"}, {Text: "Против"}}},
				{Cells: []TableCell{{Text: "1"}, {Text: "Петров Петр"}, {Text: "10"}, {Text: "0"}}},
			},
		},
	}

	merged, stats := MergeVotingTables(passA, passB, 0.6)
	if stats.MergedRows == 0 {
		t.Fatalf("expected at least one merged row, got %+v", stats)
	}

	if got := strings.TrimSpace(merged[0].Rows[1].Cells[2].Text); got != "10" {
		t.Fatalf("expected voting result to come from matching table, got %s", got)
	}
	if got := strings.TrimSpace(merged[1].Rows[1].Cells[2].Text); got != "есть" {
		t.Fatalf("expected comment table to match separately, got %s", got)
	}
}

func TestMergeVotingTablesSkipsLowSimilarity(t *testing.T) {
	passA := []Table{
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "№"}, {Text: "ФИО"}, {Text: "За"}}},
				{Cells: []TableCell{{Text: "1"}, {Text: "Иванов Иван"}, {Text: ""}}},
			},
		},
	}

	passB := []Table{
		{
			Rows: []TableRow{
				{Cells: []TableCell{{Text: "Item"}, {Text: "Value"}}},
				{Cells: []TableCell{{Text: "x"}, {Text: "y"}}},
			},
		},
	}

	merged, stats := MergeVotingTables(passA, passB, 0.6)
	if stats.MergedRows != 0 || stats.MatchedRows != 0 {
		t.Fatalf("expected no matches for unrelated tables, got %+v", stats)
	}

	if got := strings.TrimSpace(merged[0].Rows[1].Cells[2].Text); got != "" {
		t.Fatalf("expected base table to stay unchanged, got %s", got)
	}
}

func TestFillTablesMatchesByShape(t *testing.T) {
	jsonContent := map[string]any{
		"tables": []any{map[string]any{"id": "table-1", "markdown": "|H1|H2|\n|-|-|\n|New1|New2|"}},
	}

	fallback := "|H1|H2|\n|-|-|\n|Old1|Old2|"

	rendered, ok, conflicts := RenderMarkdownWithConflicts(jsonContent, &fallback)
	if !ok {
		t.Fatalf("expected markdown rendered from JSON content")
	}
	if len(conflicts) > 0 {
		t.Fatalf("expected no conflicts, got %v", conflicts)
	}

	if strings.Contains(rendered, "Old1") {
		t.Fatalf("expected original table to be replaced, got %s", rendered)
	}
	if !strings.Contains(rendered, "New1") {
		t.Fatalf("expected new content to appear, got %s", rendered)
	}
}

func TestFillTablesHandlesShiftedHeaders(t *testing.T) {
	jsonContent := map[string]any{
		"tables": []any{map[string]any{"id": "stable", "markdown": "|Имя|Голоса|\n|-|-|\n|Иван|10|"}},
	}

	fallback := "|Голоса|Имя|\n|-|-|\n|5|Пётр|"

	rendered, ok, conflicts := RenderMarkdownWithConflicts(jsonContent, &fallback)
	if !ok {
		t.Fatalf("expected markdown rendered from JSON content")
	}
	if len(conflicts) > 0 {
		t.Fatalf("expected no conflicts, got %v", conflicts)
	}

	if !strings.Contains(rendered, "|Имя|Голоса|") {
		t.Fatalf("expected header order from OCR table, got %s", rendered)
	}
	if !strings.Contains(rendered, "Иван") {
		t.Fatalf("expected OCR row to be used, got %s", rendered)
	}
}

func TestFillTablesReportsShapeConflict(t *testing.T) {
	jsonContent := map[string]any{
		"tables": []any{map[string]any{"id": "different", "markdown": "|A|B|\n|-|-|\n|1|2|"}},
	}

	fallback := "|A|B|C|\n|-|-|-|\n|1|2|3|"

	rendered, ok, conflicts := RenderMarkdownWithConflicts(jsonContent, &fallback)
	if !ok {
		t.Fatalf("expected markdown rendered from JSON content")
	}

	if len(conflicts) == 0 || !strings.Contains(conflicts[0].Reason, "shape mismatch") {
		t.Fatalf("expected shape mismatch conflict, got %v", conflicts)
	}

	if !strings.Contains(rendered, "|A|B|C|") {
		t.Fatalf("expected original table to remain, got %s", rendered)
	}
	if strings.Contains(rendered, "|A|B|\n|-|-|\n|1|2|") {
		t.Fatalf("expected conflicting table not to be appended")
	}
}

func TestFillTablesMissingOcrTable(t *testing.T) {
	jsonContent := map[string]any{"tables": []any{}}
	fallback := "|H1|H2|\n|-|-|\n|x|y|"

	rendered, ok, conflicts := RenderMarkdownWithConflicts(jsonContent, &fallback)
	if ok {
		t.Fatalf("expected fallback without OCR tables")
	}

	if len(conflicts) == 0 {
		t.Fatalf("expected conflict for missing OCR tables")
	}

	if rendered != strings.TrimSpace(fallback) {
		t.Fatalf("expected original markdown untouched, got %s", rendered)
	}
}
