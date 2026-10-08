-- Voice notes transcribed on WhatsApp (spec 11.6, decision 0029). A
-- transcript is personal data: it is stored sealed (an x-pii envelope
-- under the tenant's subject keys, like inputs typed in chat), for the
-- retention the operator sets (TASKIEM_TRANSCRIBE_RETENTION, 7 days by
-- default), so native-speaker testers can compare what was said with what
-- was heard. Refused and failed notes keep only why. The audio itself is
-- never stored. Tenant data under forced row-level security; the WhatsApp
-- notifier deletes expired rows tenant by tenant.

-- +goose Up
CREATE TABLE voice_transcripts (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  user_id      uuid NOT NULL REFERENCES users(id),
  channel      text NOT NULL DEFAULT 'whatsapp' CHECK (channel IN ('whatsapp')),
  provider     text NOT NULL,
  language     text NOT NULL CHECK (language IN ('en', 'pcm', 'yo', 'ha', 'ig')),
  status       text NOT NULL CHECK (status IN ('transcribed', 'refused', 'failed')),
  reason       text CHECK (reason IN ('too_large', 'too_long', 'unsupported', 'empty', 'provider_error', 'download_error')),
  duration_ms  int,
  bytes        int,
  transcript   jsonb CHECK (transcript IS NULL OR (transcript ? '$pii' AND status = 'transcribed')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL
);
CREATE INDEX voice_transcripts_tenant ON voice_transcripts (tenant_id, created_at DESC);
CREATE INDEX voice_transcripts_expiry ON voice_transcripts (expires_at);
ALTER TABLE voice_transcripts ENABLE ROW LEVEL SECURITY;
ALTER TABLE voice_transcripts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON voice_transcripts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON voice_transcripts TO taskiem_app;

-- Retention across tenants: the dispatch role sees only when rows expire
-- (no transcript, no tenant data) and deletes those past it.
CREATE POLICY dispatch_purge ON voice_transcripts FOR DELETE TO taskiem_dispatch USING (expires_at <= now());
CREATE POLICY dispatch_read_expiry ON voice_transcripts FOR SELECT TO taskiem_dispatch USING (expires_at <= now());
GRANT SELECT (id, expires_at), DELETE ON voice_transcripts TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_voice_transcripts_purge()
RETURNS bigint LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n bigint;
BEGIN
  DELETE FROM voice_transcripts v WHERE v.expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_voice_transcripts_purge() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_voice_transcripts_purge() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_voice_transcripts_purge() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_voice_transcripts_purge();
DROP TABLE voice_transcripts;
