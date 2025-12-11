-- +goose Up
-- Таблица documents остаётся источником статусов документов.
-- processing_jobs хранит очередь задач для cron-воркеров, job_events — детальный аудит попыток.

-- +goose StatementBegin
DO $do$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'job_stage') THEN
        CREATE TYPE job_stage AS ENUM (
            'store_original',
            'convert_to_markdown',
            'publish_to_github'
        );
    END IF;

    IF NOT EXISTS (SELECT 1 FROM pg_type WHERE typname = 'job_status') THEN
        CREATE TYPE job_status AS ENUM (
            'pending',
            'in_progress',
            'completed',
            'failed'
        );
    END IF;
END;
$do$;
-- +goose StatementEnd

CREATE TABLE IF NOT EXISTS processing_jobs (
    task_id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    document_id UUID NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    stage job_stage NOT NULL,
    status job_status NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_run_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(document_id, stage)
);

CREATE INDEX IF NOT EXISTS idx_processing_jobs_stage_status_next_run
    ON processing_jobs(stage, status, next_run_at);
CREATE INDEX IF NOT EXISTS idx_processing_jobs_next_run
    ON processing_jobs(next_run_at);

CREATE TABLE IF NOT EXISTS job_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    job_id UUID NOT NULL REFERENCES processing_jobs(task_id) ON DELETE CASCADE,
    stage job_stage NOT NULL,
    status job_status NOT NULL,
    event_type TEXT NOT NULL,
    message TEXT,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_job_events_job ON job_events(job_id);
CREATE INDEX IF NOT EXISTS idx_job_events_created ON job_events(created_at);

-- +goose Down
DROP INDEX IF EXISTS idx_job_events_created;
DROP INDEX IF EXISTS idx_job_events_job;
DROP TABLE IF EXISTS job_events;

DROP INDEX IF EXISTS idx_processing_jobs_next_run;
DROP INDEX IF EXISTS idx_processing_jobs_stage_status_next_run;
DROP TABLE IF EXISTS processing_jobs;

DROP TYPE IF EXISTS job_status;
DROP TYPE IF EXISTS job_stage;
