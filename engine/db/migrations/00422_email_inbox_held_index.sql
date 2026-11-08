-- +goose NO TRANSACTION
-- A run that starts waiting for an email reply releases the held replies
-- of its thread (taskiem_email_inbox_release, 00421): found by this
-- index, not by reading the inbox. Built CONCURRENTLY (CONTRIBUTING.md,
-- Migrations).

-- +goose Up
CREATE INDEX CONCURRENTLY email_inbox_held ON email_inbox (tenant_id, held_thread) WHERE held_thread IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS email_inbox_held;
