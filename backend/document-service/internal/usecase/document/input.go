package document

// CreateDocumentInput содержит параметры создания документа.
type CreateDocumentInput struct {
	ID                string
	FileName          string
	Owner             string
	Repository        string
	Branch            string
	BaseBranch        string
	Path              string
	UploadBucket      string
	UploadObjectKey   string
	MarkdownBucket    string
	MarkdownObjectKey string
	CreatePR          bool
	ForceOCR          bool
}

// ConfirmUploadInput подтверждает загрузку исходного файла.
type ConfirmUploadInput struct {
	DocumentID string
	Bucket     string
	ObjectKey  string
}

// UpdateStatusInput обновляет статус документа и опционально версионные ключи.
type UpdateStatusInput struct {
	DocumentID        string
	Status            string
	Reason            string
	ErrorReason       string
	MarkdownObjectKey string
	OriginalObjectKey string
	Version           int32
}
