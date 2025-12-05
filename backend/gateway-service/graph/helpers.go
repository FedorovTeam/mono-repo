package graph

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"backend-gateway-service/graph/model"
	pb "backend-gateway-service/proto/backend/document/v1"
)

func validateUploadInput(input model.UploadKnowledgeDocumentInput) error {
	if strings.TrimSpace(input.FileName) == "" {
		return errors.New("fileName is required")
	}
	if strings.TrimSpace(input.ContentBase64) == "" {
		return errors.New("contentBase64 is required")
	}
	if _, err := base64.StdEncoding.DecodeString(input.ContentBase64); err != nil {
		return fmt.Errorf("contentBase64 must be base64: %w", err)
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
