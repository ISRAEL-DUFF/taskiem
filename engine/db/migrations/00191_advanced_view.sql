-- +goose Up
-- What a business sees (no-technical-steps N0-5, N8): the technical parts
-- of the web app (the dev environment, raw definitions, expressions,
-- cron, policies' conditions, own connectors) are shown when advanced is
-- on. NULL is the plan's default: on above the Shop plan.
ALTER TABLE tenants ADD COLUMN advanced boolean;

-- +goose Down
ALTER TABLE tenants DROP COLUMN advanced;
