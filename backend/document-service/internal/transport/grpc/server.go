package grpc

import (
	"context"
	"log"
	"net"

	"backend-document-service/internal/domain"
	usecase "backend-document-service/internal/usecase/document"
	documentv1 "backend-document-service/proto/backend/document/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	documentv1.UnimplementedDocumentServiceServer
	service DocumentService
}

type DocumentService interface {
	CreateDocument(ctx context.Context, in usecase.CreateDocumentInput) (*domain.Document, error)
	ConfirmUpload(ctx context.Context, in usecase.ConfirmUploadInput) (*domain.Document, error)
	UpdateStatus(ctx context.Context, in usecase.UpdateStatusInput) (*domain.Document, error)
	GetDocument(ctx context.Context, id string) (*domain.Document, error)
	ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) ([]domain.Document, error)
}

func NewServer(service DocumentService) *Server {
	return &Server{service: service}
}

func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	g := grpc.NewServer()
	documentv1.RegisterDocumentServiceServer(g, s)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		g.GracefulStop()
	}()
	log.Printf("document-service listening on %s", addr)
	return g.Serve(lis)
}

func (s *Server) CreateDocument(ctx context.Context, req *documentv1.CreateDocumentRequest) (*documentv1.Document, error) {
	s.logWithCorrelation(ctx, "CreateDocument")

	doc, err := s.service.CreateDocument(ctx, usecase.CreateDocumentInput{
		ID:                req.GetId(),
		FileName:          req.GetFileName(),
		Owner:             req.GetOwner(),
		Repository:        req.GetRepository(),
		Branch:            req.GetBranch(),
		BaseBranch:        req.GetBaseBranch(),
		Path:              req.GetPath(),
		UploadBucket:      req.GetUploadBucket(),
		UploadObjectKey:   req.GetUploadObjectKey(),
		MarkdownBucket:    req.GetMarkdownBucket(),
		MarkdownObjectKey: req.GetMarkdownObjectKey(),
		CreatePR:          req.GetCreatePr(),
	})
	if err != nil {
		return nil, err
	}
	return toProto(doc), nil
}

func (s *Server) GetDocument(ctx context.Context, req *documentv1.GetDocumentRequest) (*documentv1.Document, error) {
	s.logWithCorrelation(ctx, "GetDocument")
	doc, err := s.service.GetDocument(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return toProto(doc), nil
}

func (s *Server) ListDocuments(ctx context.Context, req *documentv1.ListDocumentsRequest) (*documentv1.ListDocumentsResponse, error) {
	s.logWithCorrelation(ctx, "ListDocuments")
	var status *domain.DocumentStatus
	if req.GetStatusFilter() != documentv1.DocumentStatus_DOCUMENT_STATUS_UNSPECIFIED {
		mapped := fromProtoStatus(req.GetStatusFilter())
		status = &mapped
	}
	items, err := s.service.ListDocuments(ctx, int(req.GetLimit()), int(req.GetOffset()), status)
	if err != nil {
		return nil, err
	}
	res := &documentv1.ListDocumentsResponse{}
	for i := range items {
		copy := items[i]
		res.Documents = append(res.Documents, toProto(&copy))
	}
	return res, nil
}

func (s *Server) UpdateDocumentStatus(ctx context.Context, req *documentv1.UpdateDocumentStatusRequest) (*documentv1.Document, error) {
	s.logWithCorrelation(ctx, "UpdateDocumentStatus")
	doc, err := s.service.UpdateStatus(ctx, usecase.UpdateStatusInput{
		DocumentID:        req.GetId(),
		Status:            string(fromProtoStatus(req.GetStatus())),
		Reason:            req.GetReason(),
		ErrorReason:       req.GetReason(),
		MarkdownObjectKey: req.GetMarkdownObjectKey(),
		OriginalObjectKey: req.GetOriginalObjectKey(),
		Version:           req.GetVersion(),
	})
	if err != nil {
		return nil, err
	}
	return toProto(doc), nil
}

func (s *Server) ConfirmUpload(ctx context.Context, req *documentv1.ConfirmUploadRequest) (*documentv1.Document, error) {
	s.logWithCorrelation(ctx, "ConfirmUpload")
	doc, err := s.service.ConfirmUpload(ctx, usecase.ConfirmUploadInput{
		DocumentID: req.GetDocumentId(),
		Bucket:     req.GetBucket(),
		ObjectKey:  req.GetObjectKey(),
	})
	if err != nil {
		return nil, err
	}
	return toProto(doc), nil
}

func toProto(doc *domain.Document) *documentv1.Document {
	actualStatus := doc.ActualStatus
	if actualStatus == "" {
		actualStatus = doc.Status
	}
	p := &documentv1.Document{
		Id:                doc.ID,
		FileName:          doc.Name,
		Owner:             doc.Owner,
		Repository:        doc.Repository,
		Branch:            doc.Branch,
		BaseBranch:        doc.BaseBranch,
		Path:              doc.Path,
		Status:            toProtoStatus(doc.Status),
		WorkflowId:        doc.WorkflowID,
		Error:             doc.ErrorReason,
		UploadBucket:      doc.OriginalBucket,
		UploadObjectKey:   doc.OriginalObjectKey,
		MarkdownBucket:    doc.MarkdownBucket,
		MarkdownObjectKey: doc.MarkdownObjectKey,
		CreatePr:          doc.CreatePR,
		CreatedAt:         timestamppb.New(doc.CreatedAt),
		UpdatedAt:         timestamppb.New(doc.UpdatedAt),
		JobId:             doc.ActiveJobID,
		ActualStatus:      toProtoStatus(actualStatus),
	}
	return p
}

func toProtoStatus(s domain.DocumentStatus) documentv1.DocumentStatus {
	switch s {
	case domain.DocumentStatusUploaded:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_UPLOADED
	case domain.DocumentStatusStoredOriginal:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL
	case domain.DocumentStatusConvertedToMarkdown:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN
	case domain.DocumentStatusPublishedToGitHub:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB
	case domain.DocumentStatusFailed:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_FAILED
	default:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_UNSPECIFIED
	}
}

func fromProtoStatus(s documentv1.DocumentStatus) domain.DocumentStatus {
	switch s {
	case documentv1.DocumentStatus_DOCUMENT_STATUS_UPLOADED:
		return domain.DocumentStatusUploaded
	case documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL:
		return domain.DocumentStatusStoredOriginal
	case documentv1.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN:
		return domain.DocumentStatusConvertedToMarkdown
	case documentv1.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB:
		return domain.DocumentStatusPublishedToGitHub
	case documentv1.DocumentStatus_DOCUMENT_STATUS_FAILED:
		return domain.DocumentStatusFailed
	default:
		return domain.DocumentStatus("")
	}
}

func (s *Server) logWithCorrelation(ctx context.Context, method string) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		corr := md.Get("x-correlation-id")
		if len(corr) > 0 {
			log.Printf("method=%s correlation_id=%s", method, corr[0])
			return
		}
	}
	log.Printf("method=%s correlation_id=absent", method)
}
