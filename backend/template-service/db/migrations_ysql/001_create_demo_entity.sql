-- +goose Up
-- Migration: Create user_card table for discount-engine-service (YugabyteDB)

CREATE TABLE IF NOT EXISTS demo_entity (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    bucket_id UUID NOT NULL,
    region_id TEXT NOT NULL,
    bucket TEXT NOT NULL,
    value TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now(),
    updated_at TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_demo_entity_bucket_id ON demo_entity(bucket_id);
CREATE INDEX IF NOT EXISTS idx_demo_entity_region_id ON demo_entity(region_id);


-- +goose Down
DROP INDEX IF EXISTS idx_demo_entity_bucket_id;
DROP INDEX IF EXISTS idx_demo_entity_region_id;
DROP TABLE IF EXISTS demo_entity;