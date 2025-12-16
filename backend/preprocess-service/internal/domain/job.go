package domain

import "fmt"

type PreprocessJob struct {
    DocumentID   string
    SourceObject string
    TargetObject string
}

func (j PreprocessJob) Validate() error {
    if j.DocumentID == "" {
        return fmt.Errorf("document id is required")
    }
    if j.SourceObject == "" {
        return fmt.Errorf("source object is required")
    }
    if j.TargetObject == "" {
        return fmt.Errorf("target object is required")
    }
    return nil
}
