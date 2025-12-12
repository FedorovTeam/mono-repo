package grpc

import (
	"context"
	"net"
	"testing"
	"time"

	"backend-document-service/internal/config"
	"backend-document-service/internal/domain"
	"backend-document-service/internal/grpcdial"
	document "backend-document-service/internal/usecase/document"
	documentv1 "backend-document-service/proto/backend/document/v1"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func dialer(server *grpc.Server) (*grpc.ClientConn, func(), error) {
	lis := bufconn.Listen(bufSize)
	go func() {
		_ = server.Serve(lis)
	}()
	conn, err := grpcdial.DialContext(
		context.Background(),
		"bufnet",
		grpcClientConfig(),
		grpcdial.WithDialOptions(grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
			return lis.Dial()
		})),
		grpcdial.WithoutBlock(),
	)
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		conn.Close()
		server.Stop()
	}
	return conn, cleanup, nil
}

func grpcClientConfig() config.GRPCClient {
	return config.GRPCClient{
		DialTimeout:       time.Second,
		MinConnectTimeout: 500 * time.Millisecond,
		Backoff: config.Backoff{
			BaseDelay:  100 * time.Millisecond,
			Multiplier: 1.6,
			Jitter:     0.2,
			MaxDelay:   time.Second,
		},
		Keepalive: config.Keepalive{
			Time:                10 * time.Second,
			Timeout:             3 * time.Second,
			PermitWithoutStream: true,
		},
	}
}

type documentServiceMock struct {
	mock.Mock
}

func (m *documentServiceMock) CreateDocument(ctx context.Context, in document.CreateDocumentInput) (*domain.Document, error) {
	args := m.Called(ctx, in)
	doc, _ := args.Get(0).(*domain.Document)
	return doc, args.Error(1)
}

func (m *documentServiceMock) ConfirmUpload(ctx context.Context, in document.ConfirmUploadInput) (*domain.Document, error) {
	args := m.Called(ctx, in)
	doc, _ := args.Get(0).(*domain.Document)
	return doc, args.Error(1)
}

func (m *documentServiceMock) UpdateStatus(ctx context.Context, in document.UpdateStatusInput) (*domain.Document, error) {
	args := m.Called(ctx, in)
	doc, _ := args.Get(0).(*domain.Document)
	return doc, args.Error(1)
}

func (m *documentServiceMock) GetDocument(ctx context.Context, id string) (*domain.Document, error) {
	args := m.Called(ctx, id)
	doc, _ := args.Get(0).(*domain.Document)
	return doc, args.Error(1)
}

func (m *documentServiceMock) ListDocuments(ctx context.Context, limit, offset int, status *domain.DocumentStatus) ([]domain.Document, error) {
	args := m.Called(ctx, limit, offset, status)
	docs, _ := args.Get(0).([]domain.Document)
	return docs, args.Error(1)
}

