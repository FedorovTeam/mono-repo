package converter

import "testing"

func TestBuildConvertResultUsesMarkdownContent(t *testing.T) {
	mdContent := "# md-source"

	converter := NewDoclingConverter(Config{}, nil)

	result, err := converter.buildConvertResult(convertDocumentResponse{
		Status: "success",
		Document: exportDocumentResponse{
			JsonContent: map[string]any{"tables": []any{}},
			MdContent:   &mdContent,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result.Content) != mdContent {
		t.Fatalf("expected markdown content to be returned, got: %s", string(result.Content))
	}
}

func TestBuildConvertResultFallbackToMarkdown(t *testing.T) {
	mdContent := "# fallback"

	converter := NewDoclingConverter(Config{}, nil)

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

func TestBuildConvertResultFallsBackToHtml(t *testing.T) {
	htmlContent := "<p>html</p>"

	converter := NewDoclingConverter(Config{}, nil)

	result, err := converter.buildConvertResult(convertDocumentResponse{
		Status: "success",
		Document: exportDocumentResponse{
			HtmlContent: &htmlContent,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(result.Content) != htmlContent || result.MediaType != "text/html" {
		t.Fatalf("expected html fallback, got: %s (media type %s)", string(result.Content), result.MediaType)
	}
}

func TestBuildConvertResultExtractsTableMetadata(t *testing.T) {
	mdContent := "# md-source"

	converter := NewDoclingConverter(Config{}, nil)

	result, err := converter.buildConvertResult(convertDocumentResponse{
		Status: "success",
		Document: exportDocumentResponse{
			JsonContent: map[string]any{
				"tables": []any{
					map[string]any{"page": 1},
					map[string]any{"page_number": "2"},
				},
			},
			MdContent: &mdContent,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Tables == nil || result.Tables.TablesCount != 2 {
		t.Fatalf("expected table metadata with count=2, got %+v", result.Tables)
	}
	if len(result.Tables.Pages) != 2 {
		t.Fatalf("expected pages to be filled, got %+v", result.Tables.Pages)
	}
}

func TestExtractTableMetadataFromPagesFallback(t *testing.T) {
	mdContent := "# md-source"

	converter := NewDoclingConverter(Config{}, nil)

	result, err := converter.buildConvertResult(convertDocumentResponse{
		Status: "success",
		Document: exportDocumentResponse{
			JsonContent: map[string]any{
				"pages": map[string]any{
					"1": map[string]any{"tables": []any{map[string]any{}, map[string]any{}}},
					"2": map[string]any{"tables": []any{}},
				},
			},
			MdContent: &mdContent,
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Tables == nil || result.Tables.TablesCount != 2 {
		t.Fatalf("expected table count from pages, got %+v", result.Tables)
	}
	if len(result.Tables.Pages) != 1 || result.Tables.Pages[0] != 1 {
		t.Fatalf("expected single page=1, got %+v", result.Tables.Pages)
	}
}
