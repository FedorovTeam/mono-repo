package domain

// DocumentStatus описывает статус документа.
type DocumentStatus string

const (
	DocumentStatusUploaded            DocumentStatus = "UPLOADED"
	DocumentStatusStoredOriginal      DocumentStatus = "STORED_ORIGINAL"
	DocumentStatusConvertedToMarkdown DocumentStatus = "CONVERTED_TO_MARKDOWN"
	DocumentStatusPublishedToGithub   DocumentStatus = "PUBLISHED_TO_GITHUB"
	DocumentStatusFailed              DocumentStatus = "FAILED"
)

// Document представляет доменную модель документа.
type Document struct {
	ID              string
	FileName        string
	Repository      string
	RepositoryOwner *string
	Branch          string
	Path            string
	Status          DocumentStatus
	JobID           *string
	Error           *string
	EnablePdfOcr    bool
}

// DocumentFilter задает параметры фильтрации документов.
type DocumentFilter struct {
	Status     *DocumentStatus
	Repository *string
}

// Pagination описывает параметры постраничной выборки.
type Pagination struct {
	Limit  int
	Offset int
}

// DocumentList описывает страницу документов.
type DocumentList struct {
	Documents  []Document
	TotalCount int64
}
