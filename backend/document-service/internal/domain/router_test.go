package domain

import "testing"

func TestStageRouterChoosesPreprocessForDocAndPdf(t *testing.T) {
	router := NewStageRouter(RouterRules{PreprocessEnabled: true})

	doc := &Document{OriginalObjectKey: "incoming/report.doc"}
	if stage := router.NextStageAfterStore(doc); stage != JobStagePreprocessToDocx {
		t.Fatalf("expected preprocess stage, got %s", stage)
	}

	pdf := &Document{OriginalObjectKey: "incoming/report.pdf"}
	if stage := router.NextStageAfterStore(pdf); stage != JobStagePreprocessToDocx {
		t.Fatalf("expected preprocess stage for pdf, got %s", stage)
	}

	docx := &Document{OriginalObjectKey: "incoming/report.DOCX"}
	if stage := router.NextStageAfterStore(docx); stage != JobStageConvertToMarkdown {
		t.Fatalf("expected convert stage for docx, got %s", stage)
	}
}

func TestStageRouterRepositoryOverride(t *testing.T) {
	router := NewStageRouter(RouterRules{
		PreprocessEnabled: true,
		RepositoryRules: []RepositoryRouterRule{
			{Owner: "acme", Repository: "kb", PreprocessEnabled: boolPtr(false)},
		},
	})

	doc := &Document{Owner: "acme", Repository: "kb", OriginalObjectKey: "incoming/report.doc"}
	if stage := router.NextStageAfterStore(doc); stage != JobStageConvertToMarkdown {
		t.Fatalf("expected convert_to_markdown when override disables preprocess, got %s", stage)
	}
}

func TestStageRouterUsesTableExtractorForFlaggedPdf(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true, PreprocessEnabled: true})
	doc := &Document{OriginalObjectKey: "incoming/report.pdf", ExtractTables: true}
	if stage := router.NextStageAfterStore(doc); stage != JobStageExtractTables {
		t.Fatalf("expected extract_tables for flagged pdf, got %s", stage)
	}
}

func TestStageRouterFallsBackToPreprocessWhenTableExtractorDisabled(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: false, PreprocessEnabled: true})
	doc := &Document{OriginalObjectKey: "incoming/report.pdf", ExtractTables: true}
	if stage := router.NextStageAfterStore(doc); stage != JobStagePreprocessToDocx {
		t.Fatalf("expected preprocess when table extractor disabled, got %s", stage)
	}
}

func TestStageRouterUsesTablesOnlyStrategy(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true})
	doc := &Document{OriginalObjectKey: "incoming/meeting.pdf", ConversionStrategy: ConversionStrategyTablesOnly}
	if stage := router.NextStageAfterStore(doc); stage != JobStageExtractTables {
		t.Fatalf("expected extract_tables for tables_only strategy, got %s", stage)
	}
}

func boolPtr(val bool) *bool { return &val }
