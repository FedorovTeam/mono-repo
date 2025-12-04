-- +goose Up
ALTER TABLE demo_entity DROP CONSTRAINT IF EXISTS demo_entity_pkey;
ALTER TABLE demo_entity ADD PRIMARY KEY ((bucket_id) HASH, id);

-- +goose Down
ALTER TABLE demo_entity DROP CONSTRAINT IF EXISTS demo_entity_pkey;
ALTER TABLE demo_entity ADD PRIMARY KEY (id);
