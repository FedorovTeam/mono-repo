package graph

import (
	"testing"

	"backend-gateway-service/graph/model"
)

func TestValidateUploadInputBasicChecks(t *testing.T) {
	baseInput := model.UploadKnowledgeDocumentInput{
		FileName:      "doc.pdf",
		FileSizeBytes: 10,
		Branch:        "main",
		Path:          "docs/doc.pdf",
		Repository:    "owner/repo",
	}
	cfg := UploadValidationConfig{MaxSizeBytes: 100}

	cases := []struct {
		name        string
		modify      func(input *model.UploadKnowledgeDocumentInput)
		expectError bool
	}{
		{name: "missing file name", modify: func(input *model.UploadKnowledgeDocumentInput) { input.FileName = "  " }, expectError: true},
		{name: "missing repository", modify: func(input *model.UploadKnowledgeDocumentInput) { input.Repository = "" }, expectError: true},
		{name: "missing branch", modify: func(input *model.UploadKnowledgeDocumentInput) { input.Branch = "  " }, expectError: true},
		{name: "missing path", modify: func(input *model.UploadKnowledgeDocumentInput) { input.Path = "" }, expectError: true},
		{name: "non-positive size", modify: func(input *model.UploadKnowledgeDocumentInput) { input.FileSizeBytes = 0 }, expectError: true},
		{name: "exceeds size limit", modify: func(input *model.UploadKnowledgeDocumentInput) { input.FileSizeBytes = 101 }, expectError: true},
		{name: "passes without normalization", modify: func(input *model.UploadKnowledgeDocumentInput) { input.Repository = " org/repo " }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := baseInput
			if tc.modify != nil {
				tc.modify(&input)
			}

			repo, owner, err := validateUploadInput(input, cfg)

			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error for case %s", tc.name)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if repo != "org/repo" {
				t.Fatalf("repository should be trimmed without normalization, got %q", repo)
			}
			if owner != nil {
				t.Fatalf("owner should not be derived in gateway: %v", owner)
			}
		})
	}
}
