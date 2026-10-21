-- +goose Up
-- Reaching people who are not on Taskiem (no-technical-steps N11, decision
-- 0039): a business's staff and customers opt in once, with a one-tap link
-- to the platform's Telegram bot or WhatsApp number, and every workflow
-- that messages their phone then reaches them there (notify@1 message).
--
-- A contact is the business's, keyed by a keyed hash of a phone number
-- the person proved: shared through Telegram, sent from on WhatsApp, or
-- verified by the form's code. A link made without a proven phone reaches
-- the person only about the submission it came from (reach_request_links).
-- Phone numbers and chat addresses are sealed under the person's
-- personal-data subject; only keyed hashes are compared.

CREATE TABLE reach_contacts (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  phone_hash  bytea NOT NULL CHECK (length(phone_hash) = 32),
  phone       jsonb NOT NULL,  -- sealed E.164
  created_at  timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, phone_hash)
);

-- A contact's chat channels: one Telegram account, one WhatsApp number.
CREATE TABLE reach_channels (
  contact_id    uuid NOT NULL REFERENCES reach_contacts(id) ON DELETE CASCADE,
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  channel       text NOT NULL CHECK (channel IN ('telegram', 'whatsapp')),
  address_hash  bytea NOT NULL CHECK (length(address_hash) = 32),
  address       jsonb NOT NULL,  -- sealed tg:<user id> or +E.164
  linked_at     timestamptz NOT NULL DEFAULT now(),
  last_sent_at  timestamptz,
  stopped_at    timestamptz,
  PRIMARY KEY (contact_id, channel)
);
CREATE INDEX reach_channels_address ON reach_channels (channel, address_hash);

