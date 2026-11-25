-- +goose Up
-- Forms that sell L0 (docs/spec/forms-that-sell.md §6, §10): a form left
-- half-filled. When a buyer types a contact into a form that follows up
-- and does not send it, the public host sends a keep: the contact and the
-- product choice, sealed with the public key for the api role, through an
-- inbox of its own as submissions are. The api role opens it, seals the
-- contact under the person in the vault, keys it, and keeps one row per
-- form and contact in form_left. Nothing else the buyer typed is kept.
--
-- form_left is read and written by the api role only through the narrow
-- functions below (forced row-level security, no direct access); the
-- public host writes its keeps to the inbox and reads nothing of it. A
-- follow-up's restore link is a row of form_restores, written by the api
-- role and claimed once by the public host by the token's hash, getting
-- back only the product choice; it never holds the contact.

-- What the public host writes: one keep, sealed.
CREATE TABLE form_left_inbox (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  endpoint_id    uuid REFERENCES form_endpoints(id) ON DELETE CASCADE,
  action_id      uuid REFERENCES storefront_actions(id) ON DELETE CASCADE,
  payload        bytea NOT NULL CHECK (length(payload) <= 4096),
  created_at     timestamptz NOT NULL DEFAULT now(),
  claimed_until  timestamptz,
  CHECK ((endpoint_id IS NULL) <> (action_id IS NULL))
);
CREATE INDEX form_left_inbox_waiting ON form_left_inbox (created_at);

-- One form left half-filled: per form (or action) and contact.
CREATE TABLE form_left (
  id              uuid PRIMARY KEY,
  tenant_id       uuid NOT NULL REFERENCES tenants(id),
  endpoint_id     uuid REFERENCES form_endpoints(id) ON DELETE CASCADE,
  action_id       uuid REFERENCES storefront_actions(id) ON DELETE CASCADE,
  front_id        uuid REFERENCES storefront_fronts(id) ON DELETE SET NULL,
  site            text CHECK (length(site) <= 300),
  contact         jsonb NOT NULL,                                     -- sealed under the person (vault)
  contact_hash    bytea NOT NULL CHECK (length(contact_hash) = 32),   -- keyed, as message's limits are
  contact_kind    text NOT NULL CHECK (contact_kind IN ('phone', 'email')),
  choice          jsonb NOT NULL DEFAULT '{}' CHECK (length(choice::text) <= 4000),
  last_keep_at    timestamptz NOT NULL,
  followed_up_at  timestamptz,
  came_back_at    timestamptz,
  expires_at      timestamptz NOT NULL,
  created_at      timestamptz NOT NULL DEFAULT now(),
  CHECK ((endpoint_id IS NULL) <> (action_id IS NULL))
);
CREATE UNIQUE INDEX form_left_one ON form_left (tenant_id, (COALESCE(endpoint_id, action_id)), contact_hash);
CREATE INDEX form_left_expires ON form_left (expires_at);

