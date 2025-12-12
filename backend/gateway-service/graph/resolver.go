package graph

import (
	"go.uber.org/zap"

	"backend-gateway-service/internal/usecase"
)

// Resolver serves as dependency injection for your app, add any dependencies you require here.
type Resolver struct {
	DocumentUploader usecase.DocumentUploader
	DocumentService  usecase.DocumentProvider
	UploadConfig     UploadValidationConfig
	Logger           *zap.Logger
}