func TestGRPCServerUsesDocumentService(t *testing.T) {
	svc := &documentServiceMock{}
	grpcServer := grpc.NewServer()
	documentv1.RegisterDocumentServiceServer(grpcServer, NewServer(svc))

	conn, cleanup, err := dialer(grpcServer)
	require.NoError(t, err)
	t.Cleanup(cleanup)

	client := documentv1.NewDocumentServiceClient(conn)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "test-corr"))

	createdAt := time.Now().UTC()
	createdDoc := &domain.Document{
		ID:                "doc-id",
		Name:              "doc",
		Owner:             "owner",
		Repository:        "owner/repo",
		Branch:            "main",
		BaseBranch:        "main",
		Path:              "docs/doc.md",
		Status:            domain.DocumentStatusUploaded,
		OriginalBucket:    "orig",
		OriginalObjectKey: "docs/doc.docx",
		MarkdownBucket:    "md",
		MarkdownObjectKey: "docs/doc.md",
		CreatePR:          true,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}

	confirmedDoc := &domain.Document{
		ID:                createdDoc.ID,
		Name:              createdDoc.Name,
		Owner:             createdDoc.Owner,
		Repository:        createdDoc.Repository,
		Branch:            createdDoc.Branch,
		BaseBranch:        createdDoc.BaseBranch,
		Path:              createdDoc.Path,
		Status:            domain.DocumentStatusUploaded,
		OriginalBucket:    createdDoc.OriginalBucket,
		OriginalObjectKey: createdDoc.OriginalObjectKey,
		MarkdownBucket:    createdDoc.MarkdownBucket,
		MarkdownObjectKey: createdDoc.MarkdownObjectKey,
		CreatePR:          createdDoc.CreatePR,
		CreatedAt:         createdDoc.CreatedAt,
		UpdatedAt:         createdDoc.UpdatedAt,
	}

	storedDoc := &domain.Document{
		ID:                createdDoc.ID,
		Name:              createdDoc.Name,
		Owner:             createdDoc.Owner,
		Repository:        createdDoc.Repository,
		Branch:            createdDoc.Branch,
		BaseBranch:        createdDoc.BaseBranch,
		Path:              createdDoc.Path,
		Status:            domain.DocumentStatusStoredOriginal,
		OriginalBucket:    createdDoc.OriginalBucket,
		OriginalObjectKey: createdDoc.OriginalObjectKey,
		MarkdownBucket:    createdDoc.MarkdownBucket,
		MarkdownObjectKey: "md/key2",
		CreatePR:          createdDoc.CreatePR,
		CreatedAt:         createdDoc.CreatedAt,
		UpdatedAt:         createdDoc.UpdatedAt,
	}

	svc.On("CreateDocument", mock.Anything, document.CreateDocumentInput{
		ID:                createdDoc.ID,
		FileName:          createdDoc.Name,
		Owner:             createdDoc.Owner,
		Repository:        createdDoc.Repository,
		Branch:            createdDoc.Branch,
		BaseBranch:        createdDoc.BaseBranch,
		Path:              createdDoc.Path,
		UploadBucket:      createdDoc.OriginalBucket,
		UploadObjectKey:   createdDoc.OriginalObjectKey,
		MarkdownBucket:    createdDoc.MarkdownBucket,
		MarkdownObjectKey: createdDoc.MarkdownObjectKey,
		CreatePR:          createdDoc.CreatePR,
	}).Return(createdDoc, nil).Once()

	svc.On("ConfirmUpload", mock.Anything, document.ConfirmUploadInput{
		DocumentID: createdDoc.ID,
		Bucket:     createdDoc.OriginalBucket,
		ObjectKey:  createdDoc.OriginalObjectKey,
	}).Return(confirmedDoc, nil).Once()

	svc.On("UpdateStatus", mock.Anything, document.UpdateStatusInput{
		DocumentID:        createdDoc.ID,
		Status:            string(domain.DocumentStatusStoredOriginal),
		Reason:            "stored",
		ErrorReason:       "stored",
		MarkdownObjectKey: storedDoc.MarkdownObjectKey,
		OriginalObjectKey: storedDoc.OriginalObjectKey,
		Version:           2,
	}).Return(storedDoc, nil).Once()

	svc.On("ListDocuments", mock.Anything, 0, 0, (*domain.DocumentStatus)(nil)).Return([]domain.Document{*storedDoc}, nil).Once()
	svc.On("GetDocument", mock.Anything, createdDoc.ID).Return(storedDoc, nil).Once()

	created, err := client.CreateDocument(ctx, &documentv1.CreateDocumentRequest{
		Id:                createdDoc.ID,
		FileName:          createdDoc.Name,
		Owner:             createdDoc.Owner,
		Repository:        createdDoc.Repository,
		Branch:            createdDoc.Branch,
		BaseBranch:        createdDoc.BaseBranch,
		Path:              createdDoc.Path,
		UploadBucket:      createdDoc.OriginalBucket,
		UploadObjectKey:   createdDoc.OriginalObjectKey,
		MarkdownBucket:    createdDoc.MarkdownBucket,
		MarkdownObjectKey: createdDoc.MarkdownObjectKey,
		CreatePr:          true,
	})
	require.NoError(t, err)
	require.Equal(t, documentv1.DocumentStatus_DOCUMENT_STATUS_UPLOADED, created.Status)

	confirmed, err := client.ConfirmUpload(ctx, &documentv1.ConfirmUploadRequest{
		DocumentId: createdDoc.ID,
		Bucket:     createdDoc.OriginalBucket,
		ObjectKey:  createdDoc.OriginalObjectKey,
	})
	require.NoError(t, err)
	require.Equal(t, createdDoc.ID, confirmed.Id)

	updated, err := client.UpdateDocumentStatus(ctx, &documentv1.UpdateDocumentStatusRequest{
		Id:                createdDoc.ID,
		Status:            documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL,
		Reason:            "stored",
		Version:           2,
		MarkdownObjectKey: storedDoc.MarkdownObjectKey,
		OriginalObjectKey: storedDoc.OriginalObjectKey,
	})
	require.NoError(t, err)
	require.Equal(t, documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL, updated.Status)
	require.Equal(t, storedDoc.MarkdownObjectKey, updated.MarkdownObjectKey)

	listed, err := client.ListDocuments(ctx, &documentv1.ListDocumentsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Documents, 1)
	require.Equal(t, updated.Id, listed.Documents[0].Id)

	fetched, err := client.GetDocument(ctx, &documentv1.GetDocumentRequest{Id: createdDoc.ID})
	require.NoError(t, err)
	require.Equal(t, updated.Id, fetched.Id)
	require.Equal(t, updated.Repository, fetched.Repository)

	svc.AssertExpectations(t)
}
