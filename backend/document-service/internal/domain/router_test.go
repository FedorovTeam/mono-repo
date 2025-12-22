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

func boolPtr(val bool) *bool { return &val }
