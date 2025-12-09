ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS retry_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS correlation_id TEXT;

CREATE INDEX IF NOT EXISTS transformations_queue_started_at_idx ON transformations_queue(started_at) WHERE status = 'processing';
