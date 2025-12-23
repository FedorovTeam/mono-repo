package domain

import (
	"strings"
	"time"
)

// ProcessingJob описывает задачу фоновой обработки документа.
type ProcessingJob struct {
	TaskID     string
	DocumentID string
	Stage      JobStage
	Status     JobStatus
	Attempts   int
	NextRunAt  time.Time
	// Payload хранит ссылки для внешних исполнителей (bucket/object_key, queue_id и т.п.).
	Payload   map[string]any
	CreatedAt time.Time
	UpdatedAt time.Time
}

// JobEvent фиксирует события обработки задачи.
type JobEvent struct {
	ID        string
	JobID     string
	Stage     JobStage
	Status    JobStatus
	EventType string
	Message   string
	// Payload события фиксирует входные/выходные ссылки шагов (например, object_key, queue_id или commit_sha) без размещения контента.
	Payload   map[string]any
	CreatedAt time.Time
}

// MarkStatus меняет статус задачи и обновляет временные метки.
func (j *ProcessingJob) MarkStatus(status JobStatus) {
	j.Status = status
	j.UpdatedAt = time.Now().UTC()
}

// JobPayloadMetadata хранит общие поля для всех стадий.
type JobPayloadMetadata struct {
	CorrelationID string
	LastError     string
	RetryAfter    string
}

// StoreOriginalPayload описывает данные для шага сохранения оригинала.
type StoreOriginalPayload struct {
	JobPayloadMetadata
	Bucket       string
	ObjectKey    string
	OriginalETag string
}

// ConvertPayload описывает данные для шага конвертации в Markdown.
type ConvertPayload struct {
	JobPayloadMetadata
	OriginalBucket        string
	OriginalObjectKey     string
	PreprocessJobID       string
	PreprocessedBucket    string
	PreprocessedObjectKey string
	TargetObjectKey       string
	MarkdownBucket        string
	MarkdownObjectKey     string
	QueueID               string
	Status                string
	FilenameExtension     string
	ConversionStrategy    ConversionStrategy
	ExtractTables         bool
}

// PreprocessPayload описывает данные для шага преобразования в DOCX.
type PreprocessPayload struct {
	JobPayloadMetadata
	OriginalBucket         string
	OriginalObjectKey      string
	OriginalETag           string
	TargetBucket           string
	TargetObjectKey        string
	PreprocessJobID        string
	PreprocessedBucket     string
	PreprocessedObjectKey  string
	FilenameExtension      string
	PreprocessStatus       string
	PreprocessErrorMessage string
	PollAttempts           int
}

// ExtractTablesPayload описывает данные для извлечения таблиц из PDF.
type ExtractTablesPayload struct {
	JobPayloadMetadata
	OriginalBucket          string
	OriginalObjectKey       string
	ArtifactsBucket         string
	ArtifactsPrefix         string
	ArtifactsPrefixTemplate string
	QueueID                 string
	TableQueueID            string
	Status                  string
	StatusReason            string
	PollAttempts            int
	MarkdownObjectKey       string
	ConversionStrategy      ConversionStrategy
	ExtractTables           bool
}

// PublishPayload описывает данные для шага публикации в GitHub.
type PublishPayload struct {
	JobPayloadMetadata
	MarkdownBucket    string
	MarkdownObjectKey string
	Repository        string
	Branch            string
	BaseBranch        string
	GitHubPath        string
	CreatePR          *bool
	CommitMessage     string
	PRTitle           string
	PRBody            string
	AuthorName        string
	AuthorEmail       string
}

func (p JobPayloadMetadata) applyTo(payload map[string]any) {
	if p.CorrelationID != "" {
		payload["correlation_id"] = p.CorrelationID
	}
	if p.LastError != "" {
		payload["last_error"] = p.LastError
	}
	if p.RetryAfter != "" {
		payload["retry_after"] = p.RetryAfter
	}
}

func metadataFromMap(payload map[string]any) JobPayloadMetadata {
	return JobPayloadMetadata{
		CorrelationID: stringFromMap(payload, "correlation_id"),
		LastError:     stringFromMap(payload, "last_error"),
		RetryAfter:    stringFromMap(payload, "retry_after"),
	}
}

