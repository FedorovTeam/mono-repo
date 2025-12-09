ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS docling_job_id TEXT,
    ADD COLUMN IF NOT EXISTS docling_poll_count INT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS transformations_queue_docling_idx ON transformations_queue(docling_job_id) WHERE docling_job_id IS NOT NULL;
