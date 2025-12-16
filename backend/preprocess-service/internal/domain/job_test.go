package domain

import "testing"

func TestPreprocessJobValidate(t *testing.T) {
    job := PreprocessJob{
        DocumentID:   "123",
        SourceObject: "source.docx",
        TargetObject: "output.pdf",
    }

    if err := job.Validate(); err != nil {
        t.Fatalf("expected valid job, got error: %v", err)
    }

    invalid := PreprocessJob{}
    if err := invalid.Validate(); err == nil {
        t.Fatalf("expected validation error for empty job")
    }
}
