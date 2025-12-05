package temporal

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	temporalClient "go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"

	"backend-gateway-service/internal/ctxutils"
)

// UploadWorkflowInput входные данные для workflow.
type UploadWorkflowInput struct {
	DocumentId      string
	FileName        string
	Repository      string
	Branch          string
	Path            string
	UploadBucket    string
	UploadObjectKey string
	UploadURL       string
	FileSizeBytes   int
	CorrelationId   string
}

// Client интерфейс над Temporal SDK.
type Client interface {
	StartUploadWorkflow(ctx context.Context, input UploadWorkflowInput) (string, error)
	Close()
}

type SDKClient struct {
	client    temporalClient.Client
	taskQueue string
}

func NewSDKClient(address, namespace, taskQueue string) (Client, error) {
	c, err := temporalClient.Dial(temporalClient.Options{HostPort: address, Namespace: namespace})
	if err != nil {
		return nil, fmt.Errorf("failed to connect temporal: %w", err)
	}
	return &SDKClient{client: c, taskQueue: taskQueue}, nil
}

func (c *SDKClient) StartUploadWorkflow(ctx context.Context, input UploadWorkflowInput) (string, error) {
	correlationID := ctxutils.CorrelationID(ctx)
	if correlationID == "" {
		correlationID = input.CorrelationId
	}
	ctx, span := otel.Tracer("gateway.temporal").Start(ctx, "StartUploadWorkflow")
	span.SetAttributes(attribute.String("correlation_id", correlationID), attribute.String("document_id", input.DocumentId), attribute.String("api_key", ctxutils.APIKey(ctx)))
	defer span.End()
	options := temporalClient.StartWorkflowOptions{
		TaskQueue: c.taskQueue,
		Memo: map[string]interface{}{
			"correlation_id": correlationID,
			"api_key":        ctxutils.APIKey(ctx),
		},
	}

	we, err := c.client.ExecuteWorkflow(ctx, options, "KnowledgeDocumentWorkflow", input)
	if err != nil {
		return "", err
	}
	return we.GetID(), nil
}

func (c *SDKClient) Close() {
	if c.client != nil {
		c.client.Close()
	}
}

// Workflow definition placeholder to ensure registration when running worker tests.
func KnowledgeDocumentWorkflow(ctx workflow.Context, input UploadWorkflowInput) (string, error) {
	return input.DocumentId, nil
}
