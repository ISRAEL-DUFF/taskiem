-- A button's id is random (never worked out from its run), and the place
-- it was drawn at (a run's answer card, a row, a message) finds it again,
-- so an answer drawn many times mints one row per button.
-- +goose Up
ALTER TABLE tool_buttons ADD COLUMN place text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX tool_buttons_place ON tool_buttons (tenant_id, from_run, place) WHERE from_run IS NOT NULL;

-- +goose Down
DROP INDEX tool_buttons_place;
ALTER TABLE tool_buttons DROP COLUMN place;
