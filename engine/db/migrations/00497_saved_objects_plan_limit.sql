-- Saved objects (docs/spec/saved-objects.md §15 6): a business keeps up to
-- 2,000 saved things on the lowest plan, more on higher plans. The cap is a
-- plan limit like any other (deploy/plans.yaml; taskiem tenants limits),
-- the platform default 2,000 (TASKIEM_DEFAULT_MAX_SAVED_OBJECTS); 0: no
-- limit. A person's own saved things are capped apart (500).
-- +goose Up
ALTER TABLE tenant_limits ADD COLUMN max_saved_objects int CHECK (max_saved_objects >= 0);

-- +goose Down
ALTER TABLE tenant_limits DROP COLUMN max_saved_objects;
