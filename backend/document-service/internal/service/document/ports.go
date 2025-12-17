package document

import "backend-document-service/internal/domain"

// Repository объединяет операции, необходимые воркерам и приложениям для управления документами.
type Repository interface {
	domain.DocumentRepository
	domain.JobRepository
	domain.EventLogRepository
}
