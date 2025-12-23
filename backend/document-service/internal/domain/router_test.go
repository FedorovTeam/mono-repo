package domain

import "testing"

func TestStageRouterChoosesPreprocessForDocAndPdf(t *testing.T) {
	router := NewStageRouter(RouterRules{PreprocessEnabled: true})

	doc := &Document{OriginalObjectKey: "incoming/report.doc"}
	if stage := router.NextStageAfterStore(doc); stage != JobStagePreprocessToDocx {
		t.Fatalf("expected preprocess stage, got %s", stage)
	}

	pdf := &Document{OriginalObjectKey: "incoming/report.pdf"}
	if stage := router.NextStageAfterStore(pdf); stage != JobStageConvertToMarkdown {
		t.Fatalf("expected convert stage for pdf, got %s", stage)
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
	if stage := router.NextStageAfterStore(doc); stage != JobStageConvertToMarkdown {
		t.Fatalf("expected convert when table extractor disabled, got %s", stage)
	}
}

func TestStageRouterUsesTablesOnlyStrategy(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true})
	doc := &Document{OriginalObjectKey: "incoming/meeting.pdf", ConversionStrategy: ConversionStrategyTablesOnly}
	if stage := router.NextStageAfterStore(doc); stage != JobStageExtractTables {
		t.Fatalf("expected extract_tables for tables_only strategy, got %s", stage)
	}
}

func TestStageRouterUsesDoclingWithTablesStrategy(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true})
	doc := &Document{OriginalObjectKey: "incoming/meeting.pdf", ConversionStrategy: ConversionStrategyDoclingWithTables}
	if stage := router.NextStageAfterStore(doc); stage != JobStageConvertToMarkdown {
		t.Fatalf("expected convert_to_markdown for docling_with_tables strategy, got %s", stage)
	}
	if !router.ShouldExtractTablesAfterConvert(doc, false, 0, nil) {
		t.Fatalf("expected extract_tables after convert for docling_with_tables strategy")
	}

	docDocx := &Document{OriginalObjectKey: "incoming/meeting.docx", ConversionStrategy: ConversionStrategyDoclingWithTables}
	if !router.ShouldExtractTablesAfterConvert(docDocx, false, 0, nil) {
		t.Fatalf("expected docx to schedule table extraction after convert when using docling_with_tables strategy")
	}
}

func TestStageRouterPathPrefixOverrides(t *testing.T) {
	router := NewStageRouter(RouterRules{
		TableExtractorEnabled: true,
		RepositoryRules: []RepositoryRouterRule{
			{PathPrefix: "docs/private/", TableExtractorEnabled: boolPtr(false)},
		},
	})

	doc := &Document{Path: "docs/private/report.pdf", OriginalObjectKey: "docs/private/report.pdf", ExtractTables: true}
	if stage := router.NextStageAfterStore(doc); stage != JobStageConvertToMarkdown {
		t.Fatalf("expected convert_to_markdown when path rule disables table extractor, got %s", stage)
	}
}

func TestTableExtractionConfigUsesRepositoryDefaults(t *testing.T) {
	router := NewStageRouter(RouterRules{
		TableExtractorEnabled:    true,
		ArtifactsBucket:          "fallback-bucket",
		ArtifactsPrefixTemplate:  "tables/{{ .DocumentID }}/{{ .QueueID }}/",
		TableExtractorExtensions: []string{"pdf"},
		RepositoryRules: []RepositoryRouterRule{
			{
				Owner:                   "acme",
				Repository:              "kb",
				PathPrefix:              "reports/",
				ArtifactsBucket:         "custom-bucket",
				ArtifactsPrefixTemplate: "custom/{{ .Repository }}/{{ .QueueID }}/",
			},
		},
	})

	cfg := router.TableExtractionConfig(&Document{Owner: "acme", Repository: "kb", Path: "reports/monthly.md"})
	if cfg.ArtifactsBucket != "custom-bucket" {
		t.Fatalf("expected rule artifacts_bucket, got %s", cfg.ArtifactsBucket)
	}
	if cfg.ArtifactsPrefixTmpl != "custom/{{ .Repository }}/{{ .QueueID }}/" {
		t.Fatalf("unexpected prefix template: %s", cfg.ArtifactsPrefixTmpl)
	}
}

func TestDefaultExtractTablesPrefersConfiguredExtensions(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true})
	pdf := &Document{OriginalObjectKey: "incoming/report.pdf", ConversionStrategy: ConversionStrategyTablesOnly}
	if !router.DefaultExtractTables(pdf) {
		t.Fatalf("expected tables_only strategy to enable table extraction by default")
	}

	docx := &Document{OriginalObjectKey: "incoming/report.docx"}
	if router.DefaultExtractTables(docx) {
		t.Fatalf("expected docx without strategy not to use table extraction by default")
	}
}

func TestStageRouterUsesDetectedTablesWhenEnabled(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true, TableDetectionMinCount: 1})
	doc := &Document{OriginalObjectKey: "incoming/with-tables.pdf"}
	if router.ShouldExtractTablesAfterConvert(doc, false, 2, []int32{1}) {
		t.Fatalf("expected router to skip table extraction without explicit intent")
	}
}

func TestStageRouterHonorsDetectionThreshold(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true, TableDetectionMinCount: 2})
	doc := &Document{OriginalObjectKey: "incoming/with-tables.pdf"}
	if router.ShouldExtractTablesAfterConvert(doc, false, 1, []int32{1}) {
		t.Fatalf("expected router to skip extraction without explicit intent")
	}
	if router.ShouldExtractTablesAfterConvert(doc, false, 2, []int32{1}) {
		t.Fatalf("expected router to skip extraction without explicit intent even when threshold met")
	}
}

func TestStageRouterDetectsTablesForDocx(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true, TableDetectionMinCount: 1})
	doc := &Document{OriginalObjectKey: "incoming/with-tables.docx"}
	if router.ShouldExtractTablesAfterConvert(doc, false, 3, []int32{1}) {
		t.Fatalf("expected router to skip extraction without explicit intent")
	}
}

func TestStageRouterUsesPagesWhenCountMissing(t *testing.T) {
	router := NewStageRouter(RouterRules{TableExtractorEnabled: true, TableDetectionMinCount: 1})
	doc := &Document{OriginalObjectKey: "incoming/with-tables.pdf"}
	if router.ShouldExtractTablesAfterConvert(doc, false, 0, []int32{3, 3}) {
		t.Fatalf("expected router to skip extraction without explicit intent")
	}
}

func boolPtr(val bool) *bool { return &val }
