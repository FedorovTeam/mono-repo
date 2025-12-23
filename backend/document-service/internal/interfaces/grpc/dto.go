package grpc

import (
	"backend-document-service/internal/domain"
	document "backend-document-service/internal/usecase/document"
	documentv1 "backend-document-service/proto/backend/document/v1"
)

type createDocumentDTO struct {
	ID                 string
	FileName           string
	Owner              string
	Repository         string
	Branch             string
	BaseBranch         string
	Path               string
	UploadBucket       string
	UploadObjectKey    string
	MarkdownBucket     string
	MarkdownObjectKey  string
	CreatePR           bool
	ConversionStrategy string
	ExtractTables      bool
}

type updateDocumentStatusDTO struct {
	DocumentID        string
	Status            domain.DocumentStatus
	Reason            string
	ErrorReason       string
	MarkdownObjectKey string
	OriginalObjectKey string
	Version           int32
}

type confirmUploadDTO struct {
	DocumentID         string
	Bucket             string
	ObjectKey          string
	ExtractTables      *bool
	ConversionStrategy string
}

type listDocumentsDTO struct {
	Limit  int
	Offset int
	Status *domain.DocumentStatus
}

func fromCreateDocumentRequest(req *documentv1.CreateDocumentRequest) createDocumentDTO {
	return createDocumentDTO{
		ID:                 req.GetId(),
		FileName:           req.GetFileName(),
		Owner:              req.GetOwner(),
		Repository:         req.GetRepository(),
		Branch:             req.GetBranch(),
		BaseBranch:         req.GetBaseBranch(),
		Path:               req.GetPath(),
		UploadBucket:       req.GetUploadBucket(),
		UploadObjectKey:    req.GetUploadObjectKey(),
		MarkdownBucket:     req.GetMarkdownBucket(),
		MarkdownObjectKey:  req.GetMarkdownObjectKey(),
		CreatePR:           req.GetCreatePr(),
		ConversionStrategy: req.GetConversionStrategy(),
		ExtractTables:      req.GetExtractTables(),
	}
}

func toCreateDocumentCommand(dto createDocumentDTO) document.CreateDocumentInput {
	return document.CreateDocumentInput{
		ID:                 dto.ID,
		FileName:           dto.FileName,
		Owner:              dto.Owner,
		Repository:         dto.Repository,
		Branch:             dto.Branch,
		BaseBranch:         dto.BaseBranch,
		Path:               dto.Path,
		UploadBucket:       dto.UploadBucket,
		UploadObjectKey:    dto.UploadObjectKey,
		MarkdownBucket:     dto.MarkdownBucket,
		MarkdownObjectKey:  dto.MarkdownObjectKey,
		CreatePR:           dto.CreatePR,
		ConversionStrategy: dto.ConversionStrategy,
		ExtractTables:      dto.ExtractTables,
	}
}

func fromUpdateDocumentStatusRequest(req *documentv1.UpdateDocumentStatusRequest) updateDocumentStatusDTO {
	return updateDocumentStatusDTO{
		DocumentID:        req.GetId(),
		Status:            fromProtoStatus(req.GetStatus()),
		Reason:            req.GetReason(),
		ErrorReason:       req.GetReason(),
		MarkdownObjectKey: req.GetMarkdownObjectKey(),
		OriginalObjectKey: req.GetOriginalObjectKey(),
		Version:           req.GetVersion(),
	}
}

func toUpdateDocumentStatusCommand(dto updateDocumentStatusDTO) document.UpdateStatusInput {
	return document.UpdateStatusInput{
		DocumentID:        dto.DocumentID,
		Status:            string(dto.Status),
		Reason:            dto.Reason,
		ErrorReason:       dto.ErrorReason,
		MarkdownObjectKey: dto.MarkdownObjectKey,
		OriginalObjectKey: dto.OriginalObjectKey,
		Version:           dto.Version,
	}
}

func fromConfirmUploadRequest(req *documentv1.ConfirmUploadRequest) confirmUploadDTO {
	var extractTables *bool
	if req.GetExtractTables() {
		val := req.GetExtractTables()
		extractTables = &val
	}
	return confirmUploadDTO{
		DocumentID:         req.GetDocumentId(),
		Bucket:             req.GetBucket(),
		ObjectKey:          req.GetObjectKey(),
		ExtractTables:      extractTables,
		ConversionStrategy: req.GetConversionStrategy(),
	}
}

func toConfirmUploadCommand(dto confirmUploadDTO) document.ConfirmUploadInput {
	return document.ConfirmUploadInput{
		DocumentID:         dto.DocumentID,
		Bucket:             dto.Bucket,
		ObjectKey:          dto.ObjectKey,
		ExtractTables:      dto.ExtractTables,
		ConversionStrategy: dto.ConversionStrategy,
	}
}

func fromListDocumentsRequest(req *documentv1.ListDocumentsRequest) listDocumentsDTO {
	var status *domain.DocumentStatus
	if req.GetStatusFilter() != documentv1.DocumentStatus_DOCUMENT_STATUS_UNSPECIFIED {
		mapped := fromProtoStatus(req.GetStatusFilter())
		status = &mapped
	}
	return listDocumentsDTO{
		Limit:  int(req.GetLimit()),
		Offset: int(req.GetOffset()),
		Status: status,
	}
}
