package graph

import (
	"context"
	"errors"
	"strings"

	"backend-gateway-service/graph/model"
	"backend-gateway-service/internal/ctxutils"
	pb "backend-gateway-service/proto/backend/document/v1"
)

// UploadValidationConfig описывает ограничения на входные данные загрузки.
type UploadValidationConfig struct {
	SignedURLTTLSeconds int64
}

func validateUploadInput(input model.UploadKnowledgeDocumentInput, cfg UploadValidationConfig) (repository string, owner *string, err error) {
	if strings.TrimSpace(input.FileName) == "" {
		return "", nil, errors.New("fileName is required")
	}

	repository = strings.TrimSpace(input.Repository)
	if repository == "" {
		return "", nil, errors.New("repository is required")
	}

	if strings.TrimSpace(input.Branch) == "" {
		return "", nil, errors.New("branch is required")
	}
	if strings.TrimSpace(input.Path) == "" {
		return "", nil, errors.New("path is required")
	}
	if input.FileSizeBytes <= 0 {
		return "", nil, errors.New("fileSizeBytes must be positive")
	}
	return repository, owner, nil
}

func mapDocument(doc *pb.Document) *model.KnowledgeDocument {
	if doc == nil {
		return nil
	}

	status := doc.GetActualStatus()
	if status == pb.DocumentStatus_DOCUMENT_STATUS_UNSPECIFIED {
		status = doc.GetStatus()
	}

	result := &model.KnowledgeDocument{
		ID:         doc.GetId(),
		FileName:   doc.GetFileName(),
		Repository: doc.GetRepository(),
		Branch:     doc.GetBranch(),
		Path:       doc.GetPath(),
		Status:     modelStatusFromProto(status),
	}

	if doc.GetRepositoryOwner() != "" {
		owner := doc.GetRepositoryOwner()
		result.RepositoryOwner = &owner
	}

	jobID := doc.GetJobId()
	if jobID == "" {
		jobID = doc.GetWorkflowId()
	}
	if jobID == "" {
		jobID = doc.GetId()
	}
	if jobID != "" {
		result.JobID = &jobID
	}
	if doc.GetError() != "" {
		errText := doc.GetError()
		result.Error = &errText
	}

	return result
}

func modelStatusFromProto(status pb.DocumentStatus) model.KnowledgeDocumentStatus {
	switch status {
	case pb.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL:
		return model.KnowledgeDocumentStatusStoredOriginal
	case pb.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN:
		return model.KnowledgeDocumentStatusConvertedToMarkdown
	case pb.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB:
		return model.KnowledgeDocumentStatusPublishedToGithub
	case pb.DocumentStatus_DOCUMENT_STATUS_FAILED:
		return model.KnowledgeDocumentStatusFailed
	default:
		return model.KnowledgeDocumentStatusUploaded
	}
}

func protoStatusFromModel(status model.KnowledgeDocumentStatus) pb.DocumentStatus {
	switch status {
	case model.KnowledgeDocumentStatusStoredOriginal:
		return pb.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL
	case model.KnowledgeDocumentStatusConvertedToMarkdown:
		return pb.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN
	case model.KnowledgeDocumentStatusPublishedToGithub:
		return pb.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB
	case model.KnowledgeDocumentStatusFailed:
		return pb.DocumentStatus_DOCUMENT_STATUS_FAILED
	default:
		return pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED
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
