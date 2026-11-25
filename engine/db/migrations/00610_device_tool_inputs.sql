-- Device fields, build-plan-gaps H3 (docs/spec/device-fields.md §17).
--
-- tool_input_files: a photo or a signature a member sent for a tool's
-- input (POST /v1/tools/{command}/files) before starting it, scanned,
-- cleaned and kept as the forms' files are: a form submission of their
-- own (no endpoint, no request, held a day with retain_until) whose file
-- this row names, for that person, tool and input only. A start takes it
-- once (taken_at), and the run it starts is set on its submission, so the
-- file goes with the run's data (taskiem_purge_run) or after the day when
-- no run took it (taskiem_form_submissions_expire).
--
-- form_chat_answers.asking: a form step's scan asked in WhatsApp or
-- Telegram (g-scan-wa) is answered by the code typed as the person's next
-- message in that chat; asking names the field and the chats it was asked
-- in, and the next message in one of them clears it there.
-- +goose Up
CREATE TABLE tool_input_files (
  id             uuid PRIMARY KEY,           -- the form file's id
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  submission_id  uuid NOT NULL REFERENCES form_submissions(id) ON DELETE CASCADE,
  user_id        uuid NOT NULL,
  command        text NOT NULL CHECK (length(command) <= 64),
  field          text NOT NULL CHECK (length(field) <= 64),
  kind           text NOT NULL CHECK (kind IN ('photo', 'signature')),
  source         text NOT NULL CHECK (source IN ('camera', 'sent', 'drawn')),
  taken_at       timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX tool_input_files_submission ON tool_input_files (submission_id);
ALTER TABLE tool_input_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE tool_input_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tool_input_files TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON tool_input_files TO taskiem_app;

ALTER TABLE form_chat_answers ADD COLUMN asking text CHECK (asking IS NULL OR length(asking) <= 64),
  ADD COLUMN asking_in text[] NOT NULL DEFAULT '{}';

-- +goose Down
ALTER TABLE form_chat_answers DROP COLUMN asking_in, DROP COLUMN asking;
DROP TABLE tool_input_files;
