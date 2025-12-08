package grpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"

	"backend-document-service/internal/model"
	"backend-document-service/internal/service/document"
	"backend-document-service/internal/storage"
	documentv1 "backend-document-service/proto/backend/document/v1"

	"github.com/google/uuid"
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
	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(func(ctx context.Context, s string) (net.Conn, error) {
		return lis.Dial()
	}), grpc.WithInsecure())
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		conn.Close()
		server.Stop()
	}
	return conn, cleanup, nil
}

type staticKey struct{}

func (staticKey) GenerateKeys(ctx context.Context) (storage.KeyPair, error) {
	return storage.KeyPair{OriginalObjectKey: "orig/key", MarkdownObjectKey: "md/key"}, nil
}

type memoryRepo struct {
	mu        sync.Mutex
	documents map[string]model.Document
}

func (m *memoryRepo) CreateDocument(ctx context.Context, doc *model.Document, version *model.DocumentVersion, log *model.DocumentStatusLog) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.documents == nil {
		m.documents = make(map[string]model.Document)
	}
	doc.ID = uuid.NewString()
	doc.Versions = []model.DocumentVersion{*version}
	m.documents[doc.ID] = *doc
	return doc, nil
}

func (m *memoryRepo) GetDocument(ctx context.Context, id string) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &doc, nil
}

func (m *memoryRepo) ListDocuments(ctx context.Context, limit, offset int, status *model.Status) ([]model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []model.Document
	for _, d := range m.documents {
		if status != nil && d.Status != *status {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}

func (m *memoryRepo) UpdateStatus(ctx context.Context, id string, to model.Status, reason, errorReason string, version *model.DocumentVersion) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	if err := model.ValidateTransition(doc.Status, to); err != nil {
		return nil, err
	}
	doc.Status = to
	if errorReason != "" {
		doc.ErrorReason = errorReason
	}
	if version != nil {
		doc.Versions = append(doc.Versions, *version)
		doc.MarkdownObjectKey = version.MarkdownObjectKey
	}
	m.documents[id] = doc
	return &doc, nil
}

func (m *memoryRepo) UpdateWorkflowID(ctx context.Context, id, workflowID string) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	doc, ok := m.documents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	doc.WorkflowID = workflowID
	m.documents[id] = doc
	return &doc, nil
}

func TestGRPCWorkflow(t *testing.T) {
	repo := &memoryRepo{}
	svc := document.New(repo, staticKey{}, nil, "", nil, "orig", "md", "owner", "repo", "main", "main")
	srv := NewServer(svc)
	grpcServer := grpc.NewServer()
	documentv1.RegisterDocumentServiceServer(grpcServer, srv)

	conn, cleanup, err := dialer(grpcServer)
	require.NoError(t, err)
	defer cleanup()

	client := documentv1.NewDocumentServiceClient(conn)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("x-correlation-id", "test-corr"))

	created, err := client.CreateDocument(ctx, &documentv1.CreateDocumentRequest{FileName: "doc", Repository: "repo/name", Branch: "main", BaseBranch: "main", Path: "docs/doc.md", UploadBucket: "orig", MarkdownBucket: "md", Owner: "owner"})
	require.NoError(t, err)
	require.Equal(t, documentv1.DocumentStatus_DOCUMENT_STATUS_UPLOADED, created.Status)
	require.Equal(t, "repo/name", created.Repository)
	require.Equal(t, "main", created.Branch)
	require.Equal(t, "owner", created.Owner)
	require.Equal(t, "docs/doc.md", created.Path)

	updated, err := client.UpdateDocumentStatus(ctx, &documentv1.UpdateDocumentStatusRequest{Id: created.Id, Status: documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL, Version: 2, MarkdownObjectKey: "md/key2"})
	require.NoError(t, err)
	require.Equal(t, documentv1.DocumentStatus_DOCUMENT_STATUS_STORED_ORIGINAL, updated.Status)

	listed, err := client.ListDocuments(ctx, &documentv1.ListDocumentsRequest{})
	require.NoError(t, err)
	require.Len(t, listed.Documents, 1)

	fetched, err := client.GetDocument(ctx, &documentv1.GetDocumentRequest{Id: created.Id})
	require.NoError(t, err)
	require.Equal(t, created.Id, fetched.Id)
	require.Equal(t, created.Repository, fetched.Repository)
	require.Equal(t, created.Path, fetched.Path)
}