-- A follow-up's restore link: the token's hash, the form and the choice,
-- one-time, never the contact.
CREATE TABLE form_restores (
  token_hash   bytea PRIMARY KEY CHECK (length(token_hash) = 32),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  endpoint_id  uuid REFERENCES form_endpoints(id) ON DELETE CASCADE,
  action_id    uuid REFERENCES storefront_actions(id) ON DELETE CASCADE,
  choice       jsonb NOT NULL DEFAULT '{}' CHECK (length(choice::text) <= 4000),
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now(),
  CHECK ((endpoint_id IS NULL) <> (action_id IS NULL))
);
CREATE INDEX form_restores_expires ON form_restores (expires_at);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['form_left_inbox', 'form_left', 'form_restores'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY no_direct_access ON %I TO taskiem_app USING (false) WITH CHECK (false)', t);
    EXECUTE format('CREATE POLICY dispatch ON %I TO taskiem_dispatch USING (true) WITH CHECK (true)', t);
    EXECUTE format('REVOKE ALL ON %I FROM PUBLIC', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_dispatch', t);
  END LOOP;
END $$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The public host's keep: for a live form or an action that is on, its
-- tenant read here, never taken from the caller. A business with more
-- than 2,000 keeps waiting takes no more until the api role catches up.
CREATE FUNCTION taskiem_public_form_keep(p_endpoint uuid, p_action uuid, p_payload bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  tenant uuid;
BEGIN
  IF (p_endpoint IS NULL) = (p_action IS NULL) THEN
    RETURN false;
  END IF;
  IF p_endpoint IS NOT NULL THEN
    SELECT e.tenant_id INTO tenant FROM form_endpoints e JOIN tenants t ON t.id = e.tenant_id AND t.status = 'active'
     WHERE e.id = p_endpoint AND e.status = 'live';
  ELSE
    SELECT a.tenant_id INTO tenant FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id AND s.status = 'published'
      JOIN tenants t ON t.id = a.tenant_id AND t.status = 'active'
     WHERE a.id = p_action AND a.enabled;
  END IF;
  IF tenant IS NULL THEN
    RETURN false;
  END IF;
  IF (SELECT count(*) FROM (SELECT 1 FROM form_left_inbox WHERE tenant_id = tenant LIMIT 2001) x) > 2000 THEN
    RETURN false;
  END IF;
  INSERT INTO form_left_inbox (id, tenant_id, endpoint_id, action_id, payload) VALUES (gen_random_uuid(), tenant, p_endpoint, p_action, p_payload);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The api role's side of the inbox.
CREATE FUNCTION taskiem_form_left_inbox_claim(p_limit int)
RETURNS TABLE (id uuid, tenant_id uuid, endpoint_id uuid, action_id uuid, payload bytea, created_at timestamptz)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_left_inbox i SET claimed_until = now() + interval '5 minutes'
   WHERE i.id IN (SELECT x.id FROM form_left_inbox x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING i.id, i.tenant_id, i.endpoint_id, i.action_id, i.payload, i.created_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_left_inbox_done(p_id uuid)
RETURNS void
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM form_left_inbox WHERE id = p_id
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Keeps a form left half-filled, one row per form and contact, a later
-- keep replacing the contact, the choice and the site. It lasts 7 days
-- after its last keep, or after its follow-up once one is sent. Only in
-- a tenant in scope, and only for that tenant's own form.
CREATE FUNCTION taskiem_form_left_put(p_tenant uuid, p_endpoint uuid, p_action uuid, p_front uuid, p_site text, p_contact jsonb,
  p_hash bytea, p_kind text, p_choice jsonb, p_at timestamptz)
RETURNS uuid
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  got uuid;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  IF p_endpoint IS NOT NULL AND NOT EXISTS (SELECT 1 FROM form_endpoints WHERE id = p_endpoint AND tenant_id = p_tenant)
     OR p_action IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions WHERE id = p_action AND tenant_id = p_tenant)
     OR p_front IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_fronts WHERE id = p_front AND tenant_id = p_tenant) THEN
    RAISE EXCEPTION 'not this business''s form' USING ERRCODE = '42501';
  END IF;
  INSERT INTO form_left (id, tenant_id, endpoint_id, action_id, front_id, site, contact, contact_hash, contact_kind, choice, last_keep_at, expires_at)
  VALUES (gen_random_uuid(), p_tenant, p_endpoint, p_action, p_front, p_site, p_contact, p_hash, p_kind, COALESCE(p_choice, '{}'), p_at, p_at + interval '7 days')
  ON CONFLICT (tenant_id, (COALESCE(endpoint_id, action_id)), contact_hash) DO UPDATE
    SET front_id = EXCLUDED.front_id, site = COALESCE(EXCLUDED.site, form_left.site), contact = EXCLUDED.contact, contact_kind = EXCLUDED.contact_kind,
        choice = EXCLUDED.choice, last_keep_at = GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at),
        expires_at = CASE WHEN form_left.followed_up_at IS NULL THEN GREATEST(form_left.last_keep_at, EXCLUDED.last_keep_at) + interval '7 days'
                          ELSE form_left.expires_at END
  RETURNING id INTO got;
  RETURN got;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A submission from the same contact on the same form: within the wait
-- (no follow-up yet) the record goes; after the follow-up it is marked
-- came back and stays until purged. Answers deleted, came_back or ''.
CREATE FUNCTION taskiem_form_left_sent(p_tenant uuid, p_scope uuid, p_hash bytea)
RETURNS text
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  DELETE FROM form_left WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND contact_hash = p_hash AND followed_up_at IS NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    RETURN 'deleted';
  END IF;
  UPDATE form_left SET came_back_at = COALESCE(came_back_at, now())
   WHERE tenant_id = p_tenant AND COALESCE(endpoint_id, action_id) = p_scope AND contact_hash = p_hash AND followed_up_at IS NOT NULL;
  GET DIAGNOSTICS n = ROW_COUNT;
  IF n > 0 THEN
    RETURN 'came_back';
  END IF;
  RETURN '';
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A follow-up sent: the record now lasts 7 days from it, as its restore
-- link does, and the link's row is written with the choice only.
CREATE FUNCTION taskiem_form_left_followed(p_tenant uuid, p_id uuid, p_token_hash bytea, p_at timestamptz)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  UPDATE form_left SET followed_up_at = p_at, expires_at = p_at + interval '7 days'
   WHERE tenant_id = p_tenant AND id = p_id AND followed_up_at IS NULL
  RETURNING * INTO l;
  IF l.id IS NULL THEN
    RETURN false;
  END IF;
  INSERT INTO form_restores (token_hash, tenant_id, endpoint_id, action_id, choice, expires_at)
  VALUES (p_token_hash, p_tenant, l.endpoint_id, l.action_id, l.choice, p_at + interval '7 days');
  RETURN true;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The public host's restore: once, within its 7 days, the form and the
-- choice only.
CREATE FUNCTION taskiem_public_form_restore(p_hash bytea)
RETURNS jsonb
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE form_restores r SET used_at = now()
   WHERE r.token_hash = p_hash AND r.used_at IS NULL AND r.expires_at > now()
  RETURNING jsonb_build_object('endpoint_id', r.endpoint_id, 'action_id', r.action_id, 'choice', r.choice)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Forgets what has lasted its time: records past their 7 days, restore
-- links past theirs, and keeps no api role took in a day.
CREATE FUNCTION taskiem_form_left_purge()
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
  m int;
BEGIN
  DELETE FROM form_left WHERE expires_at <= now();
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM form_restores WHERE expires_at <= now();
  GET DIAGNOSTICS m = ROW_COUNT;
  DELETE FROM form_left_inbox WHERE created_at < now() - interval '1 day';
  RETURN n + m;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_public_form_keep(uuid, uuid, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_restore(bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_inbox_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_inbox_done(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_sent(uuid, uuid, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_followed(uuid, uuid, bytea, timestamptz) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_purge() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_keep(uuid, uuid, bytea), taskiem_public_form_restore(bytea), taskiem_form_left_inbox_claim(int),
  taskiem_form_left_inbox_done(uuid), taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz),
  taskiem_form_left_sent(uuid, uuid, bytea), taskiem_form_left_followed(uuid, uuid, bytea, timestamptz), taskiem_form_left_purge() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_keep(uuid, uuid, bytea), taskiem_public_form_restore(bytea) TO taskiem_public;
GRANT EXECUTE ON FUNCTION taskiem_form_left_inbox_claim(int), taskiem_form_left_inbox_done(uuid),
  taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz), taskiem_form_left_sent(uuid, uuid, bytea),
  taskiem_form_left_followed(uuid, uuid, bytea, timestamptz), taskiem_form_left_purge() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_form_left_purge();
DROP FUNCTION taskiem_public_form_restore(bytea);
DROP FUNCTION taskiem_form_left_followed(uuid, uuid, bytea, timestamptz);
DROP FUNCTION taskiem_form_left_sent(uuid, uuid, bytea);
DROP FUNCTION taskiem_form_left_put(uuid, uuid, uuid, uuid, text, jsonb, bytea, text, jsonb, timestamptz);
DROP FUNCTION taskiem_form_left_inbox_done(uuid);
DROP FUNCTION taskiem_form_left_inbox_claim(int);
DROP FUNCTION taskiem_public_form_keep(uuid, uuid, bytea);
DROP TABLE form_restores;
DROP TABLE form_left;
DROP TABLE form_left_inbox;