// ToMap преобразует StoreOriginalPayload в map для хранения в БД.
func (p StoreOriginalPayload) ToMap() map[string]any {
	res := map[string]any{}
	if p.Bucket != "" {
		res["bucket"] = p.Bucket
	}
	if p.ObjectKey != "" {
		res["object_key"] = p.ObjectKey
	}
	if p.OriginalETag != "" {
		res["original_etag"] = p.OriginalETag
	}
	p.JobPayloadMetadata.applyTo(res)
	return res
}

// ToMap преобразует ExtractTablesPayload в map.
func (p ExtractTablesPayload) ToMap() map[string]any {
	res := map[string]any{}
	if p.OriginalBucket != "" {
		res["original_bucket"] = p.OriginalBucket
	}
	if p.OriginalObjectKey != "" {
		res["original_object_key"] = p.OriginalObjectKey
	}
	if p.ArtifactsBucket != "" {
		res["artifacts_bucket"] = p.ArtifactsBucket
	}
	if p.ArtifactsPrefix != "" {
		res["artifacts_prefix"] = p.ArtifactsPrefix
	}
	if p.ArtifactsPrefixTemplate != "" {
		res["artifacts_prefix_template"] = p.ArtifactsPrefixTemplate
	}
	if p.TableQueueID != "" {
		res["table_queue_id"] = p.TableQueueID
	}
	if p.QueueID != "" {
		res["queue_id"] = p.QueueID
	} else if p.TableQueueID != "" {
		res["queue_id"] = p.TableQueueID
	}
	if p.Status != "" {
		res["status"] = p.Status
	}
	if p.StatusReason != "" {
		res["status_reason"] = p.StatusReason
	}
	if p.PollAttempts > 0 {
		res["poll_attempts"] = p.PollAttempts
	}
	if p.MarkdownObjectKey != "" {
		res["markdown_object_key"] = p.MarkdownObjectKey
	}
	if p.ConversionStrategy != "" {
		res["conversion_strategy"] = string(p.ConversionStrategy)
	}
	res["extract_tables"] = p.ExtractTables
	p.JobPayloadMetadata.applyTo(res)
	return res
}

// StoreOriginalPayloadFromMap восстанавливает структуру из map.
func StoreOriginalPayloadFromMap(payload map[string]any) StoreOriginalPayload {
	return StoreOriginalPayload{
		JobPayloadMetadata: metadataFromMap(payload),
		Bucket:             stringFromMap(payload, "bucket"),
		ObjectKey:          stringFromMap(payload, "object_key"),
		OriginalETag:       stringFromMap(payload, "original_etag"),
	}
}

// ToMap преобразует ConvertPayload в map для хранения в БД.
func (p ConvertPayload) ToMap() map[string]any {
	res := map[string]any{}
	if p.OriginalBucket != "" {
		res["original_bucket"] = p.OriginalBucket
	}
	if p.OriginalObjectKey != "" {
		res["original_object_key"] = p.OriginalObjectKey
	}
	if p.PreprocessJobID != "" {
		res["preprocess_job_id"] = p.PreprocessJobID
	}
	if p.PreprocessedBucket != "" {
		res["preprocessed_bucket"] = p.PreprocessedBucket
	}
	if p.PreprocessedObjectKey != "" {
		res["preprocessed_object_key"] = p.PreprocessedObjectKey
	}
	if p.TargetObjectKey != "" {
		res["target_object_key"] = p.TargetObjectKey
	}
	if p.MarkdownBucket != "" {
		res["markdown_bucket"] = p.MarkdownBucket
	}
	if p.MarkdownObjectKey != "" {
		res["markdown_object_key"] = p.MarkdownObjectKey
	}
	if p.QueueID != "" {
		res["queue_id"] = p.QueueID
	}
	if p.Status != "" {
		res["status"] = p.Status
	}
	if p.FilenameExtension != "" {
		res["filename_extension"] = p.FilenameExtension
	}
	if p.ConversionStrategy != "" {
		res["conversion_strategy"] = string(p.ConversionStrategy)
	}
	res["extract_tables"] = p.ExtractTables
	p.JobPayloadMetadata.applyTo(res)
	return res
}

