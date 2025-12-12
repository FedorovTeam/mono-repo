package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/99designs/gqlgen/graphql/handler"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"

	"backend-gateway-service/graph/generated"
	"backend-gateway-service/graph/model"
	storageclient "backend-gateway-service/internal/clients/storage"
	"backend-gateway-service/internal/ctxutils"
	"backend-gateway-service/internal/middleware"
	pb "backend-gateway-service/proto/backend/document/v1"
	storagepb "backend-gateway-service/proto/backend/storage/v1"
)

type mockDocumentClient struct {
	createFunc  func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error)
	getFunc     func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error)
	listFunc    func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error)
	confirmFunc func(ctx context.Context, in *pb.ConfirmUploadRequest) (*pb.Document, error)
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

func (m *mockDocumentClient) ConfirmUpload(ctx context.Context, in *pb.ConfirmUploadRequest, _ ...grpc.CallOption) (*pb.Document, error) {
	if m.confirmFunc == nil {
		return nil, nil
	}
	return m.confirmFunc(ctx, in)
}

func (m *mockDocumentClient) Close() error { return nil }

type mockStorageClient struct {
	generateFunc func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error)
}

func (m *mockStorageClient) GenerateUploadURL(ctx context.Context, in *storagepb.GenerateUploadURLRequest, _ ...grpc.CallOption) (*storagepb.GenerateUploadURLResponse, error) {
	return m.generateFunc(ctx, in)
}

func (m *mockStorageClient) Close() error { return nil }

func TestUploadValidation(t *testing.T) {
	resolver := &Resolver{DocumentClient: &mockDocumentClient{}, StorageClient: &mockStorageClient{}, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	_, err := mr.UploadKnowledgeDocument(ctxutils.WithAPIKey(context.Background(), "secret"), model.UploadKnowledgeDocumentInput{})
	if err == nil {
		t.Fatalf("expected validation error")
	}
}

func TestUploadDelegatesValidationToDocumentService(t *testing.T) {
	ctx := ctxutils.WithAPIKey(ctxutils.WithCorrelationID(context.Background(), "corr-validate"), "secret")

	storageClient := &mockStorageClient{generateFunc: func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
		return &storagepb.GenerateUploadURLResponse{UploadUrl: "http://upload", Bucket: "docs", ObjectKey: "docs/file.docx", MarkdownBucket: "markdown", MarkdownObjectKey: "markdown/file.md", ExpiresInSeconds: 600}, nil
	}}

	docClient := &mockDocumentClient{createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
		return nil, fmt.Errorf("document-service rejected input: repository must be in owner/repo format")
	}, getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) {
		return nil, nil
	}, listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
		return nil, nil
	}}

	resolver := &Resolver{DocumentClient: docClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	_, err := mr.UploadKnowledgeDocument(ctx, model.UploadKnowledgeDocumentInput{
		FileName:      "invalid.docx",
		FileSizeBytes: 10,
		Repository:    "owner/repo/extra",
		Branch:        "main",
		Path:          "docs/invalid.md",
	})
	if err == nil {
		t.Fatalf("expected error from document-service")
	}
	if !strings.Contains(err.Error(), "document-service rejected input") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUploadHappyPath(t *testing.T) {
	ctx := ctxutils.WithAPIKey(ctxutils.WithCorrelationID(context.Background(), "corr-test"), "secret")
	docCalled := false
	storageDocID := ""
	storageCalled := false

	storageClient := &mockStorageClient{generateFunc: func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
		storageCalled = true
		storageDocID = in.GetDocumentId()
		if in.CorrelationId != "corr-test" {
			t.Fatalf("storage correlation id missing")
		}
		if in.ExpiresInSeconds != 600 {
			t.Fatalf("unexpected ttl: %d", in.ExpiresInSeconds)
		}
		if storageDocID == "" {
			t.Fatalf("document id is empty in storage request")
		}
		return &storagepb.GenerateUploadURLResponse{UploadUrl: "http://minio/upload", Bucket: "docs", ObjectKey: fmt.Sprintf("docs/%s.docx", storageDocID), MarkdownBucket: "markdown", MarkdownObjectKey: fmt.Sprintf("markdown/%s.md", storageDocID), ExpiresInSeconds: 900}, nil
	}}

	docClient := &mockDocumentClient{createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
		docCalled = true
		if in.CorrelationId != "corr-test" {
			t.Fatalf("correlation id not propagated")
		}
		if in.Repository != "org/repo" {
			t.Fatalf("repository should be forwarded as-is: %s", in.Repository)
		}
		if in.RepositoryOwner != "" {
			t.Fatalf("repository owner should be empty in gateway request")
		}
		if in.ContentLength != 1024 {
			t.Fatalf("unexpected content length: %d", in.ContentLength)
		}
		if in.GetId() != storageDocID {
			t.Fatalf("document id mismatch: %s vs %s", in.GetId(), storageDocID)
		}
		if in.GetUploadBucket() != "docs" || in.GetUploadObjectKey() == "" {
			t.Fatalf("bucket info not forwarded")
		}
		return &pb.Document{Id: in.GetId(), WorkflowId: in.GetId(), JobId: in.GetId(), FileName: in.FileName, Repository: in.Repository, RepositoryOwner: in.RepositoryOwner, Branch: in.Branch, Path: in.Path, UploadBucket: in.UploadBucket, UploadObjectKey: in.UploadObjectKey, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED, ActualStatus: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
	}, getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil }, listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
		return nil, nil
	}}

	resolver := &Resolver{DocumentClient: docClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 600}, Logger: zap.NewNop()}
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
	if !docCalled || !storageCalled {
		t.Fatalf("expected downstream calls")
	}
	if payload.Document.JobID == nil || *payload.Document.JobID != storageDocID {
		t.Fatalf("job id not returned")
	}
	if payload.UploadURL == "" || payload.UploadMethod == "" {
		t.Fatalf("payload missing upload info: %+v", payload)
	}
	if payload.Document.Repository != "org/repo" {
		t.Fatalf("unexpected repository in payload: %s", payload.Document.Repository)
	}
	if payload.Document.RepositoryOwner != nil {
		t.Fatalf("repository owner should be absent when validation is delegated")
	}
}

