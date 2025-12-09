package graph

import (
	"testing"

	"backend-gateway-service/graph/model"
)

func strPtr(value string) *string { return &value }

func TestValidateUploadInputRepositoryFormats(t *testing.T) {
	baseInput := model.UploadKnowledgeDocumentInput{
		FileName:      "doc.pdf",
		FileSizeBytes: 10,
		Branch:        "main",
		Path:          "docs/doc.pdf",
	}
	cfg := UploadValidationConfig{MaxSizeBytes: 100, AllowedExtensions: []string{"pdf"}}

	cases := []struct {
		name          string
		repository    string
		expectedRepo  string
		expectedOwner *string
		expectError   bool
	}{
		{name: "repo only", repository: "docs", expectedRepo: "docs"},
		{name: "owner and repo", repository: "owner/docs", expectedRepo: "docs", expectedOwner: strPtr("owner")},
		{name: "invalid with extra slash", repository: "owner/docs/extra", expectError: true},
		{name: "missing repo after slash", repository: "owner/", expectError: true},
		{name: "empty repository", repository: "  ", expectError: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := baseInput
			input.Repository = tc.repository

			repo, owner, err := validateUploadInput(input, cfg)

			if tc.expectError {
				if err == nil {
					t.Fatalf("expected error for repository %q", tc.repository)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if repo != tc.expectedRepo {
				t.Fatalf("expected repo %q, got %q", tc.expectedRepo, repo)
			}

			if (owner == nil) != (tc.expectedOwner == nil) {
				t.Fatalf("owner expectation mismatch: expected %v, got %v", tc.expectedOwner, owner)
			}

			if owner != nil && *owner != *tc.expectedOwner {
				t.Fatalf("expected owner %q, got %q", *tc.expectedOwner, *owner)
			}
		})
	}
}
