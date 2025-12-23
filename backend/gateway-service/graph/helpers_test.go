package graph

import (
	"testing"
	"time"

	"backend-gateway-service/graph/model"
	"backend-gateway-service/internal/domain"
)

func TestMapDocument(t *testing.T) {
	owner := "org"
	job := "job-1"
	createdAt := time.Now()
	doc := mapDocument(&domain.Document{ID: "1", FileName: "file", Repository: "org/repo", RepositoryOwner: &owner, Branch: "main", Path: "docs/file", CreatedAt: createdAt, Status: domain.DocumentStatusConvertedToMarkdown, JobID: &job})

	if doc == nil || doc.ID != "1" {
		t.Fatalf("expected document mapped")
	}
	if doc.RepositoryOwner == nil || *doc.RepositoryOwner != owner {
		t.Fatalf("repository owner not mapped")
	}
	if !doc.CreatedAt.Equal(createdAt) {
		t.Fatalf("createdAt not mapped")
	}
	if doc.Status != model.KnowledgeDocumentStatusConvertedToMarkdown {
		t.Fatalf("unexpected status: %s", doc.Status)
	}
	if doc.JobID == nil || *doc.JobID != job {
		t.Fatalf("job id not mapped")
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

func TestMapHeaders(t *testing.T) {
	if headers := mapHeaders(nil); len(headers) != 0 {
		t.Fatalf("expected empty slice for empty headers")
	}

	result := mapHeaders(map[string]string{
		"b-header": "2",
		"a-header": "1",
	})
	if len(result) != 2 {
		t.Fatalf("unexpected length: %d", len(result))
	}
	if result[0].Name != "a-header" || result[0].Value != "1" {
		t.Fatalf("headers not sorted/mapped: %+v", result[0])
	}
	if result[1].Name != "b-header" || result[1].Value != "2" {
		t.Fatalf("headers not sorted/mapped: %+v", result[1])
	}
}

func intPointer(v int) *int { return &v }