func TestUploadFailsOnIncompleteStorageResponse(t *testing.T) {
	tests := []struct {
		name    string
		resp    *storagepb.GenerateUploadURLResponse
		missing []string
	}{
		{
			name: "missing upload bucket",
			resp: &storagepb.GenerateUploadURLResponse{
				UploadUrl:         "http://minio/upload",
				ObjectKey:         "docs/file.docx",
				MarkdownBucket:    "md",
				MarkdownObjectKey: "md/file.md",
				ExpiresInSeconds:  600,
			},
			missing: []string{"bucket"},
		},
		{
			name: "missing markdown paths",
			resp: &storagepb.GenerateUploadURLResponse{
				UploadUrl:        "http://minio/upload",
				Bucket:           "docs",
				ObjectKey:        "docs/file.docx",
				ExpiresInSeconds: 600,
			},
			missing: []string{"markdown_bucket", "markdown_object_key"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storageCalled := false
			docCalled := false
			core, logs := observer.New(zapcore.DebugLevel)
			logger := zap.New(core)

			storageClient := &mockStorageClient{generateFunc: func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
				storageCalled = true
				return tt.resp, nil
			}}

			docClient := &mockDocumentClient{
				createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
					docCalled = true
					t.Fatalf("CreateDocument should not be called when storage response is incomplete")
					return nil, nil
				},
				getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) {
					return nil, nil
				},
				listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
					return nil, nil
				},
			}

			resolver := &Resolver{DocumentClient: docClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{MaxSizeBytes: 1024 * 1024, AllowedExtensions: []string{"docx"}, SignedURLTTLSeconds: 600}, Logger: logger}
			mr := &mutationResolver{resolver}

			_, err := mr.UploadKnowledgeDocument(ctxutils.WithAPIKey(context.Background(), "secret"), model.UploadKnowledgeDocumentInput{
				FileName:      "demo.docx",
				FileSizeBytes: 512,
				Repository:    "org/repo",
				Branch:        "main",
				Path:          "docs/demo.md",
			})
			if err == nil {
				t.Fatalf("expected error for incomplete storage response")
			}
			for _, field := range tt.missing {
				if !strings.Contains(err.Error(), field) {
					t.Fatalf("expected error to mention missing %s: %v", field, err)
				}
			}
			if !storageCalled {
				t.Fatalf("storage service should be called")
			}
			if docCalled {
				t.Fatalf("document service should not be called")
			}
			foundLog := false
			for _, entry := range logs.All() {
				if entry.Message == "storage service returned incomplete upload data" {
					foundLog = true
					break
				}
			}
			if !foundLog {
				t.Fatalf("expected log entry for incomplete storage response")
			}
		})
	}
}

