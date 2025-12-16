-- +goose Up
-- Очередь заданий на предобработку исходных файлов для LibreOffice-конвертации.

-- +goose StatementBegin
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'preprocess_status') THEN
        CREATE TYPE preprocess_status AS ENUM (
            'pending',
            'processing',
            'done',
            'failed',
            'stopped'
        );
    END IF;
END;
$do$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS preprocess_queue (
    job_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_bucket TEXT NOT NULL,
    source_key TEXT NOT NULL,
    source_etag TEXT,
    target_bucket TEXT,
    target_key TEXT,
    status preprocess_status NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts INT NOT NULL DEFAULT 5 CHECK (max_attempts > 0),
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    error_code TEXT,
    error_message TEXT,
    error_details JSONB NOT NULL DEFAULT '{}'::jsonb,
    callback_url TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_preprocess_queue_status_next_run
    ON preprocess_queue(status, next_run_at);
CREATE INDEX IF NOT EXISTS idx_preprocess_queue_source
    ON preprocess_queue(source_bucket, source_key);
CREATE UNIQUE INDEX IF NOT EXISTS uq_preprocess_queue_source_etag
    ON preprocess_queue(source_bucket, source_key, source_etag);

-- +goose Down
DROP INDEX IF EXISTS uq_preprocess_queue_source_etag;
DROP INDEX IF EXISTS idx_preprocess_queue_source;
DROP INDEX IF EXISTS idx_preprocess_queue_status_next_run;
DROP TABLE IF EXISTS preprocess_queue;
DROP TYPE IF EXISTS preprocess_status;
