package graph

import (
	"go.uber.org/zap"

	"backend-gateway-service/internal/clients/document"
	"backend-gateway-service/internal/clients/storage"
)

// Resolver serves as dependency injection for your app, add any dependencies you require here.
type Resolver struct {
	DocumentClient document.Client
	StorageClient  storage.Client
	UploadConfig   UploadValidationConfig
	Logger         *zap.Logger
}
