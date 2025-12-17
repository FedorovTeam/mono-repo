package graph

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"
	"errors"

	"backend-gateway-service/graph/model"
	"backend-gateway-service/internal/domain"
)

// UploadValidationConfig описывает ограничения на входные данные загрузки.
type UploadValidationConfig struct {
	SignedURLTTLSeconds int64
}

func mapDocument(doc *domain.Document) *model.KnowledgeDocument {
	if doc == nil {
		return nil
	}

	result := &model.KnowledgeDocument{
		ID:          doc.ID,
		FileName:    doc.FileName,
		Repository:  doc.Repository,
		Branch:      doc.Branch,
		Path:        doc.Path,
		Status:      modelStatusFromDomain(doc.Status),
		ForcePDFOcr: doc.ForcePdfOcr,
	}

	if doc.RepositoryOwner != nil {
		result.RepositoryOwner = doc.RepositoryOwner
	}

	if doc.JobID != nil {
		result.JobID = doc.JobID
	}
	if doc.Error != nil {
		result.Error = doc.Error
	}

	return result
}

func modelStatusFromDomain(status domain.DocumentStatus) model.KnowledgeDocumentStatus {
	switch status {
	case domain.DocumentStatusStoredOriginal:
		return model.KnowledgeDocumentStatusStoredOriginal
	case domain.DocumentStatusConvertedToMarkdown:
		return model.KnowledgeDocumentStatusConvertedToMarkdown
	case domain.DocumentStatusPublishedToGithub:
		return model.KnowledgeDocumentStatusPublishedToGithub
	case domain.DocumentStatusFailed:
		return model.KnowledgeDocumentStatusFailed
	default:
		return model.KnowledgeDocumentStatusUploaded
	}
}

func domainStatusFromModel(status model.KnowledgeDocumentStatus) domain.DocumentStatus {
	switch status {
	case model.KnowledgeDocumentStatusStoredOriginal:
		return domain.DocumentStatusStoredOriginal
	case model.KnowledgeDocumentStatusConvertedToMarkdown:
		return domain.DocumentStatusConvertedToMarkdown
	case model.KnowledgeDocumentStatusPublishedToGithub:
		return domain.DocumentStatusPublishedToGithub
	case model.KnowledgeDocumentStatusFailed:
		return domain.DocumentStatusFailed
	default:
		return domain.DocumentStatusUploaded
	}
}

func paginationParams(p *model.Pagination) (limit int, offset int) {
	limit = 10
	if p != nil {
		if p.Limit != nil {
			limit = *p.Limit
		}
		if p.Offset != nil {
			offset = *p.Offset
		}
	}
	return
}

func requireAPIKey(ctx context.Context) (string, error) {
	apiKey := ctxutils.APIKey(ctx)
	if apiKey == "" {
		return "", errors.New("missing api key for public mutation")
	}
	return apiKey, nil
}

func derefBool(value *bool) bool {
	if value == nil {
		return false
	}
	return *value
}
