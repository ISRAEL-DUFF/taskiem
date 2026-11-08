-- Run files a person sent are erased with that person (decision 0062,
-- gap 16): the file records the subject it is about (the vault's subject
-- id for the sender's number or chat, never the value), and erasing that
-- subject deletes its files at once. A daily cap per organisation reads
-- created_at (gap 12). Erasure expires a person's files before deleting
-- them, so a failed delete leaves them unreadable for the sweep.

-- +goose Up
ALTER TABLE run_files ADD COLUMN subject text;
CREATE INDEX run_files_subject ON run_files (tenant_id, subject) WHERE subject IS NOT NULL;
CREATE INDEX run_files_created ON run_files (tenant_id, created_at);
GRANT UPDATE (expires_at) ON run_files TO taskiem_app;

-- +goose Down
REVOKE UPDATE (expires_at) ON run_files FROM taskiem_app;
DROP INDEX run_files_created;
DROP INDEX run_files_subject;
ALTER TABLE run_files DROP COLUMN subject;
