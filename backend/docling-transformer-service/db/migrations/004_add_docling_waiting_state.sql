ALTER TABLE transformations_queue
    DROP CONSTRAINT IF EXISTS transformations_queue_status_check;

ALTER TABLE transformations_queue
    ADD CONSTRAINT transformations_queue_status_check
        CHECK (status IN ('pending', 'processing', 'waiting_docling', 'done', 'failed'));

ALTER TABLE transformations_queue
    ADD COLUMN IF NOT EXISTS next_poll_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS transformations_queue_waiting_idx
    ON transformations_queue (next_poll_at)
    WHERE status = 'waiting_docling';
