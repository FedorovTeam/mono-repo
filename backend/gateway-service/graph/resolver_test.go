package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"backend-gateway-service/graph/generated"
	"backend-gateway-service/graph/model"
	storageclient "backend-gateway-service/internal/clients/storage"
	"backend-gateway-service/internal/clients/temporal"
	"backend-gateway-service/internal/ctxutils"
	"backend-gateway-service/internal/middleware"
	pb "backend-gateway-service/proto/backend/document/v1"
	storagepb "backend-gateway-service/proto/backend/storage/v1"
)

type mockDocumentClient struct {
	createFunc func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error)
	getFunc    func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error)
	listFunc   func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error)
	retryFunc  func(ctx context.Context, in *pb.RetryProcessingRequest) (*pb.Document, error)
}

func (m *mockDocumentClient) CreateDocument(ctx context.Context, in *pb.CreateDocumentRequest, _ ...grpc.CallOption) (*pb.Document, error) {
	return m.createFunc(ctx, in)
}

func (m *mockDocumentClient) GetDocument(ctx context.Context, in *pb.GetDocumentRequest, _ ...grpc.CallOption) (*pb.Document, error) {
	return m.getFunc(ctx, in)
}

func (m *mockDocumentClient) ListDocuments(ctx context.Context, in *pb.ListDocumentsRequest, _ ...grpc.CallOption) (*pb.ListDocumentsResponse, error) {
	return m.listFunc(ctx, in)
}

func (m *mockDocumentClient) RetryProcessing(ctx context.Context, in *pb.RetryProcessingRequest, _ ...grpc.CallOption) (*pb.Document, error) {
	return m.retryFunc(ctx, in)
}

func (m *mockDocumentClient) Close() error { return nil }

type mockTemporalClient struct {
	startFunc func(ctx context.Context, input temporal.UploadWorkflowInput) (string, error)
}

func (m *mockTemporalClient) StartUploadWorkflow(ctx context.Context, input temporal.UploadWorkflowInput) (string, error) {
	return m.startFunc(ctx, input)
}

func (m *mockTemporalClient) Close() {}

type mockStorageClient struct {
	generateFunc func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error)
}

func (m *mockStorageClient) GenerateUploadURL(ctx context.Context, in *storagepb.GenerateUploadURLRequest, _ ...grpc.CallOption) (*storagepb.GenerateUploadURLResponse, error) {
	return m.generateFunc(ctx, in)
}

func (m *mockStorageClient) Close() error { return nil }

