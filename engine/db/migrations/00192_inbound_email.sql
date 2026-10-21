-- +goose Up
-- Inbound email (decision 0038, no-technical-steps N10): a workflow with
-- an email trigger has an address, <public_id>@<mail domain>, per
-- environment. The mail role receives mail for it over SMTP and, like the
-- form host, holds no vault key and reads no table: it connects as
-- taskiem_public, checks a recipient and leaves the message sealed with
-- the public key (TASKIEM_PUBLIC_KEY) in email_inbox through the
-- functions below. The api role opens it, parses it, keeps its
-- attachments in the organisation's Xenibase storage, seals the personal
-- parts and starts the run.

ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront', 'form', 'email'));

-- One address per workflow and environment, stable across versions.
CREATE TABLE email_endpoints (
  id            uuid PRIMARY KEY,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  workflow_id   uuid NOT NULL,
  environment   text NOT NULL,
  public_id     text NOT NULL UNIQUE CHECK (public_id ~ '^[a-z0-9]{12}$'),
  version       int NOT NULL,
  status        text NOT NULL DEFAULT 'live' CHECK (status IN ('live', 'closed', 'suspended')),
  -- The latest forwarding confirmation a mail provider sent to the
  -- address ({provider, code, link, received_at}), shown to the
  -- organisation so it can finish setting up forwarding.
  confirmation  jsonb,
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (workflow_id, environment),
  FOREIGN KEY (tenant_id, workflow_id) REFERENCES workflows (tenant_id, id)
);
ALTER TABLE email_endpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_endpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_endpoints TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON email_endpoints TO taskiem_app;
CREATE POLICY dispatch ON email_endpoints TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE ON email_endpoints TO taskiem_dispatch;

-- What the mail role writes (platform data): the message and what the
-- receiving server knew of it (envelope, SPF, DKIM, DMARC), both sealed.
-- idem is the message's identity at that address (its Message-ID, else a
-- hash of it), so a message delivered twice is kept once.
CREATE TABLE email_inbox (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  endpoint_id    uuid NOT NULL REFERENCES email_endpoints(id) ON DELETE CASCADE,
  idem           bytea NOT NULL UNIQUE CHECK (length(idem) = 32),
  meta           bytea NOT NULL CHECK (length(meta) <= 65536),
  raw            bytea NOT NULL CHECK (length(raw) <= 26300000), -- a message is at most 25 MB
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz
);
ALTER TABLE email_inbox ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_inbox FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON email_inbox TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON email_inbox TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, INSERT, UPDATE, DELETE ON email_inbox TO taskiem_dispatch;

-- What arrived and what came of it, for the organisation: no content,
-- which is in the run (sealed where personal).
CREATE TABLE email_messages (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  endpoint_id    uuid REFERENCES email_endpoints(id) ON DELETE SET NULL,
  workflow_id    uuid NOT NULL,
  environment    text NOT NULL,
  idem           bytea NOT NULL CHECK (length(idem) = 32),
  sender_domain  text,
  size           int NOT NULL,
  attachments    int NOT NULL DEFAULT 0,
  status         text NOT NULL CHECK (status IN ('started', 'refused', 'confirmation')),
  note           text,
  run_id         uuid,
  received_at    timestamptz NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (endpoint_id, idem)
);
CREATE INDEX email_messages_list ON email_messages (tenant_id, workflow_id, created_at DESC);
ALTER TABLE email_messages ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_messages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_messages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON email_messages TO taskiem_app;
CREATE POLICY dispatch ON email_messages FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (endpoint_id, tenant_id, idem, created_at) ON email_messages TO taskiem_dispatch;

