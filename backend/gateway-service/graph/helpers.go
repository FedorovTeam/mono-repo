package graph

import (
	"backend-gateway-service/pkg/ctxutils"
	"context"
	"errors"
	"sort"

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
		ID:         doc.ID,
		FileName:   doc.FileName,
		Repository: doc.Repository,
		Branch:     doc.Branch,
		Path:       doc.Path,
		CreatedAt:  doc.CreatedAt,
		Status:     modelStatusFromDomain(doc.Status),
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
	result.ExtractTables = doc.ExtractTables
	if doc.OriginalDownloadURL != nil {
		result.OriginalDownloadURL = doc.OriginalDownloadURL
	}
	if strategy := modelConversionStrategyFromDomain(doc.ConversionStrategy); strategy != nil {
		result.ConversionStrategy = strategy
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

func modelConversionStrategyFromDomain(strategy domain.ConversionStrategy) *model.ConversionStrategy {
	switch strategy {
	case domain.ConversionStrategyTablesOnly:
		val := model.ConversionStrategyTablesOnly
		return &val
	case domain.ConversionStrategyDoclingWithTables:
		val := model.ConversionStrategyDoclingWithTables
		return &val
	default:
		val := model.ConversionStrategyDefault
		return &val
	}
}

func domainConversionStrategyFromModel(strategy *model.ConversionStrategy) domain.ConversionStrategy {
	if strategy == nil {
		return domain.ConversionStrategyDefault
	}

	switch *strategy {
	case model.ConversionStrategyTablesOnly:
		return domain.ConversionStrategyTablesOnly
	case model.ConversionStrategyDoclingWithTables:
		return domain.ConversionStrategyDoclingWithTables
	default:
		return domain.ConversionStrategyDefault
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

func mapHeaders(headers map[string]string) []*model.UploadHeader {
	if len(headers) == 0 {
		return []*model.UploadHeader{}
	}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]*model.UploadHeader, 0, len(headers))
	for _, key := range keys {
		result = append(result, &model.UploadHeader{Name: key, Value: headers[key]})
	}
	return result
}
