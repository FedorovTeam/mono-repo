ALTER TABLE table_extraction_jobs
    ADD COLUMN source_url TEXT DEFAULT '';

ALTER TABLE table_extraction_jobs
    ADD COLUMN local_path TEXT DEFAULT '';

ALTER TABLE table_extraction_queue
    ADD COLUMN source_url TEXT DEFAULT '';

ALTER TABLE table_extraction_queue
    ADD COLUMN local_path TEXT DEFAULT '';
