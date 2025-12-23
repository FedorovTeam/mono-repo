package domain

import "strings"

// DocumentStatus описывает статус документа.
type DocumentStatus string

const (
	DocumentStatusUploaded            DocumentStatus = "UPLOADED"
	DocumentStatusStoredOriginal      DocumentStatus = "STORED_ORIGINAL"
	DocumentStatusConvertedToMarkdown DocumentStatus = "CONVERTED_TO_MARKDOWN"
	DocumentStatusPublishedToGithub   DocumentStatus = "PUBLISHED_TO_GITHUB"
	DocumentStatusFailed              DocumentStatus = "FAILED"
)

// ConversionStrategy задаёт желаемую стратегию обработки.
type ConversionStrategy string

const (
	ConversionStrategyDefault           ConversionStrategy = ""
	ConversionStrategyTablesOnly        ConversionStrategy = "tables_only"
	ConversionStrategyDoclingWithTables ConversionStrategy = "docling_with_tables"
)

// Document представляет доменную модель документа.
type Document struct {
	ID                 string
	FileName           string
	Repository         string
	RepositoryOwner    *string
	Branch             string
	Path               string
	Status             DocumentStatus
	JobID              *string
	Error              *string
	ExtractTables      bool
	ConversionStrategy ConversionStrategy
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

// NormalizeConversionStrategy приводит стратегию к нижнему регистру без пробелов.
func NormalizeConversionStrategy(strategy string) ConversionStrategy {
	return ConversionStrategy(strings.ToLower(strings.TrimSpace(strategy)))
}
