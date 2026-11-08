-- Run files a person sent are erased with that person (decision 0062,
-- gap 16): the file records the subject it is about (the vault's subject
-- id for the sender's number or chat, never the value), and erasing that
-- subject deletes its files at once. A daily cap per organisation reads
-- created_at (gap 12).

-- +goose Up
ALTER TABLE run_files ADD COLUMN subject text;
CREATE INDEX run_files_subject ON run_files (tenant_id, subject) WHERE subject IS NOT NULL;
CREATE INDEX run_files_created ON run_files (tenant_id, created_at);

-- +goose Down
DROP INDEX run_files_created;
DROP INDEX run_files_subject;
ALTER TABLE run_files DROP COLUMN subject;
