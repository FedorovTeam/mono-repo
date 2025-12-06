package grpc

import (
	"context"
	"log"
	"net"
	"path/filepath"
	"strings"

	"backend-document-service/internal/model"
	svc "backend-document-service/internal/service/document"
	documentv1 "backend-document-service/proto/backend/document/v1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	documentv1.UnimplementedDocumentServiceServer
	service *svc.Service
}

func NewServer(service *svc.Service) *Server {
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

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(req.GetFileName())), ".")

	doc, err := s.service.CreateDocument(ctx, svc.CreateInput{
		ID:                req.GetId(),
		FileName:          req.GetFileName(),
		Repository:        req.GetRepository(),
		Branch:            req.GetBranch(),
		Path:              req.GetPath(),
		UploadBucket:      req.GetUploadBucket(),
		UploadObjectKey:   req.GetUploadObjectKey(),
		MarkdownBucket:    req.GetMarkdownBucket(),
		MarkdownObjectKey: req.GetMarkdownObjectKey(),
		FilenameExt:       ext,
		PublishPath:       req.GetPath(), // можно собрать сложнее, если надо
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
	var status *model.Status
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
	doc, err := s.service.UpdateStatus(ctx, svc.UpdateStatusInput{
		DocumentID:        req.GetId(),
		Status:            fromProtoStatus(req.GetStatus()),
		Reason:            req.GetReason(),
		MarkdownObjectKey: req.GetMarkdownObjectKey(),
		OriginalObjectKey: req.GetOriginalObjectKey(),
		Version:           req.GetVersion(),
	})
	if err != nil {
		return nil, err
	}
	return toProto(doc), nil
}

func toProto(doc *model.Document) *documentv1.Document {
	p := &documentv1.Document{
		Id:                doc.ID,
		FileName:          doc.Name,
		Status:            toProtoStatus(doc.Status),
		UploadBucket:      doc.OriginalBucket,
		UploadObjectKey:   doc.OriginalObjectKey,
		MarkdownBucket:    doc.MarkdownBucket,
		MarkdownObjectKey: doc.MarkdownObjectKey,
		CreatedAt:         timestamppb.New(doc.CreatedAt),
		UpdatedAt:         timestamppb.New(doc.UpdatedAt),
	}
	return p
}

func toProtoStatus(s model.Status) documentv1.DocumentStatus {
	switch s {
	case model.StatusUploaded:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_UPLOADED
	case model.StatusStoredOriginal:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL
	case model.StatusConvertedToMarkdown:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN
	case model.StatusPublishedToGitHub:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB
	case model.StatusFailed:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_FAILED
	default:
		return documentv1.DocumentStatus_DOCUMENT_STATUS_UNSPECIFIED
	}
}

func fromProtoStatus(s documentv1.DocumentStatus) model.Status {
	switch s {
	case documentv1.DocumentStatus_DOCUMENT_STATUS_UPLOADED:
		return model.StatusUploaded
	case documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL:
		return model.StatusStoredOriginal
	case documentv1.DocumentStatus_DOCUMENT_STATUS_CONVERTED_TO_MARKDOWN:
		return model.StatusConvertedToMarkdown
	case documentv1.DocumentStatus_DOCUMENT_STATUS_PUBLISHED_TO_GITHUB:
		return model.StatusPublishedToGitHub
	case documentv1.DocumentStatus_DOCUMENT_STATUS_FAILED:
		return model.StatusFailed
	default:
		return model.Status("")
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
