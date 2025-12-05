package document

import (
	"context"
	"net"
	"sync"
	"testing"

	"backend-document-service/internal/model"
	githubpublisherv1 "backend-document-service/proto/backend/githubpublisher/v1"
	storagev1 "backend-document-service/proto/backend/storage/v1"
	transformerv1 "backend-document-service/proto/backend/transformer/v1"

	"github.com/stretchr/testify/require"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

const bufSize = 1024 * 1024

func TestDocumentProcessingWorkflow_E2E(t *testing.T) {
	correlationID := "corr-123"
	header := &commonpb.Header{Fields: map[string]*commonpb.Payload{}}
	encodedID, err := converter.GetDefaultDataConverter().ToPayload([]byte(correlationID))
	require.NoError(t, err)
	header.Fields[CorrelationIDHeader] = encodedID

	propagator := NewCorrelationIDPropagator()
	suite := &testsuite.WorkflowTestSuite{}
	env := suite.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{propagator})
	env.SetHeader(header)

	storageMeta := &metadataLog{}
	storageConn, storageServer := startBufConnServer(t, func(server *grpc.Server) {
		storagev1.RegisterStorageServiceServer(server, &stubStorageServer{metadata: storageMeta})
	})
	t.Cleanup(func() { _ = storageConn.Close() })
	transformerMeta := &metadataLog{}
	transformerConn, transformerServer := startBufConnServer(t, func(server *grpc.Server) {
		transformerv1.RegisterTransformerServiceServer(server, &stubTransformerServer{metadata: transformerMeta})
	})
	t.Cleanup(func() { _ = transformerConn.Close() })
	publisherMeta := &metadataLog{}
	publisherConn, publisherServer := startBufConnServer(t, func(server *grpc.Server) {
		githubpublisherv1.RegisterGithubPublisherServiceServer(server, &stubPublisherServer{metadata: publisherMeta})
	})
	t.Cleanup(func() { _ = publisherConn.Close() })

	defer storageServer.Stop()
	defer transformerServer.Stop()
	defer publisherServer.Stop()

	repo := &memoryRepository{}
	activities := NewActivities(repo, storagev1.NewStorageServiceClient(storageConn), transformerv1.NewTransformerServiceClient(transformerConn), githubpublisherv1.NewGithubPublisherServiceClient(publisherConn), "repo", "main")

	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: "doc-1", TaskQueue: "document-processing"})
	env.RegisterWorkflow(DocumentProcessingWorkflow)
	env.RegisterActivityWithOptions(activities.UploadOriginal, activity.RegisterOptions{Name: ActivityUploadOriginalName})
	env.RegisterActivityWithOptions(activities.ConvertToMarkdown, activity.RegisterOptions{Name: ActivityConvertToMarkdownName})
	env.RegisterActivityWithOptions(activities.PublishMarkdown, activity.RegisterOptions{Name: ActivityPublishMarkdownName})

	input := WorkflowInput{
		DocumentID:      "doc-1",
		FilenameExt:     "docx",
		OriginalContent: []byte("content"),
		PublishPath:     "docs/doc-1.md",
		Version:         1,
	}
	env.ExecuteWorkflow(DocumentProcessingWorkflow, input)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	require.Equal(t, []model.Status{model.StatusStoredOriginal, model.StatusConvertedToMarkdown, model.StatusPublishedToGitHub}, repo.statuses())
	require.Equal(t, []string{correlationID, correlationID, correlationID}, repo.correlationIDs())
	require.Equal(t, []string{correlationID, correlationID}, storageMeta.values)
	require.Equal(t, []string{correlationID}, transformerMeta.values)
	require.Equal(t, []string{correlationID}, publisherMeta.values)
}

type metadataLog struct {
	values []string
	mu     sync.Mutex
}

func (m *metadataLog) append(ctx context.Context) {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get(CorrelationIDHeader); len(vals) > 0 {
			m.mu.Lock()
			m.values = append(m.values, vals[0])
			m.mu.Unlock()
		}
	}
}

type stubStorageServer struct {
	storagev1.UnimplementedStorageServiceServer
	metadata *metadataLog
}

func (s *stubStorageServer) UploadOriginal(ctx context.Context, _ *storagev1.UploadOriginalRequest) (*storagev1.UploadObjectResponse, error) {
	s.metadata.append(ctx)
	return &storagev1.UploadObjectResponse{Bucket: "orig", ObjectKey: "orig-key"}, nil
}

func (s *stubStorageServer) UploadMarkdown(ctx context.Context, _ *storagev1.UploadMarkdownRequest) (*storagev1.UploadObjectResponse, error) {
	s.metadata.append(ctx)
	return &storagev1.UploadObjectResponse{Bucket: "md", ObjectKey: "md-key"}, nil
}

type stubTransformerServer struct {
	transformerv1.UnimplementedTransformerServiceServer
	metadata *metadataLog
}

func (s *stubTransformerServer) ConvertDocxToMarkdown(ctx context.Context, _ *transformerv1.ConvertDocxToMarkdownRequest) (*transformerv1.ConvertDocxToMarkdownResponse, error) {
	s.metadata.append(ctx)
	return &transformerv1.ConvertDocxToMarkdownResponse{MarkdownContent: []byte("# header"), MediaType: "text/markdown"}, nil
}

type stubPublisherServer struct {
	githubpublisherv1.UnimplementedGithubPublisherServiceServer
	metadata *metadataLog
}

func (s *stubPublisherServer) PublishMarkdown(ctx context.Context, _ *githubpublisherv1.PublishMarkdownRequest) (*githubpublisherv1.PublishMarkdownResponse, error) {
	s.metadata.append(ctx)
	return &githubpublisherv1.PublishMarkdownResponse{CommitSha: "sha", Url: "https://example.com"}, nil
}

type memoryRepository struct {
	mu     sync.Mutex
	states []model.Status
	ids    []string
}

func (m *memoryRepository) CreateDocument(_ context.Context, _ *model.Document, _ *model.DocumentVersion, _ *model.DocumentStatusLog) (*model.Document, error) {
	return nil, nil
}

func (m *memoryRepository) GetDocument(_ context.Context, _ string) (*model.Document, error) {
	return nil, nil
}

func (m *memoryRepository) ListDocuments(_ context.Context, _ int, _ int, _ *model.Status) ([]model.Document, error) {
	return nil, nil
}

func (m *memoryRepository) UpdateStatus(ctx context.Context, _ string, to model.Status, _ string, _ *model.DocumentVersion) (*model.Document, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, to)
	m.ids = append(m.ids, CorrelationIDFromContext(ctx))
	return &model.Document{Status: to}, nil
}

func (m *memoryRepository) statuses() []model.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	copyStates := make([]model.Status, len(m.states))
	copy(copyStates, m.states)
	return copyStates
}

func (m *memoryRepository) correlationIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	copyIDs := make([]string, len(m.ids))
	copy(copyIDs, m.ids)
	return copyIDs
}

func startBufConnServer(t *testing.T, register func(server *grpc.Server)) (*grpc.ClientConn, *grpc.Server) {
	listener := bufconn.Listen(bufSize)
	server := grpc.NewServer()
	register(server)

	go func() {
		_ = server.Serve(listener)
	}()

	dialer := func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}

	conn, err := grpc.DialContext(context.Background(), "bufnet", grpc.WithContextDialer(dialer), grpc.WithInsecure(), grpc.WithBlock())
	require.NoError(t, err)

	return conn, server
}
