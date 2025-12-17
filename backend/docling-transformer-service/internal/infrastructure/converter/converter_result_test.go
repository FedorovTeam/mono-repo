package converter

import "testing"

func TestBuildConvertResultPrefersJsonTables(t *testing.T) {
	tableJSON := map[string]any{
		"tables": []any{
			map[string]any{
				"id": "t1",
				"rows": []any{
					map[string]any{"cells": []any{map[string]any{"text": "H1"}, map[string]any{"text": "H2"}}},
					map[string]any{"cells": []any{map[string]any{"text": "R1"}, map[string]any{"text": "R2"}}},
				},
			},
		},
		"marks": []any{
			map[string]any{"mark": "{{t1}}"},
		},
	}
	mdContent := "# placeholder\n\n{{t1}}"

	converter := NewDoclingConverter(Config{Render: RenderConfig{PreferJSON: true}}, nil)

	result, err := converter.buildConvertResult(convertDocumentResponse{
		Status: "success",
		Document: exportDocumentResponse{
			JsonContent: tableJSON,
			MdContent:   &mdContent,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result.Content) == mdContent {
		t.Fatalf("expected JSON-derived markdown to be preferred")
	}

	if string(result.Content) == "" {
		t.Fatalf("expected non-empty content")
	}
}

func TestBuildConvertResultFallbackToMarkdown(t *testing.T) {
	mdContent := "# fallback"

	converter := NewDoclingConverter(Config{Render: RenderConfig{PreferJSON: false}}, nil)

	result, err := converter.buildConvertResult(convertDocumentResponse{
		Status: "success",
		Document: exportDocumentResponse{
			MdContent: &mdContent,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result.Content) != mdContent {
		t.Fatalf("expected fallback md_content, got: %s", string(result.Content))
	}
}

func TestBuildConvertResultTwoPassVoting(t *testing.T) {
	tableJSON := map[string]any{
		"tables": []any{
			map[string]any{
				"id": "table_a",
				"rows": []any{
					map[string]any{"cells": []any{map[string]any{"text": "A"}}},
					map[string]any{"cells": []any{map[string]any{"text": "B"}}},
				},
			},
			map[string]any{
				"id":   "table_b",
				"rows": []any{map[string]any{"cells": []any{map[string]any{"text": "Only header"}}}},
			},
		},
		"marks": []any{map[string]any{"mark": "table_a"}},
	}

	mdContent := "md-source"

	converter := NewDoclingConverter(Config{Render: RenderConfig{
		PreferJSON:         false,
		TwoPassVotingMerge: true,
		VotingTableMinRows: 2,
		PassBTableCoverage: 0.5,
	}}, nil)

	result, err := converter.buildConvertResult(convertDocumentResponse{
		Status: "success",
		Document: exportDocumentResponse{
			JsonContent: tableJSON,
			MdContent:   &mdContent,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result.Content) == mdContent {
		t.Fatalf("expected JSON markdown to be selected by two-pass strategy")
	}
}