func TestUploadValidation(t *testing.T) {
	resolver := &Resolver{DocumentClient: &mockDocumentClient{}, TemporalClient: &mockTemporalClient{}, StorageClient: &mockStorageClient{}, UploadConfig: UploadValidationConfig{MaxSizeBytes: 1024, AllowedExtensions: []string{"docx"}}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	_, err := mr.UploadKnowledgeDocument(context.Background(), model.UploadKnowledgeDocumentInput{})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestUploadHappyPath(t *testing.T) {
	ctx := ctxutils.WithCorrelationID(context.Background(), "corr-test")
	docCalled := false
	temporalCalled := false

	docClient := &mockDocumentClient{createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
		docCalled = true
		if in.CorrelationId != "corr-test" {
			t.Fatalf("correlation id not propagated")
		}
		if in.ContentLength != 1024 {
			t.Fatalf("unexpected content length: %d", in.ContentLength)
		}
		return &pb.Document{Id: "123", FileName: in.FileName, Repository: in.Repository, Branch: in.Branch, Path: in.Path, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
	}, retryFunc: func(ctx context.Context, in *pb.RetryProcessingRequest) (*pb.Document, error) { return nil, nil }, getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil }, listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
		return nil, nil
	}}

	storageCalled := false
	storageClient := &mockStorageClient{generateFunc: func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
		storageCalled = true
		if in.CorrelationId != "corr-test" {
			t.Fatalf("storage correlation id missing")
		}
		if in.ExpiresInSeconds != 600 {
			t.Fatalf("unexpected ttl: %d", in.ExpiresInSeconds)
		}
		return &storagepb.GenerateUploadURLResponse{UploadUrl: "http://minio/upload", Bucket: "docs", ObjectKey: "docs/123.docx", ExpiresInSeconds: 900}, nil
	}}

	temporalClient := &mockTemporalClient{startFunc: func(ctx context.Context, input temporal.UploadWorkflowInput) (string, error) {
		temporalCalled = true
		if input.CorrelationId != "corr-test" {
			t.Fatalf("workflow correlation id missing")
		}
		if input.DocumentId != "123" || input.UploadBucket != "docs" || input.UploadObjectKey != "docs/123.docx" || input.UploadURL == "" || input.FileSizeBytes != 1024 {
			t.Fatalf("unexpected workflow input: %+v", input)
		}
		return "wf-1", nil
	}}

	resolver := &Resolver{DocumentClient: docClient, TemporalClient: temporalClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{MaxSizeBytes: 2 * 1024 * 1024, AllowedExtensions: []string{"docx"}, SignedURLTTLSeconds: 600}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	payload, err := mr.UploadKnowledgeDocument(ctx, model.UploadKnowledgeDocumentInput{
		FileName:      "demo.docx",
		FileSizeBytes: 1024,
		Repository:    "org/repo",
		Branch:        "main",
		Path:          "docs/demo.md",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !docCalled || !temporalCalled || !storageCalled {
		t.Fatalf("expected downstream calls")
	}
	if payload.Document.WorkflowID == nil || *payload.Document.WorkflowID != "wf-1" {
		t.Fatalf("workflow id not returned")
	}
	if payload.UploadURL == "" || payload.UploadMethod == "" {
		t.Fatalf("payload missing upload info: %+v", payload)
	}
}

func TestKnowledgeDocumentsPaginationAndFilter(t *testing.T) {
	qr := &queryResolver{&Resolver{Logger: zap.NewNop(), DocumentClient: &mockDocumentClient{
		listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
			if in.FilterByStatus != true || in.StatusFilter != pb.DocumentStatus_DOCUMENT_STATUS_FAILED {
				t.Fatalf("status filter not propagated")
			}
			if in.Limit != 5 || in.Offset != 2 {
				t.Fatalf("pagination not applied")
			}
			return &pb.ListDocumentsResponse{Documents: []*pb.Document{{Id: "1", Status: pb.DocumentStatus_DOCUMENT_STATUS_FAILED}}, TotalCount: 1}, nil
		},
		getFunc:    func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil },
		createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) { return nil, nil },
		retryFunc:  func(ctx context.Context, in *pb.RetryProcessingRequest) (*pb.Document, error) { return nil, nil },
	}}}

	limit := 5
	offset := 2
	status := model.KnowledgeDocumentStatusFailed
	conn, err := qr.KnowledgeDocuments(context.Background(), &model.KnowledgeDocumentFilter{Status: &status}, &model.Pagination{Limit: &limit, Offset: &offset})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(conn.Nodes) != 1 || conn.TotalCount != 1 {
		t.Fatalf("unexpected connection result: %+v", conn)
	}
}

