-- Orchestrators keep runs' folded history in memory and drop it when the
-- tenant erases a subject; this finds the latest erasure without a scan.

-- +goose Up
CREATE INDEX subject_keys_shredded ON subject_keys (tenant_id, shredded_at) WHERE shredded_at IS NOT NULL;

-- +goose Down
DROP INDEX subject_keys_shredded;
