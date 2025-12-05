package graph

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/99designs/gqlgen/graphql/handler"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"backend-gateway-service/graph/generated"
	"backend-gateway-service/graph/model"
	"backend-gateway-service/internal/clients/temporal"
	"backend-gateway-service/internal/ctxutils"
	"backend-gateway-service/internal/middleware"
	pb "backend-gateway-service/proto/backend/document/v1"
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

func TestUploadValidation(t *testing.T) {
	resolver := &Resolver{DocumentClient: &mockDocumentClient{}, TemporalClient: &mockTemporalClient{}, Logger: zap.NewNop()}
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
		return &pb.Document{Id: "123", FileName: in.FileName, Repository: in.Repository, Branch: in.Branch, Path: in.Path, Status: pb.DocumentStatus_DOCUMENT_STATUS_UPLOADED}, nil
	}, retryFunc: func(ctx context.Context, in *pb.RetryProcessingRequest) (*pb.Document, error) { return nil, nil }, getFunc: func(ctx context.Context, in *pb.GetDocumentRequest) (*pb.Document, error) { return nil, nil }, listFunc: func(ctx context.Context, in *pb.ListDocumentsRequest) (*pb.ListDocumentsResponse, error) {
		return nil, nil
	}}

	temporalClient := &mockTemporalClient{startFunc: func(ctx context.Context, input temporal.UploadWorkflowInput) (string, error) {
		temporalCalled = true
		if input.CorrelationId != "corr-test" {
			t.Fatalf("workflow correlation id missing")
		}
		if input.DocumentId != "123" {
			t.Fatalf("unexpected document id: %s", input.DocumentId)
		}
		return "wf-1", nil
	}}

	resolver := &Resolver{DocumentClient: docClient, TemporalClient: temporalClient, Logger: zap.NewNop()}
	mr := &mutationResolver{resolver}

	result, err := mr.UploadKnowledgeDocument(ctx, model.UploadKnowledgeDocumentInput{
		FileName:      "demo.docx",
		ContentBase64: "ZHVtbXk=",
		Repository:    "org/repo",
		Branch:        "main",
		Path:          "docs/demo.md",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !docCalled || !temporalCalled {
		t.Fatalf("expected downstream calls")
	}
	if result.WorkflowID == nil || *result.WorkflowID != "wf-1" {
		t.Fatalf("workflow id not returned")
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

	resolver := &Resolver{DocumentClient: docClient, TemporalClient: temporalClient, Logger: logger}
	gqlServer := handler.NewDefaultServer(generated.NewExecutableSchema(generated.Config{Resolvers: resolver}))
	handlerWithMiddleware := middleware.CorrelationIDMiddleware(logger)(middleware.APIKeyMiddleware("secret", logger)(gqlServer))

	reqBody := map[string]interface{}{
		"query": "mutation($input: UploadKnowledgeDocumentInput!){ uploadKnowledgeDocument(input:$input){ id status workflowId } }",
		"variables": map[string]interface{}{
			"input": map[string]interface{}{
				"fileName":      "demo.docx",
				"contentBase64": "ZHVtbXk=",
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
				ID         string  `json:"id"`
				Status     string  `json:"status"`
				WorkflowID *string `json:"workflowId"`
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
	if resp.Data.UploadKnowledgeDocument.ID != "doc-1" || resp.Data.UploadKnowledgeDocument.WorkflowID == nil || *resp.Data.UploadKnowledgeDocument.WorkflowID != "wf-99" {
		t.Fatalf("unexpected response: %+v", resp.Data.UploadKnowledgeDocument)
	}
}
