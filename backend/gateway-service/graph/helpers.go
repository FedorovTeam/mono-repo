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

func validateUploadInput(input model.UploadKnowledgeDocumentInput, cfg UploadValidationConfig) error {
	if strings.TrimSpace(input.FileName) == "" {
		return errors.New("fileName is required")
	}
	if strings.TrimSpace(input.Repository) == "" {
		return errors.New("repository is required")
	}
	if strings.TrimSpace(input.Branch) == "" {
		return errors.New("branch is required")
	}
	if strings.TrimSpace(input.Path) == "" {
		return errors.New("path is required")
	}
	if input.FileSizeBytes <= 0 {
		return errors.New("fileSizeBytes must be positive")
	}
	if cfg.MaxSizeBytes > 0 && int64(input.FileSizeBytes) > cfg.MaxSizeBytes {
		return fmt.Errorf("fileSizeBytes exceeds limit %d", cfg.MaxSizeBytes)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(input.FileName), "."))
	if ext == "" {
		return errors.New("file extension is required")
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
			return fmt.Errorf("extension %s is not allowed", ext)
		}
	}
	return nil
}

func mapDocument(doc *pb.Document, workflowID string) *model.KnowledgeDocument {
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

	wf := doc.GetWorkflowId()
	if wf == "" {
		wf = workflowID
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