// ConvertPayloadFromMap восстанавливает структуру из map.
func ConvertPayloadFromMap(payload map[string]any) ConvertPayload {
	return ConvertPayload{
		JobPayloadMetadata:    metadataFromMap(payload),
		OriginalBucket:        stringFromMap(payload, "original_bucket"),
		OriginalObjectKey:     stringFromMap(payload, "original_object_key"),
		PreprocessJobID:       stringFromMap(payload, "preprocess_job_id"),
		PreprocessedBucket:    stringFromMap(payload, "preprocessed_bucket"),
		PreprocessedObjectKey: stringFromMap(payload, "preprocessed_object_key"),
		TargetObjectKey:       stringFromMap(payload, "target_object_key"),
		MarkdownBucket:        stringFromMap(payload, "markdown_bucket"),
		MarkdownObjectKey:     stringFromMap(payload, "markdown_object_key"),
		QueueID:               stringFromMap(payload, "queue_id"),
		Status:                stringFromMap(payload, "status"),
		FilenameExtension:     stringFromMap(payload, "filename_extension"),
		ConversionStrategy:    ConversionStrategy(stringFromMap(payload, "conversion_strategy")),
		ExtractTables:         boolFromMap(payload, "extract_tables"),
	}
}

// ToMap преобразует PreprocessPayload в map для хранения в БД.
func (p PreprocessPayload) ToMap() map[string]any {
	res := map[string]any{}
	if p.OriginalBucket != "" {
		res["original_bucket"] = p.OriginalBucket
	}
	if p.OriginalObjectKey != "" {
		res["original_object_key"] = p.OriginalObjectKey
	}
	if p.OriginalETag != "" {
		res["original_etag"] = p.OriginalETag
	}
	if p.TargetBucket != "" {
		res["target_bucket"] = p.TargetBucket
	}
	if p.TargetObjectKey != "" {
		res["target_object_key"] = p.TargetObjectKey
	}
	if p.PreprocessJobID != "" {
		res["preprocess_job_id"] = p.PreprocessJobID
	}
	if p.PreprocessedBucket != "" {
		res["preprocessed_bucket"] = p.PreprocessedBucket
	}
	if p.PreprocessedObjectKey != "" {
		res["preprocessed_object_key"] = p.PreprocessedObjectKey
	}
	if p.FilenameExtension != "" {
		res["filename_extension"] = p.FilenameExtension
	}
	if p.PreprocessStatus != "" {
		res["preprocess_status"] = p.PreprocessStatus
	}
	if p.PreprocessErrorMessage != "" {
		res["preprocess_error_message"] = p.PreprocessErrorMessage
	}
	if p.PollAttempts > 0 {
		res["poll_attempts"] = p.PollAttempts
	}
	p.JobPayloadMetadata.applyTo(res)
	return res
}

// PreprocessPayloadFromMap восстанавливает структуру из map.
func PreprocessPayloadFromMap(payload map[string]any) PreprocessPayload {
	return PreprocessPayload{
		JobPayloadMetadata:     metadataFromMap(payload),
		OriginalBucket:         stringFromMap(payload, "original_bucket"),
		OriginalObjectKey:      stringFromMap(payload, "original_object_key"),
		OriginalETag:           stringFromMap(payload, "original_etag"),
		TargetBucket:           stringFromMap(payload, "target_bucket"),
		TargetObjectKey:        stringFromMap(payload, "target_object_key"),
		PreprocessJobID:        stringFromMap(payload, "preprocess_job_id"),
		PreprocessedBucket:     stringFromMap(payload, "preprocessed_bucket"),
		PreprocessedObjectKey:  stringFromMap(payload, "preprocessed_object_key"),
		FilenameExtension:      stringFromMap(payload, "filename_extension"),
		PreprocessStatus:       stringFromMap(payload, "preprocess_status"),
		PreprocessErrorMessage: stringFromMap(payload, "preprocess_error_message"),
		PollAttempts:           intFromMap(payload, "poll_attempts"),
	}
}

