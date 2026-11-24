-- Chat links (docs/spec/taskiem-for-everyone.md §8.2, §9, §12, §15; stage
-- 2, stream B). A business's chat link, https://<host>/m/{code}, opens a
-- customer conversation with it in the app or the browser.
--
-- * customer_chat_codes: one live code per business, ten characters with
--   no look-alikes, kept only as a keyed hash (the code is made again from
--   its nonce with the platform's key, so the owner can read the link back
--   and the database alone cannot). "Make a new link" replaces it; the old
--   code is kept for 30 days so it can say "This link no longer works".
-- * customer_chat_link_settings: whether the business takes chats, what its
--   storefront's chat button offers (WhatsApp, Taskiem or both), and
--   whether updates to outsiders on the shared number end with the link
--   (NULL: on exactly when the business has no number of its own).
-- * customer_chat_notices: "at most once per ..." for the browser's SMS
--   (once per conversation per hour) and the answer to an outsider who
--   replies to an update (once per business per day).
-- * customer_message_charges: sign-in codes sent by SMS to people who came
--   through a business's chat link, counted against that business's SMS
--   (§15 "Phone codes cost money"); phone_codes.came_from records the
--   business a code was asked for through.
-- * The definers: a code's business (taskiem_chat_code_find), the
--   businesses reaching a chat address with their ids
--   (taskiem_reach_business_list), and the business a phone code came
--   through (taskiem_auth_phone_code_came_from).

-- +goose Up
CREATE TABLE customer_chat_codes (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  code_hash    bytea NOT NULL UNIQUE,
  nonce        bytea NOT NULL CHECK (length(nonce) = 16),
  created_at   timestamptz NOT NULL DEFAULT now(),
  created_by   text NOT NULL,
  replaced_at  timestamptz
);
CREATE UNIQUE INDEX customer_chat_codes_live ON customer_chat_codes (tenant_id) WHERE replaced_at IS NULL;
ALTER TABLE customer_chat_codes ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_chat_codes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_chat_codes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_chat_codes TO taskiem_app;
CREATE POLICY dispatch ON customer_chat_codes FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, code_hash, replaced_at) ON customer_chat_codes TO taskiem_dispatch;

CREATE TABLE customer_chat_link_settings (
  tenant_id          uuid PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
  takes_chats        boolean NOT NULL DEFAULT true,
  storefront_button  text NOT NULL DEFAULT 'both' CHECK (storefront_button IN ('whatsapp', 'taskiem', 'both')),
  link_in_updates    boolean,
  updated_at         timestamptz NOT NULL DEFAULT now(),
  updated_by         text NOT NULL
);
ALTER TABLE customer_chat_link_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_chat_link_settings FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_chat_link_settings TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_chat_link_settings TO taskiem_app;

CREATE TABLE customer_chat_notices (
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  kind       text NOT NULL CHECK (kind IN ('answered_sms', 'outsider_link')),
  key        bytea NOT NULL,
  last_at    timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, kind, key)
);
CREATE INDEX customer_chat_notices_old ON customer_chat_notices (last_at);
ALTER TABLE customer_chat_notices ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_chat_notices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_chat_notices TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON customer_chat_notices TO taskiem_app;

CREATE TABLE customer_message_charges (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  channel     text NOT NULL CHECK (channel IN ('sms', 'whatsapp')),
  purpose     text NOT NULL CHECK (purpose IN ('signin_code')),
  segments    int NOT NULL DEFAULT 1 CHECK (segments > 0),
  created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX customer_message_charges_month ON customer_message_charges (tenant_id, created_at);
ALTER TABLE customer_message_charges ENABLE ROW LEVEL SECURITY;
ALTER TABLE customer_message_charges FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON customer_message_charges TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON customer_message_charges TO taskiem_app;

ALTER TABLE phone_codes ADD COLUMN came_from uuid REFERENCES tenants(id) ON DELETE SET NULL;

-- The business a chat code names: its live code, or one replaced in the
-- last 30 days (replaced_at set: "This link no longer works"). Only an
-- active business, not a workflows workspace.
-- +goose StatementBegin
CREATE FUNCTION taskiem_chat_code_find(p_hash bytea)
RETURNS TABLE (tenant_id uuid, replaced_at timestamptz)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT c.tenant_id, c.replaced_at FROM customer_chat_codes c
    JOIN tenants t ON t.id = c.tenant_id AND t.status = 'active' AND t.kind = 'business'
   WHERE c.code_hash = p_hash AND (c.replaced_at IS NULL OR c.replaced_at > now() - interval '30 days')
$$;
-- +goose StatementEnd

-- The businesses reaching a chat address, most recent first, with their
-- ids (taskiem_reach_businesses gives the names only).
-- +goose StatementBegin
CREATE FUNCTION taskiem_reach_business_list(p_channel text, p_address_hash bytea)
RETURNS TABLE (tenant_id uuid, business text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name FROM (
    SELECT c.tenant_id, greatest(c.linked_at, c.last_sent_at) AS at FROM reach_channels c
     WHERE c.channel = p_channel AND c.address_hash = p_address_hash AND c.stopped_at IS NULL
    UNION ALL
    SELECT l.tenant_id, greatest(l.created_at, l.last_sent_at) FROM reach_request_links l
     WHERE l.channel = p_channel AND l.address_hash = p_address_hash AND l.stopped_at IS NULL AND l.created_at > now() - interval '90 days'
  ) x JOIN tenants t ON t.id = x.tenant_id AND t.status = 'active'
  GROUP BY t.id, t.name ORDER BY max(x.at) DESC LIMIT 10
$$;
-- +goose StatementEnd

-- Records the business a pending phone code was asked for through (its
-- chat link); the code must be pending.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_phone_code_came_from(p_number text, p_purpose text, p_tenant uuid)
RETURNS boolean LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH u AS (UPDATE phone_codes c SET came_from = p_tenant
              WHERE c.number = p_number AND c.purpose = p_purpose AND c.expires_at > now() RETURNING 1)
  SELECT EXISTS (SELECT 1 FROM u)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
DO $$
DECLARE f text;
BEGIN
  FOREACH f IN ARRAY ARRAY['taskiem_chat_code_find(bytea)', 'taskiem_reach_business_list(text, bytea)',
    'taskiem_auth_phone_code_came_from(text, text, uuid)'] LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO taskiem_dispatch', f);
    EXECUTE format('REVOKE EXECUTE ON FUNCTION %s FROM PUBLIC', f);
    EXECUTE format('GRANT EXECUTE ON FUNCTION %s TO taskiem_app', f);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION taskiem_auth_phone_code_came_from(text, text, uuid);
DROP FUNCTION taskiem_reach_business_list(text, bytea);
DROP FUNCTION taskiem_chat_code_find(bytea);
ALTER TABLE phone_codes DROP COLUMN came_from;
DROP TABLE customer_message_charges;
DROP TABLE customer_chat_notices;
DROP TABLE customer_chat_link_settings;
DROP TABLE customer_chat_codes;
