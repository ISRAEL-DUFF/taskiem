-- Publishing needs a second person per environment (spec 3.4, engine
-- findings 5e): four_eyes_publish keeps meaning every environment; these
-- are the environments that need one when it is off.

-- +goose Up
ALTER TABLE governance_settings ADD COLUMN four_eyes_publish_environments text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE governance_settings DROP COLUMN four_eyes_publish_environments;