// ExtractTablesPayloadFromMap восстанавливает ExtractTablesPayload из map.
func ExtractTablesPayloadFromMap(payload map[string]any) ExtractTablesPayload {
	result := ExtractTablesPayload{
		JobPayloadMetadata:      metadataFromMap(payload),
		OriginalBucket:          stringFromMap(payload, "original_bucket"),
		OriginalObjectKey:       stringFromMap(payload, "original_object_key"),
		ArtifactsBucket:         stringFromMap(payload, "artifacts_bucket"),
		ArtifactsPrefix:         stringFromMap(payload, "artifacts_prefix"),
		ArtifactsPrefixTemplate: stringFromMap(payload, "artifacts_prefix_template"),
		QueueID:                 stringFromMap(payload, "queue_id"),
		TableQueueID:            stringFromMap(payload, "table_queue_id"),
		Status:                  stringFromMap(payload, "status"),
		StatusReason:            stringFromMap(payload, "status_reason"),
		PollAttempts:            intFromMap(payload, "poll_attempts"),
		MarkdownObjectKey:       stringFromMap(payload, "markdown_object_key"),
		ConversionStrategy:      ConversionStrategy(stringFromMap(payload, "conversion_strategy")),
		ExtractTables:           boolFromMap(payload, "extract_tables"),
	}
	if result.TableQueueID == "" {
		result.TableQueueID = result.QueueID
	}
	if result.QueueID == "" {
		result.QueueID = result.TableQueueID
	}
	return result
}

// ToMap преобразует PublishPayload в map для хранения в БД.
func (p PublishPayload) ToMap() map[string]any {
	res := map[string]any{}
	if p.MarkdownBucket != "" {
		res["markdown_bucket"] = p.MarkdownBucket
	}
	if p.MarkdownObjectKey != "" {
		res["markdown_object_key"] = p.MarkdownObjectKey
	}
	if p.Repository != "" {
		res["repository"] = p.Repository
	}
	if p.Branch != "" {
		res["branch"] = p.Branch
	}
	if p.BaseBranch != "" {
		res["base_branch"] = p.BaseBranch
	}
	if p.GitHubPath != "" {
		res["github_path"] = p.GitHubPath
	}
	if p.CreatePR != nil {
		res["create_pr"] = *p.CreatePR
	}
	if p.CommitMessage != "" {
		res["commit_message"] = p.CommitMessage
	}
	if p.PRTitle != "" {
		res["pr_title"] = p.PRTitle
	}
	if p.PRBody != "" {
		res["pr_body"] = p.PRBody
	}
	if p.AuthorName != "" {
		res["author_name"] = p.AuthorName
	}
	if p.AuthorEmail != "" {
		res["author_email"] = p.AuthorEmail
	}
	p.JobPayloadMetadata.applyTo(res)
	return res
}

// PublishPayloadFromMap восстанавливает структуру из map.
func PublishPayloadFromMap(payload map[string]any) PublishPayload {
	var createPR *bool
	if val, ok := payload["create_pr"]; ok {
		if b, ok := val.(bool); ok {
			createPR = &b
		}
	}
	return PublishPayload{
		JobPayloadMetadata: metadataFromMap(payload),
		MarkdownBucket:     stringFromMap(payload, "markdown_bucket"),
		MarkdownObjectKey:  stringFromMap(payload, "markdown_object_key"),
		Repository:         stringFromMap(payload, "repository"),
		Branch:             stringFromMap(payload, "branch"),
		BaseBranch:         stringFromMap(payload, "base_branch"),
		GitHubPath:         stringFromMap(payload, "github_path"),
		CreatePR:           createPR,
		CommitMessage:      stringFromMap(payload, "commit_message"),
		PRTitle:            stringFromMap(payload, "pr_title"),
		PRBody:             stringFromMap(payload, "pr_body"),
		AuthorName:         stringFromMap(payload, "author_name"),
		AuthorEmail:        stringFromMap(payload, "author_email"),
	}
}

func stringFromMap(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	if val, ok := payload[key]; ok {
		if s, ok := val.(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func intFromMap(payload map[string]any, key string) int {
	if payload == nil {
		return 0
	}
	val, ok := payload[key]
	if !ok {
		return 0
	}
	switch v := val.(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		return 0
	}
}

func boolFromMap(payload map[string]any, key string) bool {
	if payload == nil {
		return false
	}
	val, ok := payload[key]
	if !ok {
		return false
	}
	switch v := val.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}
