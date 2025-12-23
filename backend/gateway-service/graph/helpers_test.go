package graph

import (
	"testing"

	"backend-gateway-service/graph/model"
	"backend-gateway-service/internal/domain"
)

func TestMapDocument(t *testing.T) {
	owner := "org"
	job := "job-1"
	doc := mapDocument(&domain.Document{
		ID:              "1",
		FileName:        "file",
		Repository:      "org/repo",
		RepositoryOwner: &owner,
		Branch:          "main",
		Path:            "docs/file",
		Status:          domain.DocumentStatusConvertedToMarkdown,
		JobID:           &job,
		StatusHistory: []domain.DocumentStatusChange{
			{Status: domain.DocumentStatusUploaded},
			{Status: domain.DocumentStatusConvertedToMarkdown},
		},
	})

	if doc == nil || doc.ID != "1" {
		t.Fatalf("expected document mapped")
	}
	if doc.RepositoryOwner == nil || *doc.RepositoryOwner != owner {
		t.Fatalf("repository owner not mapped")
	}
	if doc.Status != model.KnowledgeDocumentStatusConvertedToMarkdown {
		t.Fatalf("unexpected status: %s", doc.Status)
	}
	if doc.JobID == nil || *doc.JobID != job {
		t.Fatalf("job id not mapped")
	}
	if len(doc.StatusHistory) != 2 || doc.StatusHistory[0].Status != model.KnowledgeDocumentStatusUploaded {
		t.Fatalf("status history not mapped")
	}
}

func TestPaginationParams(t *testing.T) {
	limit, offset := paginationParams(nil)
	if limit != 10 || offset != 0 {
		t.Fatalf("unexpected defaults: %d %d", limit, offset)
	}

	limit, offset = paginationParams(&model.Pagination{Limit: intPointer(5), Offset: intPointer(3)})
	if limit != 5 || offset != 3 {
		t.Fatalf("unexpected values: %d %d", limit, offset)
	}
}

func intPointer(v int) *int { return &v }
