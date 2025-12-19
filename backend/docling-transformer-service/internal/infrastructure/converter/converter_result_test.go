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
