-- Slack as a conversation (docs/spec/chat-way-in.md 8, decision 0058).
--
-- A direct message to Taskiem's Slack bot from a linked member reaches the
-- same conversation WhatsApp and Telegram have. A DM names only the
-- workspace and the user, and a workspace may hold several businesses, so
-- the conversation keeps a current business per Slack user (sl:<team>:<user>),
-- as WhatsApp's and Telegram's contacts do: a person's, not a tenant's,
-- reached only through the functions below. Slack retries an event it
-- thinks was missed: each event id is claimed once and kept a day.
-- +goose Up
CREATE TABLE slack_contacts (
  address          text PRIMARY KEY CHECK (address ~ '^sl:[A-Z0-9]{1,24}:[A-Z0-9]{1,24}$'),
  current_tenant   uuid REFERENCES tenants(id),
  last_inbound_at  timestamptz,
  updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE slack_events_seen (
  event_id     text PRIMARY KEY CHECK (length(event_id) BETWEEN 1 AND 128),
  received_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ON slack_events_seen (received_at);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['slack_contacts', 'slack_events_seen'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY no_direct_access ON %I TO taskiem_app USING (false) WITH CHECK (false)', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- A Slack user's conversation: the business it speaks to and when it
-- last wrote.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_contact(p_address text)
RETURNS TABLE (current_tenant uuid, last_inbound_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.current_tenant, c.last_inbound_at FROM slack_contacts c WHERE c.address = p_address
$$;
-- +goose StatementEnd

-- Records that a Slack user wrote (p_inbound) and, with p_set_tenant, the
-- business it now speaks to.
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_contact_touch(p_address text, p_inbound boolean, p_set_tenant boolean, p_tenant uuid)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO slack_contacts AS c (address, current_tenant, last_inbound_at)
  VALUES (p_address, CASE WHEN p_set_tenant THEN p_tenant END, CASE WHEN p_inbound THEN now() END)
  ON CONFLICT (address) DO UPDATE SET
    current_tenant = CASE WHEN p_set_tenant THEN p_tenant ELSE c.current_tenant END,
    last_inbound_at = CASE WHEN p_inbound THEN now() ELSE c.last_inbound_at END,
    updated_at = now()
$$;
-- +goose StatementEnd

-- Claims an event id: true the first time. Ids older than a day are
-- forgotten (Slack retries within minutes).
-- +goose StatementBegin
CREATE FUNCTION taskiem_slack_event_claim(p_id text)
RETURNS boolean LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF random() < 0.01 THEN
    DELETE FROM slack_events_seen e WHERE e.received_at < now() - interval '1 day';
  END IF;
  INSERT INTO slack_events_seen (event_id) VALUES (p_id) ON CONFLICT DO NOTHING;
  GET DIAGNOSTICS n = ROW_COUNT;
  RETURN n = 1;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_slack_contact(text)', 'taskiem_slack_contact_touch(text, boolean, boolean, uuid)', 'taskiem_slack_event_claim(text)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- A build drafted in a Slack DM is recorded as Slack's.
ALTER TABLE ai_builds DROP CONSTRAINT ai_builds_channel_check;
ALTER TABLE ai_builds ADD CONSTRAINT ai_builds_channel_check CHECK (channel IN ('web', 'whatsapp', 'telegram', 'slack'));

-- +goose Down
UPDATE ai_builds SET channel = 'web' WHERE channel = 'slack';
ALTER TABLE ai_builds DROP CONSTRAINT ai_builds_channel_check;
ALTER TABLE ai_builds ADD CONSTRAINT ai_builds_channel_check CHECK (channel IN ('web', 'whatsapp', 'telegram'));
DROP FUNCTION taskiem_slack_event_claim(text);
DROP FUNCTION taskiem_slack_contact_touch(text, boolean, boolean, uuid);
DROP FUNCTION taskiem_slack_contact(text);
DROP TABLE slack_events_seen;
DROP TABLE slack_contacts;