-- Attachments kept, in the organisation's Xenibase storage.
CREATE TABLE email_files (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  message_id     uuid NOT NULL REFERENCES email_messages(id) ON DELETE CASCADE,
  name           text NOT NULL,
  type           text NOT NULL,
  size           bigint NOT NULL,
  sha256         bytea NOT NULL CHECK (length(sha256) = 32),
  storage_key    text NOT NULL,
  created_at     timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE email_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE email_files FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON email_files TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON email_files TO taskiem_app;

-- +goose StatementBegin
-- Whether mail for an address is taken: its endpoint, when the address is
-- live and its organisation active. The mail role asks at RCPT, so mail
-- for anything else is refused before it is sent, not bounced later.
CREATE FUNCTION taskiem_mail_recipient(p_public_id text)
RETURNS uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT e.id FROM email_endpoints e JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
   WHERE e.public_id = lower(p_public_id) AND e.status = 'live'
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A message for an endpoint. A repeat of p_idem (in the inbox, or already
-- processed) answers with true and stores nothing. At most 60 messages an
-- hour reach one address and 300 one organisation: past that, and for an
-- address no longer live, it answers false and the mail role refuses the
-- message with a temporary error, so the sender tries again later.
CREATE FUNCTION taskiem_mail_receive(p_id uuid, p_endpoint uuid, p_idem bytea, p_meta bytea, p_raw bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
BEGIN
  SELECT e.tenant_id INTO t FROM email_endpoints e JOIN tenants tn ON tn.id = e.tenant_id AND tn.status = 'active'
   WHERE e.id = p_endpoint AND e.status = 'live';
  IF t IS NULL THEN
    RETURN false;
  END IF;
  IF EXISTS (SELECT 1 FROM email_inbox i WHERE i.idem = p_idem)
     OR EXISTS (SELECT 1 FROM email_messages m WHERE m.endpoint_id = p_endpoint AND m.idem = p_idem) THEN
    RETURN true;
  END IF;
  IF (SELECT count(*) FROM email_inbox i WHERE i.endpoint_id = p_endpoint AND i.created_at > now() - interval '1 hour')
     + (SELECT count(*) FROM email_messages m WHERE m.endpoint_id = p_endpoint AND m.created_at > now() - interval '1 hour') >= 60
     OR (SELECT count(*) FROM email_inbox i WHERE i.tenant_id = t AND i.created_at > now() - interval '1 hour')
     + (SELECT count(*) FROM email_messages m WHERE m.tenant_id = t AND m.created_at > now() - interval '1 hour') >= 300 THEN
    RETURN false;
  END IF;
  INSERT INTO email_inbox (id, tenant_id, endpoint_id, idem, meta, raw) VALUES (p_id, t, p_endpoint, p_idem, p_meta, p_raw)
    ON CONFLICT (idem) DO NOTHING;
  RETURN true;
END
$$;
-- +goose StatementEnd

-- The api role's side.
-- +goose StatementBegin
CREATE FUNCTION taskiem_email_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, endpoint_id uuid, idem bytea, meta bytea, raw bytea, created_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE email_inbox i SET claimed_until = now() + interval '5 minutes'
   WHERE i.id IN (SELECT x.id FROM email_inbox x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.tenant_id, i.endpoint_id, i.idem, i.meta, i.raw, i.created_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_email_inbox_done(p_id uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM email_inbox WHERE id = p_id
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_mail_recipient(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_mail_receive(uuid, uuid, bytea, bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_email_inbox_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_email_inbox_done(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_mail_recipient(text), taskiem_mail_receive(uuid, uuid, bytea, bytea, bytea),
  taskiem_email_inbox_claim(int), taskiem_email_inbox_done(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_mail_recipient(text), taskiem_mail_receive(uuid, uuid, bytea, bytea, bytea) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_email_inbox_claim(int), taskiem_email_inbox_done(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_email_inbox_done(uuid);
DROP FUNCTION taskiem_email_inbox_claim(int);
DROP FUNCTION taskiem_mail_receive(uuid, uuid, bytea, bytea, bytea);
DROP FUNCTION taskiem_mail_recipient(text);
DROP TABLE email_files;
DROP TABLE email_messages;
DROP TABLE email_inbox;
DROP TABLE email_endpoints;
DELETE FROM triggers WHERE type = 'email';
ALTER TABLE triggers DROP CONSTRAINT triggers_type_check;
ALTER TABLE triggers ADD CONSTRAINT triggers_type_check CHECK (type IN ('webhook', 'schedule', 'connector_event', 'ussd', 'storefront', 'form'));
