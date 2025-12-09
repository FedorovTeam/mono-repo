package graph

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"backend-gateway-service/graph/model"
	pb "backend-gateway-service/proto/backend/document/v1"
)

// UploadValidationConfig описывает ограничения на входные данные загрузки.
type UploadValidationConfig struct {
	MaxSizeBytes        int64
	AllowedExtensions   []string
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

	if strings.Contains(repository, "/") {
		parts := strings.Split(repository, "/")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return "", nil, errors.New("repository must be in format <owner>/<repo> or <repo>")
		}
		normalizedOwner := strings.TrimSpace(parts[0])
		normalizedRepo := strings.TrimSpace(parts[1])
		repository = normalizedRepo
		owner = &normalizedOwner
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
	if cfg.MaxSizeBytes > 0 && int64(input.FileSizeBytes) > cfg.MaxSizeBytes {
		return "", nil, fmt.Errorf("fileSizeBytes exceeds limit %d", cfg.MaxSizeBytes)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(input.FileName), "."))
	if ext == "" {
		return "", nil, errors.New("file extension is required")
	}
	if len(cfg.AllowedExtensions) > 0 {
		allowed := false
		for _, candidate := range cfg.AllowedExtensions {
			if strings.EqualFold(candidate, ext) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "", nil, fmt.Errorf("extension %s is not allowed", ext)
		}
	}
	return repository, owner, nil
}

func mapDocument(doc *pb.Document) *model.KnowledgeDocument {
	if doc == nil {
		return nil
	}

	result := &model.KnowledgeDocument{
		ID:         doc.GetId(),
		FileName:   doc.GetFileName(),
		Repository: doc.GetRepository(),
		Branch:     doc.GetBranch(),
		Path:       doc.GetPath(),
		Status:     modelStatusFromProto(doc.GetStatus()),
	}

	if doc.GetRepositoryOwner() != "" {
		owner := doc.GetRepositoryOwner()
		result.RepositoryOwner = &owner
	}

	wf := doc.GetWorkflowId()
	if wf == "" {
		wf = doc.GetId()
	}
	if wf != "" {
		result.WorkflowID = &wf
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
