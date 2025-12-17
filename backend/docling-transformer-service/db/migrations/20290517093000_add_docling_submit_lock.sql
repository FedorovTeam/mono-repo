-- +goose Up
CREATE TABLE IF NOT EXISTS docling_submit_lock (
    token text PRIMARY KEY,
    updated_at timestamptz NOT NULL DEFAULT NOW()
);

INSERT INTO docling_submit_lock (token) VALUES ('docling_submit')
ON CONFLICT (token) DO NOTHING;

-- +goose Down
DROP TABLE IF EXISTS docling_submit_lock;