func TestGraphQLUploadEndToEnd(t *testing.T) {
	logger := zap.NewNop()
	docClient := &mockDocumentClient{
		createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
			return &pb.Document{Id: "doc-1", FileName: in.FileName, Repository: in.Repository, Branch: in.Branch, Path: in.Path, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
		},
		getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil },
		listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
			return &pb.ListDocumentsResponse{}, nil
		},
		retryFunc: func(ctx context.Context, in *pb.RetryProcessingRequest) (*pb.Document, error) { return nil, nil },
	}
	temporalClient := &mockTemporalClient{startFunc: func(ctx context.Context, input temporal.UploadWorkflowInput) (string, error) {
		if input.CorrelationId != "corr-integration" {
			t.Fatalf("correlation id missing in workflow")
		}
		return "wf-99", nil
	}}

	storageClient := &mockStorageClient{generateFunc: func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
		if in.CorrelationId != "corr-integration" {
			t.Fatalf("correlation id missing in storage")
		}
		if in.ExpiresInSeconds != 900 {
			t.Fatalf("unexpected expires ttl: %d", in.ExpiresInSeconds)
		}
		return &storagepb.GenerateUploadURLResponse{UploadUrl: "http://minio/signed", Bucket: "docx", ObjectKey: "docx/doc-1.docx", ExpiresInSeconds: 600}, nil
	}}

	resolver := &Resolver{DocumentClient: docClient, TemporalClient: temporalClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{MaxSizeBytes: 1024 * 1024, AllowedExtensions: []string{"docx"}, SignedURLTTLSeconds: 900}, Logger: logger}
	gqlServer := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	handlerWithMiddleware := middleware.CorrelationIDMiddleware(logger)(middleware.APIKeyMiddleware("secret", logger)(gqlServer))

	reqBody := map[string]interface{}{
		"query": "mutation($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document{ id status workflowId } } }",
		"variables": map[string]interface{}{
			"input": map[string]interface{}{
				"fileName":      "demo.docx",
				"fileSizeBytes": 2048,
				"repository":    "org/repo",
				"branch":        "main",
				"path":          "docs/demo.md",
			},
		},
	}
	payload, _ := json.Marshal(reqBody)
	r := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBuffer(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("X-Correlation-ID", "corr-integration")

	rr := httptest.NewRecorder()
	handlerWithMiddleware.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		Data struct {
			UploadKnowledgeDocument struct {
				UploadURL    string `json:"uploadUrl"`
				UploadMethod string `json:"uploadMethod"`
				Document     struct {
					ID         string  `json:"id"`
					Status     string  `json:"status"`
					WorkflowID *string `json:"workflowId"`
				} `json:"document"`
			} `json:"uploadKnowledgeDocument"`
		} `json:"data"`
		Errors []interface{} `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot unmarshal response: %v", err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected graphql errors: %+v", resp.Errors)
	}
	if resp.Data.UploadKnowledgeDocument.Document.ID != "doc-1" || resp.Data.UploadKnowledgeDocument.Document.WorkflowID == nil || *resp.Data.UploadKnowledgeDocument.Document.WorkflowID != "wf-99" {
		t.Fatalf("unexpected response: %+v", resp.Data.UploadKnowledgeDocument)
	}
	if resp.Data.UploadKnowledgeDocument.UploadURL == "" || resp.Data.UploadKnowledgeDocument.UploadMethod == "" {
		t.Fatalf("upload url not returned: %+v", resp.Data.UploadKnowledgeDocument)
	}
}

func TestGraphQLUploadWithStorageGRPC(t *testing.T) {
	lis := bufconn.Listen(1024 * 1024)
	storageServer := &storageServiceStub{t: t}
	grpcServer := grpc.NewServer()
	storagepb.RegisterStorageServiceServer(grpcServer, storageServer)

	go func() {
		_ = grpcServer.Serve(lis)
	}()
	t.Cleanup(func() {
		grpcServer.Stop()
		lis.Close()
	})

	dialer := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.Dial()
	}

	storageClient, err := storageclient.NewGRPCClient("bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("cannot create storage client: %v", err)
	}
	t.Cleanup(func() { _ = storageClient.Close() })

	logger := zap.NewNop()
	docClient := &mockDocumentClient{
		createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
			return &pb.Document{Id: "doc-1", FileName: in.FileName, Repository: in.Repository, Branch: in.Branch, Path: in.Path, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
		},
		getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil },
		listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
			return &pb.ListDocumentsResponse{}, nil
		},
		retryFunc: func(ctx context.Context, in *pb.RetryProcessingRequest) (*pb.Document, error) { return nil, nil },
	}
	temporalClient := &mockTemporalClient{startFunc: func(ctx context.Context, input temporal.UploadWorkflowInput) (string, error) {
		if input.CorrelationId != "corr-grpc" {
			t.Fatalf("correlation id missing in workflow")
		}
		if input.UploadURL != "http://minio/presigned" {
			t.Fatalf("unexpected upload url: %s", input.UploadURL)
		}
		return "wf-42", nil
	}}

	resolver := &Resolver{DocumentClient: docClient, TemporalClient: temporalClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{MaxSizeBytes: 1024 * 1024, AllowedExtensions: []string{"docx"}, SignedURLTTLSeconds: 900}, Logger: logger}
	gqlServer := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	handlerWithMiddleware := middleware.CorrelationIDMiddleware(logger)(middleware.APIKeyMiddleware("secret", logger)(gqlServer))

	reqBody := map[string]interface{}{
		"query": "mutation($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document{ id status workflowId } } }",
		"variables": map[string]interface{}{
			"input": map[string]interface{}{
				"fileName":      "demo.docx",
				"fileSizeBytes": 2048,
				"repository":    "org/repo",
				"branch":        "main",
				"path":          "docs/demo.md",
			},
		},
	}
	payload, _ := json.Marshal(reqBody)
	r := httptest.NewRequest(http.MethodPost, "/query", bytes.NewBuffer(payload))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-API-Key", "secret")
	r.Header.Set("X-Correlation-ID", "corr-grpc")

	rr := httptest.NewRecorder()
	handlerWithMiddleware.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var resp struct {
		Data struct {
			UploadKnowledgeDocument struct {
				UploadURL    string `json:"uploadUrl"`
				UploadMethod string `json:"uploadMethod"`
				Document     struct {
					ID         string  `json:"id"`
					Status     string  `json:"status"`
					WorkflowID *string `json:"workflowId"`
				} `json:"document"`
			} `json:"uploadKnowledgeDocument"`
		} `json:"data"`
		Errors []interface{} `json:"errors"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("cannot unmarshal response: %v", err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected graphql errors: %+v", resp.Errors)
	}
	if storageServer.calls != 1 {
		t.Fatalf("storage service should be called once, got %d", storageServer.calls)
	}
	if resp.Data.UploadKnowledgeDocument.Document.ID != "doc-1" || resp.Data.UploadKnowledgeDocument.UploadURL != "http://minio/presigned" {
		t.Fatalf("unexpected response: %+v", resp.Data.UploadKnowledgeDocument)
	}
	if storageServer.lastCorrelation != "corr-grpc" {
		t.Fatalf("correlation id not forwarded to storage service: %s", storageServer.lastCorrelation)
	}
}

type storageServiceStub struct {
	storagepb.UnimplementedStorageServiceServer
	t               *testing.T
	calls           int
	lastCorrelation string
}

func (s *storageServiceStub) GenerateUploadURL(ctx context.Context, req *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
	s.calls++
	s.lastCorrelation = req.GetCorrelationId()

	if req.GetDocumentId() == "" {
		s.t.Fatalf("document id is required")
	}
	if req.GetExpiresInSeconds() != 900 {
		s.t.Fatalf("unexpected ttl: %d", req.GetExpiresInSeconds())
	}

	md, _ := metadata.FromIncomingContext(ctx)
	if cid := md.Get("x-correlation-id"); len(cid) == 0 || cid[0] != req.GetCorrelationId() {
		s.t.Fatalf("correlation id not propagated in metadata")
	}

	return &storagepb.GenerateUploadURLResponse{
		UploadUrl:        "http://minio/presigned",
		Bucket:           "docx",
		ObjectKey:        "docx/" + req.GetDocumentId() + ".docx",
		ExpiresInSeconds: req.GetExpiresInSeconds(),
	}, nil
}
