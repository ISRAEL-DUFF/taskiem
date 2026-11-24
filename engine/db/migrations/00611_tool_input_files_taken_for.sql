-- Device fields, H3 review (docs/spec/device-fields.md §17). A tool's
-- held file names the start that took it (its trigger and dedup key, as
-- trigger_receipts has them), so the same start sent again (its
-- Idempotency-Key, a chat's nonce) takes it again and is answered with
-- the run it started, and a file whose run could not be set on it at once
-- is given to that run later (forms.Service.AttachTakenToolFiles).
-- +goose Up
ALTER TABLE tool_input_files ADD COLUMN taken_trigger text CHECK (taken_trigger IS NULL OR length(taken_trigger) <= 200),
  ADD COLUMN taken_dedup text CHECK (taken_dedup IS NULL OR length(taken_dedup) <= 400);
CREATE INDEX tool_input_files_taken ON tool_input_files (tenant_id, taken_trigger, taken_dedup) WHERE taken_at IS NOT NULL;
-- When a code was asked for in a chat: an asking lasts ten minutes
-- (forms.CodeAskFor), so a message long after, or one meant for another
-- business on the same number, is never taken as the code.
ALTER TABLE form_chat_answers ADD COLUMN asked_at timestamptz;

-- +goose Down
ALTER TABLE form_chat_answers DROP COLUMN asked_at;
DROP INDEX tool_input_files_taken;
ALTER TABLE tool_input_files DROP COLUMN taken_dedup, DROP COLUMN taken_trigger;
