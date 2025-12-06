-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- +goose StatementBegin
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'document_status') THEN
CREATE TYPE document_status AS ENUM (
            'Uploaded',
            'StoredOriginal',
            'ConvertedToMarkdown',
            'PublishedToGitHub',
            'Failed'
        );
END IF;
END;
$do$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS documents (
                                         id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                         name TEXT NOT NULL,
                                         status document_status NOT NULL,
                                         original_bucket TEXT NOT NULL,
                                         original_object_key TEXT NOT NULL,
                                         markdown_bucket TEXT NOT NULL,
                                         markdown_object_key TEXT NOT NULL,
                                         created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                                         updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_original_key ON documents(original_bucket, original_object_key);
CREATE UNIQUE INDEX IF NOT EXISTS idx_documents_markdown_key ON documents(markdown_bucket, markdown_object_key);

CREATE TABLE IF NOT EXISTS document_versions (
                                                 id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                                 document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
                                                 version INT NOT NULL CHECK (version > 0),
                                                 original_object_key TEXT NOT NULL,
                                                 markdown_object_key TEXT NOT NULL,
                                                 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                                                 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                                                 UNIQUE(document_id, version)
);
CREATE INDEX IF NOT EXISTS idx_document_versions_doc ON document_versions(document_id);

CREATE TABLE IF NOT EXISTS document_status_logs (
                                                    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
                                                    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
                                                    from_status document_status,
                                                    to_status document_status NOT NULL,
                                                    reason TEXT,
                                                    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_document_status_logs_doc ON document_status_logs(document_id);
CREATE INDEX IF NOT EXISTS idx_document_status_logs_created ON document_status_logs(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_document_status_logs_doc;
DROP TABLE IF EXISTS document_status_logs;
DROP INDEX IF EXISTS idx_document_versions_doc;
DROP TABLE IF EXISTS document_versions;
DROP INDEX IF EXISTS idx_documents_markdown_key;
DROP INDEX IF EXISTS idx_documents_original_key;
DROP TABLE IF EXISTS documents;
DROP TYPE IF EXISTS document_status;
