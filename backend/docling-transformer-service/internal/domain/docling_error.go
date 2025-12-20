// backend/docling-transformer-service/internal/domain/docling_error.go
package domain

import "fmt"

// DoclingFailureError описывает неуспешный статус задачи в Docling с деталями.
type DoclingFailureError struct {
	TaskID  string
	Status  string
	Details string
}

func (e *DoclingFailureError) Error() string {
	base := fmt.Sprintf("docling status=%s for task %s", e.Status, e.TaskID)
	if e == nil || e.Details == "" {
		return base
	}
	return fmt.Sprintf("%s: %s", base, e.Details)
}
