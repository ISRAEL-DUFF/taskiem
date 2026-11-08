-- Speech in workflows (decision 0062 D5, D7; plan V3): speech@1 hears a
-- customer's voice note and speaks a reply.
--
--   speech_seconds_monthly  seconds of speech, heard and spoken, a tenant's
--                           workflows may use per UTC month (0: no limit);
--                           set per plan, separate from AI building's
--                           tokens so an assistant never starves building
--   speech_usage            what each tenant used per month: seconds heard
--                           and spoken, characters spoken, requests and
--                           refusals (switch off, allowance used up)
--   workflow_voice          the owner's "Voice in workflows" switch,
--                           separate from voice notes in Taskiem's own chat
--                           (voice_notes); off until the owner turns it on

-- +goose Up
ALTER TABLE tenant_limits
  ADD COLUMN speech_seconds_monthly bigint CHECK (speech_seconds_monthly >= 0);

CREATE TABLE speech_usage (
  tenant_id          uuid NOT NULL REFERENCES tenants(id),
  month              date NOT NULL CHECK (month = date_trunc('month', month)::date),
  seconds_heard      bigint NOT NULL DEFAULT 0 CHECK (seconds_heard >= 0),
  seconds_spoken     bigint NOT NULL DEFAULT 0 CHECK (seconds_spoken >= 0),
  characters_spoken  bigint NOT NULL DEFAULT 0 CHECK (characters_spoken >= 0),
  requests           bigint NOT NULL DEFAULT 0 CHECK (requests >= 0),
  refused            bigint NOT NULL DEFAULT 0 CHECK (refused >= 0),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, month)
);
ALTER TABLE speech_usage ENABLE ROW LEVEL SECURITY;
ALTER TABLE speech_usage FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON speech_usage TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON speech_usage TO taskiem_app;

ALTER TABLE tenant_channel_settings ADD COLUMN workflow_voice boolean NOT NULL DEFAULT false;

-- +goose Down
ALTER TABLE tenant_channel_settings DROP COLUMN workflow_voice;
DROP TABLE speech_usage;
ALTER TABLE tenant_limits DROP COLUMN speech_seconds_monthly;