func TestUploadGeneratesCorrelationIDWhenMissing(t *testing.T) {
	var capturedCorrelation string
	var capturedDocID string

	storageClient := &mockStorageClient{generateFunc: func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
		if in.CorrelationId == "" {
			t.Fatalf("correlation id was not generated")
		}
		capturedCorrelation = in.CorrelationId
		capturedDocID = in.GetDocumentId()
		return &storagepb.GenerateUploadURLResponse{UploadUrl: "http://minio/upload", Bucket: "docs", ObjectKey: fmt.Sprintf("docs/%s.docx", capturedDocID), MarkdownBucket: "markdown", MarkdownObjectKey: fmt.Sprintf("markdown/%s.md", capturedDocID), ExpiresInSeconds: 600}, nil
	}}

	docClient := &mockDocumentClient{createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
		if in.CorrelationId != capturedCorrelation {
			t.Fatalf("correlation id was not propagated")
		}
		if in.GetId() != capturedDocID {
			t.Fatalf("document id mismatch: %s vs %s", in.GetId(), capturedDocID)
		}
		if in.Repository != "org/repo" {
			t.Fatalf("repository should not be normalized: %s", in.Repository)
		}
		if in.RepositoryOwner != "" {
			t.Fatalf("repository owner must remain empty: %s", in.RepositoryOwner)
		}
		return &pb.Document{Id: capturedDocID, WorkflowId: capturedDocID, JobId: capturedDocID, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED, ActualStatus: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
	}, getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) {
		return nil, nil
	}, listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
		return nil, nil
	}}

	resolver := &Resolver{DocumentClient: docClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 600}, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	payload, err := mr.UploadKnowledgeDocument(ctxutils.WithAPIKey(context.Background(), "secret"), model.UploadKnowledgeDocumentInput{
		FileName:      "new.docx",
		FileSizeBytes: 512,
		Repository:    "org/repo",
		Branch:        "main",
		Path:          "docs/new.md",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload.Document == nil || payload.Document.ID != capturedDocID {
		t.Fatalf("unexpected document returned: %+v", payload.Document)
	}
	if capturedCorrelation == "" {
		t.Fatalf("correlation id was not captured")
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
	var capturedDocID string
	docClient := &mockDocumentClient{
		createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
			if in.GetId() == "" {
				t.Fatalf("document id not provided")
			}
			if capturedDocID == "" {
				capturedDocID = in.GetId()
			}
			if in.GetId() != capturedDocID {
				t.Fatalf("document id mismatch: %s vs %s", in.GetId(), capturedDocID)
			}
			return &pb.Document{Id: in.GetId(), WorkflowId: in.GetId(), JobId: in.GetId(), FileName: in.FileName, Repository: in.Repository, Branch: in.Branch, Path: in.Path, UploadBucket: in.UploadBucket, UploadObjectKey: in.UploadObjectKey, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED, ActualStatus: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
		},
		getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil },
		listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
			return &pb.ListDocumentsResponse{}, nil
		},
	}
	storageClient := &mockStorageClient{generateFunc: func(ctx context.Context, in *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
		if in.CorrelationId != "corr-integration" {
			t.Fatalf("correlation id missing in storage")
		}
		if in.ExpiresInSeconds != 900 {
			t.Fatalf("unexpected expires ttl: %d", in.ExpiresInSeconds)
		}
		capturedDocID = in.GetDocumentId()
		return &storagepb.GenerateUploadURLResponse{UploadUrl: "http://minio/signed", Bucket: "docx", ObjectKey: fmt.Sprintf("docx/%s.docx", capturedDocID), MarkdownBucket: "markdown", MarkdownObjectKey: fmt.Sprintf("markdown/%s.md", capturedDocID), ExpiresInSeconds: 600}, nil
	}}

	resolver := &Resolver{DocumentClient: docClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: logger}
	gqlServer := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	handlerWithMiddleware := middleware.CorrelationIDMiddleware(logger)(middleware.APIKeyMiddleware("secret", logger)(gqlServer))

	reqBody := map[string]interface{}{
		"query": "mutation($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document{ id status jobId } } }",
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
					ID     string  `json:"id"`
					Status string  `json:"status"`
					JobID  *string `json:"jobId"`
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
	if resp.Data.UploadKnowledgeDocument.Document.ID == "" || resp.Data.UploadKnowledgeDocument.Document.ID != capturedDocID || resp.Data.UploadKnowledgeDocument.Document.JobID == nil || *resp.Data.UploadKnowledgeDocument.Document.JobID != capturedDocID {
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

	dialCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	storageClient, err := storageclient.NewGRPCClient(dialCtx, "bufnet", grpc.WithContextDialer(dialer), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("cannot create storage client: %v", err)
	}
	t.Cleanup(func() { _ = storageClient.Close() })

	logger := zap.NewNop()
	docClient := &mockDocumentClient{
		createFunc: func(ctx context.Context, in *pb.CreateDocumentRequest) (*pb.Document, error) {
			if in.GetId() == "" || in.GetId() != storageServer.lastDocumentID {
				t.Fatalf("document id mismatch: %s vs %s", in.GetId(), storageServer.lastDocumentID)
			}
			return &pb.Document{Id: in.GetId(), WorkflowId: in.GetId(), JobId: in.GetId(), FileName: in.FileName, Repository: in.Repository, Branch: in.Branch, Path: in.Path, UploadBucket: in.UploadBucket, UploadObjectKey: in.UploadObjectKey, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED, ActualStatus: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
		},
		getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil },
		listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
			return &pb.ListDocumentsResponse{}, nil
		},
	}

	resolver := &Resolver{DocumentClient: docClient, StorageClient: storageClient, UploadConfig: UploadValidationConfig{SignedURLTTLSeconds: 900}, Logger: logger}
	gqlServer := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	handlerWithMiddleware := middleware.CorrelationIDMiddleware(logger)(middleware.APIKeyMiddleware("secret", logger)(gqlServer))

	reqBody := map[string]interface{}{
		"query": "mutation($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ uploadUrl uploadMethod document{ id status jobId } } }",
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
					ID     string  `json:"id"`
					Status string  `json:"status"`
					JobID  *string `json:"jobId"`
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
	if storageServer.lastAPIKey != "secret" {
		t.Fatalf("api key not forwarded to storage service")
	}
	if resp.Data.UploadKnowledgeDocument.Document.ID != storageServer.lastDocumentID || resp.Data.UploadKnowledgeDocument.UploadURL != "http://minio/presigned" {
		t.Fatalf("unexpected response: %+v", resp.Data.UploadKnowledgeDocument)
	}
	if resp.Data.UploadKnowledgeDocument.Document.JobID == nil || *resp.Data.UploadKnowledgeDocument.Document.JobID != storageServer.lastDocumentID {
		t.Fatalf("job id not forwarded: %+v", resp.Data.UploadKnowledgeDocument.Document)
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
	lastAPIKey      string
	lastDocumentID  string
}

func (s *storageServiceStub) GenerateUploadURL(ctx context.Context, req *storagepb.GenerateUploadURLRequest) (*storagepb.GenerateUploadURLResponse, error) {
	s.calls++
	s.lastCorrelation = req.GetCorrelationId()
	s.lastDocumentID = req.GetDocumentId()

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
	if apiKey := md.Get("x-api-key"); len(apiKey) == 0 || apiKey[0] != "secret" {
		s.t.Fatalf("api key not propagated in metadata: %v", apiKey)
	}
	s.lastAPIKey = md.Get("x-api-key")[0]

	return &storagepb.GenerateUploadURLResponse{
		UploadUrl:         "http://minio/presigned",
		Bucket:            "docx",
		ObjectKey:         "docx/" + req.GetDocumentId() + ".docx",
		MarkdownBucket:    "markdown",
		MarkdownObjectKey: "markdown/" + req.GetDocumentId() + ".md",
		ExpiresInSeconds:  req.GetExpiresInSeconds(),
	}, nil
}
