package document

import (
	"backend-gateway-service/internal/domain"
	pb "backend-gateway-service/proto/backend/document/v1"
)

func mapDocument(doc *pb.Document) *domain.Document {
	if doc == nil {
		return nil
	}

	result := &domain.Document{
		ID:                 doc.GetId(),
		FileName:           doc.GetFileName(),
		Repository:         doc.GetRepository(),
		Branch:             doc.GetBranch(),
		Path:               doc.GetPath(),
		Status:             domainStatusFromProto(doc.GetStatus()),
		ExtractTables:      doc.GetExtractTables(),
		ConversionStrategy: domain.NormalizeConversionStrategy(doc.GetConversionStrategy()),
		UploadBucket:       doc.GetUploadBucket(),
		UploadObjectKey:    doc.GetUploadObjectKey(),
	}

	if ts := doc.GetCreatedAt(); ts != nil {
		result.CreatedAt = ts.AsTime()
	}

	if doc.GetOwner() != "" {
		owner := doc.GetOwner()
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

func domainStatusFromProto(status pb.DocumentStatus) domain.DocumentStatus {
	switch status {
	case pb.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL:
		return domain.DocumentStatusStoredOriginal
	case pb.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN:
		return domain.DocumentStatusConvertedToMarkdown
	case pb.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB:
		return domain.DocumentStatusPublishedToGithub
	case pb.DocumentStatus_DOCUMENT_STATUS_FAILED:
		return domain.DocumentStatusFailed
	default:
		return domain.DocumentStatusUploaded
	}
}

func protoStatusFromDomain(status domain.DocumentStatus) pb.DocumentStatus {
	switch status {
	case domain.DocumentStatusStoredOriginal:
		return pb.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL
	case domain.DocumentStatusConvertedToMarkdown:
		return pb.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN
	case domain.DocumentStatusPublishedToGithub:
		return pb.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB
	case domain.DocumentStatusFailed:
		return pb.DocumentStatus_DOCUMENT_STATUS_FAILED
	default:
		return pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED
	}
}
