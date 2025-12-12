package document

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"backend-document-service/internal/domain"
)

type Defaults struct {
	OriginalBucket string
	MarkdownBucket string
	Owner          string
	Repository     string
	Branch         string
	BaseBranch     string
}

type CreateInput struct {
	ID                string
	FileName          string
	Owner             string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	UploadBucket      string
	UploadObjectKey   string
	MarkdownBucket    string
	MarkdownObjectKey string
	CreatePR          bool
}

type ConfirmUploadInput struct {
	DocumentID string
	Bucket     string
	ObjectKey  string
}

type UpdateStatusInput struct {
	DocumentID        string
	Status            domain.Status
	Reason            string
	ErrorReason       string
	MarkdownObjectKey string
	OriginalObjectKey string
	Version           int32
}

type CreateDocument struct {
	Repo     domain.DocumentRepository
	Storage  domain.Storage
	Defaults Defaults
}

type ConfirmUpload struct {
	Repo    domain.DocumentRepository
	Storage domain.Storage
}

type UpdateStatus struct {
	Repo    domain.DocumentRepository
	Storage domain.Storage
}

func (uc *CreateDocument) Execute(ctx context.Context, in CreateInput) (*domain.Document, error) {
	if strings.TrimSpace(in.FileName) == "" {
		return nil, errors.New("name is required")
	}
	if in.ID != "" {
		if _, err := uuid.Parse(in.ID); err != nil {
			return nil, errors.New("id is not a valid uuid")
		}
	} else {
		in.ID = uuid.NewString()
	}
	owner := strings.TrimSpace(in.Owner)
	repository := strings.TrimSpace(in.Repository)
	if repository == "" {
		repository = uc.Defaults.Repository
	}
	if strings.Contains(repository, "/") {
		ownerPart, repoPart, found := strings.Cut(repository, "/")
		if !found || ownerPart == "" || repoPart == "" || strings.Contains(repoPart, "/") {
			return nil, status.Error(codes.InvalidArgument, "repository must be in owner/repo format")
		}
		if owner != "" && ownerPart != owner {
			return nil, status.Error(codes.InvalidArgument, "owner does not match repository")
		}
		if owner == "" {
			owner = ownerPart
		}
		repository = repoPart
	}
	if owner == "" {
		owner = uc.Defaults.Owner
	}
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		branch = uc.Defaults.Branch
	}
	baseBranch := strings.TrimSpace(in.BaseBranch)
	if baseBranch == "" {
		baseBranch = uc.Defaults.BaseBranch
	}
	if baseBranch == "" {
		baseBranch = branch
	}

	originalBucket := strings.TrimSpace(in.UploadBucket)
	if originalBucket == "" {
		originalBucket = uc.Defaults.OriginalBucket
	}
	markdownBucket := strings.TrimSpace(in.MarkdownBucket)
	if markdownBucket == "" {
		markdownBucket = uc.Defaults.MarkdownBucket
	}
	if originalBucket == "" || markdownBucket == "" {
		return nil, errors.New("buckets are required")
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		path = in.FileName
	}

	originalKey := strings.TrimSpace(in.UploadObjectKey)
	markdownKey := strings.TrimSpace(in.MarkdownObjectKey)
	if originalKey == "" || markdownKey == "" {
		return nil, errors.New("object keys are required")
	}

	doc := &domain.Document{
		ID:                in.ID,
		Name:              in.FileName,
		Owner:             owner,
		Repository:        repository,
		Branch:            branch,
		BaseBranch:        baseBranch,
		Path:              path,
		WorkflowID:        in.ID,
		Status:            domain.StatusUploaded,
		OriginalBucket:    originalBucket,
		OriginalObjectKey: originalKey,
		MarkdownBucket:    markdownBucket,
		MarkdownObjectKey: markdownKey,
		CreatePR:          in.CreatePR,
	}
	version := &domain.DocumentVersion{
		Version:           1,
		OriginalObjectKey: originalKey,
		MarkdownObjectKey: markdownKey,
	}
	log := &domain.DocumentStatusLog{ToStatus: domain.StatusUploaded}
	created, err := uc.Repo.CreateDocument(ctx, doc, version, log)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func (uc *ConfirmUpload) Execute(ctx context.Context, in ConfirmUploadInput) (*domain.Document, error) {
	if _, err := uuid.Parse(in.DocumentID); err != nil {
		return nil, errors.New("invalid id")
	}
	if strings.TrimSpace(in.ObjectKey) == "" {
		return nil, errors.New("object_key is required")
	}
	if strings.TrimSpace(in.Bucket) == "" {
		return nil, errors.New("bucket is required")
	}

	doc, err := uc.Repo.GetDocument(ctx, in.DocumentID)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, errors.New("document not found")
	}
	if doc.OriginalBucket != "" && doc.OriginalBucket != in.Bucket {
		return nil, fmt.Errorf("bucket mismatch: expected %s, got %s", doc.OriginalBucket, in.Bucket)
	}
	if doc.OriginalObjectKey != "" && doc.OriginalObjectKey != in.ObjectKey {
		return nil, fmt.Errorf("object_key mismatch: expected %s, got %s", doc.OriginalObjectKey, in.ObjectKey)
	}

	doc.OriginalBucket = in.Bucket
	doc.OriginalObjectKey = in.ObjectKey

	return doc, nil
}

func (uc *UpdateStatus) Execute(ctx context.Context, in UpdateStatusInput) (*domain.Document, error) {
	if _, err := uuid.Parse(in.DocumentID); err != nil {
		return nil, errors.New("invalid id")
	}
	if err := domain.ValidateStatus(in.Status); err != nil {
		return nil, err
	}

	var version *domain.DocumentVersion
	if in.Version > 0 && (in.MarkdownObjectKey != "" || in.OriginalObjectKey != "") {
		version = &domain.DocumentVersion{
			Version:           in.Version,
			MarkdownObjectKey: in.MarkdownObjectKey,
			OriginalObjectKey: in.OriginalObjectKey,
		}
	}

	doc, err := uc.Repo.UpdateStatus(ctx, in.DocumentID, in.Status, in.Reason, in.ErrorReason, version)
	if err != nil {
		return nil, err
	}
	return doc, nil
}
