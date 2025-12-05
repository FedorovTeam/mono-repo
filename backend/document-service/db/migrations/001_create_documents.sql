-- +goose Up
CREATE TABLE IF NOT EXISTS documents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    original_bucket TEXT NOT NULL,
    original_object_key TEXT NOT NULL,
    markdown_bucket TEXT NOT NULL,
    markdown_object_key TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now() NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT now() NOT NULL,
    CONSTRAINT document_status_check CHECK (status IN (
        'Uploaded', 'StoredOriginal', 'ConvertedToMarkdown', 'PublishedToGitHub', 'Failed'
    ))
);

CREATE TABLE IF NOT EXISTS document_versions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    version INT NOT NULL,
    original_object_key TEXT NOT NULL,
    markdown_object_key TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now() NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT now() NOT NULL,
    UNIQUE(document_id, version)
);
CREATE INDEX IF NOT EXISTS idx_document_versions_doc ON document_versions(document_id);

CREATE TABLE IF NOT EXISTS document_status_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    from_status TEXT,
    to_status TEXT NOT NULL,
    reason TEXT,
    created_at TIMESTAMPTZ DEFAULT now() NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_document_status_logs_doc ON document_status_logs(document_id);

-- +goose Down
DROP INDEX IF EXISTS idx_document_status_logs_doc;
DROP TABLE IF EXISTS document_status_logs;
DROP INDEX IF EXISTS idx_document_versions_doc;
DROP TABLE IF EXISTS document_versions;
DROP TABLE IF EXISTS documents;
