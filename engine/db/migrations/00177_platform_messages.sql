-- Messages to people outside a tenant (Phase 5, S1; docs/messages.md):
-- the customers of a storefront and the respondents of a hosted form, who
-- have no Taskiem account and are reached by SMS or email through the
-- platform's own providers, on a tenant's behalf.
--
-- platform_messages is a durable outbox. A message is queued in the same
-- transaction as whatever caused it, so it is never lost; each attempt is
-- claimed once; failures back off and, after the last attempt, stay as dead
-- letters. The recipient and the text are sealed under the recipient's
-- personal-data subject, so erasing that person erases their queued
-- messages, and both are wiped once the message is finished. What remains
-- (when, which provider, how many SMS segments) is for usage and support.
--
-- platform_message_recipients counts messages per recipient across every
-- tenant, by a keyed hash of the address, so a public form cannot be used
-- to flood someone's phone or inbox.
-- +goose Up
CREATE TABLE platform_messages (
  id               uuid PRIMARY KEY,
  tenant_id        uuid NOT NULL REFERENCES tenants(id),
  channel          text NOT NULL CHECK (channel IN ('sms', 'email')),
  purpose          text NOT NULL CHECK (purpose IN ('code', 'confirmation', 'link', 'notice')),
  dedup_key        text NOT NULL CHECK (length(dedup_key) BETWEEN 1 AND 200),
  subject_id       text NOT NULL,
  recipient        jsonb,
  body             jsonb,
  sender_name      text NOT NULL CHECK (length(sender_name) BETWEEN 1 AND 60),
  status           text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'sent', 'dead', 'unknown', 'dropped')),
  attempts         int NOT NULL DEFAULT 0,
  next_attempt_at  timestamptz NOT NULL DEFAULT now(),
  expires_at       timestamptz,
  provider         text,
  provider_ref     text,
  segments         int,
  last_error       text,
  created_at       timestamptz NOT NULL DEFAULT now(),
  finished_at      timestamptz,
  UNIQUE (tenant_id, dedup_key)
);
CREATE INDEX platform_messages_due ON platform_messages (tenant_id, next_attempt_at) WHERE status = 'pending';
CREATE INDEX platform_messages_usage ON platform_messages (tenant_id, finished_at) WHERE status = 'sent';
CREATE INDEX platform_messages_subject ON platform_messages (tenant_id, subject_id);
ALTER TABLE platform_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON platform_messages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON platform_messages TO taskiem_app;
-- The sender finds tenants with something due without entering each.
CREATE POLICY dispatch ON platform_messages FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, status, next_attempt_at) ON platform_messages TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_platform_message_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT m.tenant_id FROM platform_messages m
    JOIN tenants t ON t.id = m.tenant_id AND t.status = 'active'
   WHERE m.status = 'pending' AND m.next_attempt_at <= now()
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_platform_message_tenants() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_platform_message_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_platform_message_tenants() TO taskiem_app;

-- Per recipient, across tenants: fixed hour and day windows.
CREATE TABLE platform_message_recipients (
  recipient_hash  bytea PRIMARY KEY CHECK (length(recipient_hash) = 32),
  hour_start      timestamptz NOT NULL,
  hour_count      int NOT NULL,
  day_start       timestamptz NOT NULL,
  day_count       int NOT NULL
);
ALTER TABLE platform_message_recipients ENABLE ROW LEVEL SECURITY;
ALTER TABLE platform_message_recipients FORCE ROW LEVEL SECURITY;
-- Only the function below reaches it.
CREATE POLICY no_direct_access ON platform_message_recipients TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON platform_message_recipients TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON platform_message_recipients TO taskiem_dispatch;

-- Counts one message to a recipient if it is within both limits, and says
-- whether it was. Windows older than a day are forgotten as it goes.
-- +goose StatementBegin
CREATE FUNCTION taskiem_platform_recipient_take(p_hash bytea, p_per_hour int, p_per_day int)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r platform_message_recipients;
  hour_now timestamptz := date_trunc('hour', now());
  day_now timestamptz := date_trunc('day', now());
BEGIN
  DELETE FROM platform_message_recipients WHERE recipient_hash IN (
    SELECT recipient_hash FROM platform_message_recipients
     WHERE day_start < day_now - interval '1 day' AND recipient_hash <> p_hash LIMIT 100);
  INSERT INTO platform_message_recipients VALUES (p_hash, hour_now, 0, day_now, 0)
    ON CONFLICT (recipient_hash) DO NOTHING;
  SELECT * INTO r FROM platform_message_recipients WHERE recipient_hash = p_hash FOR UPDATE;
  IF r.hour_start < hour_now THEN r.hour_start := hour_now; r.hour_count := 0; END IF;
  IF r.day_start < day_now THEN r.day_start := day_now; r.day_count := 0; END IF;
  IF r.hour_count >= p_per_hour OR r.day_count >= p_per_day THEN
    RETURN false;
  END IF;
  UPDATE platform_message_recipients SET hour_start = r.hour_start, hour_count = r.hour_count + 1,
         day_start = r.day_start, day_count = r.day_count + 1
   WHERE recipient_hash = p_hash;
  RETURN true;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_platform_recipient_take(bytea, int, int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_platform_recipient_take(bytea, int, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_platform_recipient_take(bytea, int, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_platform_recipient_take(bytea, int, int);
DROP TABLE platform_message_recipients;
DROP FUNCTION taskiem_platform_message_tenants();
DROP TABLE platform_messages;
