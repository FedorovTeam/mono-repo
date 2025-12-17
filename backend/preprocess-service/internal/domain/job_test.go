package domain

import "testing"

func TestPreprocessJobValidate(t *testing.T) {
	job := PreprocessJob{
		ID:           "job-1",
		DocumentID:   "doc-1",
		SourceBucket: "source",
		SourceKey:    "docx/file.docx",
		SourceETag:   "etag-1",
		TargetBucket: "target",
		TargetKey:    "out/file.pdf",
	}

	if err := job.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestPreprocessJobValidateMissingFields(t *testing.T) {
	job := PreprocessJob{TargetBucket: "", TargetKey: ""}

	if err := job.Validate(); err == nil {
		t.Fatal("expected validation error for empty job")
	}
}

func TestPreprocessJobValidateAllowsEmptyTargets(t *testing.T) {
	job := PreprocessJob{
		ID:           "job-1",
		DocumentID:   "doc-1",
		SourceBucket: "source",
		SourceKey:    "docx/file.docx",
		SourceETag:   "etag-1",
	}

	if err := job.Validate(); err != nil {
		t.Fatalf("expected empty targets to be allowed: %v", err)
	}
}
