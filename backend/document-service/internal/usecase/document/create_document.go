package document

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"backend-document-service/internal/domain"
)

// Defaults задаёт значения по умолчанию для нового документа.
type Defaults struct {
	OriginalBucket string
	MarkdownBucket string
	Owner          string
	Repository     string
	Branch         string
	BaseBranch     string
}

// CreateDocument создаёт запись о документе и первую версию.
type CreateDocument struct {
	repo     Repository
	defaults Defaults
	router   *domain.StageRouter
}

func NewCreateDocument(repo Repository, defaults Defaults, router *domain.StageRouter) *CreateDocument {
	return &CreateDocument{repo: repo, defaults: defaults, router: router}
}

func (uc *CreateDocument) Execute(ctx context.Context, in CreateDocumentInput) (*domain.Document, error) {
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
		repository = uc.defaults.Repository
	}
	if strings.Contains(repository, "/") {
		ownerPart, repoPart, found := strings.Cut(repository, "/")
		if !found || ownerPart == "" || repoPart == "" || strings.Contains(repoPart, "/") {
			return nil, errors.New("repository must be in owner/repo format")
		}
		if owner != "" && ownerPart != owner {
			return nil, errors.New("owner does not match repository")
		}
		if owner == "" {
			owner = ownerPart
		}
		repository = repoPart
	}
	if owner == "" {
		owner = uc.defaults.Owner
	}
	branch := strings.TrimSpace(in.Branch)
	if branch == "" {
		branch = uc.defaults.Branch
	}
	baseBranch := strings.TrimSpace(in.BaseBranch)
	if baseBranch == "" {
		baseBranch = uc.defaults.BaseBranch
	}
	if baseBranch == "" {
		baseBranch = branch
	}

	originalBucket := strings.TrimSpace(in.UploadBucket)
	if originalBucket == "" {
		originalBucket = uc.defaults.OriginalBucket
	}
	markdownBucket := strings.TrimSpace(in.MarkdownBucket)
	if markdownBucket == "" {
		markdownBucket = uc.defaults.MarkdownBucket
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		path = defaultDocumentPath(in.ID)
	}

	strategy := domain.NormalizeConversionStrategy(in.ConversionStrategy)

	originalKey := strings.TrimSpace(in.UploadObjectKey)
	markdownKey := strings.TrimSpace(in.MarkdownObjectKey)

	doc := &domain.Document{
		ID:                 in.ID,
		Name:               in.FileName,
		Owner:              owner,
		Repository:         repository,
		Branch:             branch,
		BaseBranch:         baseBranch,
		Path:               path,
		WorkflowID:         in.ID,
		Status:             domain.DocumentStatusUploaded,
		OriginalBucket:     originalBucket,
		OriginalObjectKey:  originalKey,
		MarkdownBucket:     markdownBucket,
		MarkdownObjectKey:  markdownKey,
		CreatePR:           in.CreatePR,
		ConversionStrategy: strategy,
		ExtractTables:      in.ExtractTables,
	}

	if doc.ConversionStrategy == domain.ConversionStrategyDoclingWithTables {
		doc.ExtractTables = true
	}

	if !doc.ExtractTables && uc.router != nil && uc.router.DefaultExtractTables(doc) {
		doc.ExtractTables = true
	}

	if err := doc.ValidateForCreation(); err != nil {
		return nil, err
	}

	version := &domain.DocumentVersion{Version: 1, OriginalObjectKey: originalKey, MarkdownObjectKey: markdownKey}
	log := &domain.DocumentStatusLog{ToStatus: domain.DocumentStatusUploaded}
	created, err := uc.repo.CreateDocument(ctx, doc, version, log)
	if err != nil {
		return nil, err
	}
	return created, nil
}

func defaultDocumentPath(id string) string {
	return fmt.Sprintf("knowledge/docs/%s.md", id)
}