-- A link about one submission only (its reference), made when no phone
-- was proven: it reaches the person when the run of that submission
-- messages one of the phones they typed in.
CREATE TABLE reach_request_links (
  tenant_id     uuid NOT NULL REFERENCES tenants(id),
  reference     text NOT NULL,
  channel       text NOT NULL CHECK (channel IN ('telegram', 'whatsapp')),
  address_hash  bytea NOT NULL CHECK (length(address_hash) = 32),
  address       jsonb NOT NULL,
  phone_hashes  bytea[] NOT NULL,
  created_at    timestamptz NOT NULL DEFAULT now(),
  last_sent_at  timestamptz,
  stopped_at    timestamptz,
  PRIMARY KEY (tenant_id, reference, channel)
);
CREATE INDEX reach_request_links_address ON reach_request_links (channel, address_hash);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['reach_contacts', 'reach_channels', 'reach_request_links'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('GRANT SELECT, UPDATE ON %I TO taskiem_dispatch', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- One-time link codes, kept as SHA-256. A thank-you page makes them with
-- the submission's reference and the phones typed in it, sealed with the
-- public key for the api role (payload); a business's standing page makes
-- them with neither. Seven days, single use: taking one records who took
-- it, so a Telegram account can still prove its phone afterwards.
CREATE TABLE reach_codes (
  code_hash      bytea PRIMARY KEY CHECK (length(code_hash) = 32),
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  reference      text,
  payload        bytea CHECK (length(payload) <= 4096),
  expires_at     timestamptz NOT NULL,
  taken_channel  text CHECK (taken_channel IN ('telegram', 'whatsapp')),
  taken_hash     bytea,
  taken_at       timestamptz,
  created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reach_codes_taken ON reach_codes (taken_channel, taken_hash) WHERE taken_at IS NOT NULL;
CREATE INDEX reach_codes_tenant ON reach_codes (tenant_id, created_at);

-- Where links point: the platform bot's username and the platform's
-- WhatsApp number, recorded by the api role at start (not secrets).
CREATE TABLE reach_targets (
  channel     text PRIMARY KEY CHECK (channel IN ('telegram', 'whatsapp')),
  target      text NOT NULL CHECK (target ~ '^[A-Za-z0-9_]{5,32}$' OR target ~ '^\+[1-9][0-9]{6,14}$'),
  updated_at  timestamptz NOT NULL DEFAULT now()
);

-- A business's standing page, /u/<public id> on the form host.
CREATE TABLE reach_pages (
  tenant_id   uuid PRIMARY KEY REFERENCES tenants(id),
  public_id   text NOT NULL UNIQUE CHECK (public_id ~ '^[a-z0-9]{12}$'),
  created_at  timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE reach_pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE reach_pages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON reach_pages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON reach_pages TO taskiem_app;
CREATE POLICY dispatch ON reach_pages TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON reach_pages TO taskiem_dispatch;

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['reach_codes', 'reach_targets'] LOOP
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

-- +goose StatementBegin
-- Makes a code for a live business: for a submission (its reference, in
-- the inbox or already started) or for its standing page (no reference).
-- At most 2,000 codes an hour per business. false: not made.
CREATE FUNCTION taskiem_reach_code_put(p_code_hash bytea, p_tenant uuid, p_reference text, p_payload bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = p_tenant AND t.status = 'active') THEN
    RETURN false;
  END IF;
  IF p_reference IS NOT NULL AND NOT EXISTS (SELECT 1 FROM form_inbox i WHERE i.reference = p_reference AND i.tenant_id = p_tenant)
     AND NOT EXISTS (SELECT 1 FROM form_submissions s JOIN form_endpoints e ON e.id = s.endpoint_id WHERE s.reference = p_reference AND e.tenant_id = p_tenant) THEN
    RETURN false;
  END IF;
  IF (SELECT count(*) FROM reach_codes c WHERE c.tenant_id = p_tenant AND c.created_at > now() - interval '1 hour') >= 2000 THEN
    RETURN false;
  END IF;
  IF random() < 0.01 THEN
    DELETE FROM reach_codes c WHERE c.expires_at < now() - interval '1 day';
  END IF;
  INSERT INTO reach_codes (code_hash, tenant_id, reference, payload, expires_at)
  VALUES (p_code_hash, p_tenant, p_reference, p_payload, now() + interval '7 days') ON CONFLICT DO NOTHING;
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Takes a live code once, for the chat address (its keyed hash) that sent
-- it, with its business.
CREATE FUNCTION taskiem_reach_code_take(p_code_hash bytea, p_channel text, p_address_hash bytea)
RETURNS TABLE (tenant_id uuid, business text, reference text, payload bytea)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE reach_codes c SET taken_channel = p_channel, taken_hash = p_address_hash, taken_at = now()
    FROM tenants t
   WHERE c.code_hash = p_code_hash AND c.taken_at IS NULL AND c.expires_at > now() AND t.id = c.tenant_id AND t.status = 'active'
  RETURNING c.tenant_id, t.name, c.reference, c.payload
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The code a chat address took in the last hour and has not finished
-- with: a Telegram account asked to share its phone.
CREATE FUNCTION taskiem_reach_code_pending(p_channel text, p_address_hash bytea)
RETURNS TABLE (code_hash bytea, tenant_id uuid, business text, reference text, payload bytea)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.code_hash, c.tenant_id, t.name, c.reference, c.payload FROM reach_codes c JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active'
   WHERE c.taken_channel = p_channel AND c.taken_hash = p_address_hash AND c.taken_at > now() - interval '1 hour'
   ORDER BY c.taken_at DESC LIMIT 1
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_reach_code_done(p_code_hash bytea)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM reach_codes c WHERE c.code_hash = p_code_hash
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The businesses a chat address gets updates from now, most recent first.
CREATE FUNCTION taskiem_reach_businesses(p_channel text, p_address_hash bytea)
RETURNS TABLE (business text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.name FROM (
    SELECT c.tenant_id, greatest(c.linked_at, c.last_sent_at) AS at FROM reach_channels c
     WHERE c.channel = p_channel AND c.address_hash = p_address_hash AND c.stopped_at IS NULL
    UNION ALL
    SELECT l.tenant_id, greatest(l.created_at, l.last_sent_at) FROM reach_request_links l
     WHERE l.channel = p_channel AND l.address_hash = p_address_hash AND l.stopped_at IS NULL AND l.created_at > now() - interval '90 days'
  ) x JOIN tenants t ON t.id = x.tenant_id AND t.status = 'active'
  GROUP BY t.id, t.name ORDER BY max(x.at) DESC LIMIT 10
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- STOP: the business that messaged or linked the address most recently
-- stops reaching it on this channel. Returns its name, or NULL.
CREATE FUNCTION taskiem_reach_stop(p_channel text, p_address_hash bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  n text;
BEGIN
  SELECT x.tenant_id INTO t FROM (
    SELECT c.tenant_id, greatest(c.linked_at, c.last_sent_at) AS at FROM reach_channels c
     WHERE c.channel = p_channel AND c.address_hash = p_address_hash AND c.stopped_at IS NULL
    UNION ALL
    SELECT l.tenant_id, greatest(l.created_at, l.last_sent_at) FROM reach_request_links l
     WHERE l.channel = p_channel AND l.address_hash = p_address_hash AND l.stopped_at IS NULL
  ) x ORDER BY x.at DESC LIMIT 1;
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  UPDATE reach_channels c SET stopped_at = now() WHERE c.tenant_id = t AND c.channel = p_channel AND c.address_hash = p_address_hash AND c.stopped_at IS NULL;
  UPDATE reach_request_links l SET stopped_at = now() WHERE l.tenant_id = t AND l.channel = p_channel AND l.address_hash = p_address_hash AND l.stopped_at IS NULL;
  SELECT name INTO n FROM tenants WHERE id = t;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- START: the business stopped most recently reaches the address again.
CREATE FUNCTION taskiem_reach_resume(p_channel text, p_address_hash bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  t uuid;
  n text;
BEGIN
  SELECT x.tenant_id INTO t FROM (
    SELECT c.tenant_id, c.stopped_at AS at FROM reach_channels c
     WHERE c.channel = p_channel AND c.address_hash = p_address_hash AND c.stopped_at IS NOT NULL
    UNION ALL
    SELECT l.tenant_id, l.stopped_at FROM reach_request_links l
     WHERE l.channel = p_channel AND l.address_hash = p_address_hash AND l.stopped_at IS NOT NULL
  ) x ORDER BY x.at DESC LIMIT 1;
  IF t IS NULL THEN
    RETURN NULL;
  END IF;
  UPDATE reach_channels c SET stopped_at = NULL WHERE c.tenant_id = t AND c.channel = p_channel AND c.address_hash = p_address_hash;
  UPDATE reach_request_links l SET stopped_at = NULL WHERE l.tenant_id = t AND l.channel = p_channel AND l.address_hash = p_address_hash;
  SELECT name INTO n FROM tenants WHERE id = t;
  RETURN n;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Telegram told the bot it may not write to an account (the person
-- blocked it): every business stops reaching that account.
CREATE FUNCTION taskiem_reach_blocked(p_channel text, p_address_hash bytea)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE reach_channels c SET stopped_at = now() WHERE c.channel = p_channel AND c.address_hash = p_address_hash AND c.stopped_at IS NULL;
  UPDATE reach_request_links l SET stopped_at = now() WHERE l.channel = p_channel AND l.address_hash = p_address_hash AND l.stopped_at IS NULL;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_reach_target_set(p_channel text, p_target text)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  INSERT INTO reach_targets (channel, target) VALUES (p_channel, p_target)
  ON CONFLICT (channel) DO UPDATE SET target = EXCLUDED.target, updated_at = now();
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE FUNCTION taskiem_reach_target_clear(p_channel text)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM reach_targets WHERE channel = p_channel;
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Where a business's links point: the platform bot, and its own WhatsApp
-- number when it has an active one, else the platform's.
CREATE FUNCTION taskiem_reach_targets(p_tenant uuid)
RETURNS TABLE (telegram text, whatsapp text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT (SELECT r.target FROM reach_targets r WHERE r.channel = 'telegram'),
         coalesce((SELECT n.display_number FROM whatsapp_numbers n WHERE n.tenant_id = p_tenant AND n.status = 'active'),
                  (SELECT r.target FROM reach_targets r WHERE r.channel = 'whatsapp'))
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A standing page: its business, when live.
CREATE FUNCTION taskiem_reach_page(p_public_id text)
RETURNS TABLE (tenant_id uuid, business text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.tenant_id, t.name FROM reach_pages p JOIN tenants t ON t.id = p.tenant_id AND t.status = 'active' WHERE p.public_id = p_public_id
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_reach_code_put(bytea, uuid, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_code_take(bytea, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_code_pending(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_code_done(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_businesses(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_stop(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_resume(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_blocked(text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_target_set(text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_target_clear(text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_targets(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_reach_page(text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_reach_code_put(bytea, uuid, text, bytea), taskiem_reach_code_take(bytea, text, bytea),
  taskiem_reach_code_pending(text, bytea), taskiem_reach_code_done(bytea), taskiem_reach_businesses(text, bytea),
  taskiem_reach_stop(text, bytea), taskiem_reach_resume(text, bytea), taskiem_reach_blocked(text, bytea),
  taskiem_reach_target_set(text, text), taskiem_reach_target_clear(text), taskiem_reach_targets(uuid), taskiem_reach_page(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_reach_code_put(bytea, uuid, text, bytea), taskiem_reach_targets(uuid), taskiem_reach_page(text) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_reach_code_put(bytea, uuid, text, bytea), taskiem_reach_code_take(bytea, text, bytea),
  taskiem_reach_code_pending(text, bytea), taskiem_reach_code_done(bytea), taskiem_reach_businesses(text, bytea),
  taskiem_reach_stop(text, bytea), taskiem_reach_resume(text, bytea), taskiem_reach_blocked(text, bytea),
  taskiem_reach_target_set(text, text), taskiem_reach_target_clear(text), taskiem_reach_targets(uuid) TO taskiem_app;

-- The chat outbox carries messages to contacts too: a contact's channel,
-- or a submission's link, instead of a member.
ALTER TABLE whatsapp_outbox ALTER COLUMN user_id DROP NOT NULL;
ALTER TABLE whatsapp_outbox ADD COLUMN contact_id uuid, ADD COLUMN reach_reference text;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice', 'message'));
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_to_check
  CHECK ((kind = 'message') = (user_id IS NULL) AND (kind <> 'message' OR (contact_id IS NULL) <> (reach_reference IS NULL)));

-- The form host learns which fields hold phones a workflow messages, so a
-- thank-you page offers the links only when they will be used.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form(p_public_id text)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('endpoint_id', e.id, 'tenant_id', e.tenant_id, 'environment', e.environment, 'version', e.version, 'status', e.status,
           'open', e.status = 'live' AND (e.opens_at IS NULL OR e.opens_at <= now()) AND (e.closes_at IS NULL OR e.closes_at > now())
             AND (e.max_submissions IS NULL OR e.submissions < e.max_submissions),
           'closed_message', e.closed_message, 'business', t.name,
           'trigger', e.form->'trigger', 'inputs', e.form->'inputs', 'types', e.form->'types', 'reach', e.form->'reach')
    FROM form_endpoints e
    JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
   WHERE e.public_id = p_public_id AND e.status <> 'suspended'
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_public_form(p_public_id text)
RETURNS jsonb
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT jsonb_build_object('endpoint_id', e.id, 'tenant_id', e.tenant_id, 'environment', e.environment, 'version', e.version, 'status', e.status,
           'open', e.status = 'live' AND (e.opens_at IS NULL OR e.opens_at <= now()) AND (e.closes_at IS NULL OR e.closes_at > now())
             AND (e.max_submissions IS NULL OR e.submissions < e.max_submissions),
           'closed_message', e.closed_message, 'business', t.name,
           'trigger', e.form->'trigger', 'inputs', e.form->'inputs', 'types', e.form->'types')
    FROM form_endpoints e
    JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
   WHERE e.public_id = p_public_id AND e.status <> 'suspended'
$$;
-- +goose StatementEnd
DELETE FROM whatsapp_outbox WHERE kind = 'message';
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_to_check;
ALTER TABLE whatsapp_outbox DROP CONSTRAINT whatsapp_outbox_kind_check;
ALTER TABLE whatsapp_outbox ADD CONSTRAINT whatsapp_outbox_kind_check CHECK (kind IN ('approval', 'run_outcome', 'alert', 'notice'));
ALTER TABLE whatsapp_outbox DROP COLUMN reach_reference, DROP COLUMN contact_id;
ALTER TABLE whatsapp_outbox ALTER COLUMN user_id SET NOT NULL;
DROP FUNCTION taskiem_reach_page(text);
DROP FUNCTION taskiem_reach_targets(uuid);
DROP FUNCTION taskiem_reach_target_clear(text);
DROP FUNCTION taskiem_reach_target_set(text, text);
DROP FUNCTION taskiem_reach_blocked(text, bytea);
DROP FUNCTION taskiem_reach_resume(text, bytea);
DROP FUNCTION taskiem_reach_stop(text, bytea);
DROP FUNCTION taskiem_reach_businesses(text, bytea);
DROP FUNCTION taskiem_reach_code_done(bytea);
DROP FUNCTION taskiem_reach_code_pending(text, bytea);
DROP FUNCTION taskiem_reach_code_take(bytea, text, bytea);
DROP FUNCTION taskiem_reach_code_put(bytea, uuid, text, bytea);
DROP TABLE reach_pages;
DROP TABLE reach_targets;
DROP TABLE reach_codes;
DROP TABLE reach_request_links;
DROP TABLE reach_channels;
DROP TABLE reach_contacts;
