-- +goose Up
-- Forms that sell, a review of L4 (docs/spec/forms-that-sell.md §6.4,
-- §19):
--
-- 1. A WhatsApp follow-up is claimed before it goes: the record is marked
--    followed up on WhatsApp in a "sending" state and committed, with the
--    caps and the per-business lock, before Meta is asked; the send's
--    outcome is written after (sent, or clearly not sent and texted
--    instead, once). An unclear outcome stays a send: the template is
--    never sent twice. A failure WhatsApp reports before the message's id
--    is written is remembered (form_left_wa_early) and applied once it is.
-- 2. WhatsApp's failures by kind: 131050 (the person stopped marketing
--    messages from this business) is a stop and no text; 131049 (Meta's
--    per-person marketing limit) is no text and no retry; any other is
--    texted once, but not to a contact who stopped the business's
--    follow-ups, on a form no longer following up, or for a page whose
--    order was recorded since.
-- 3. The template registry keeps Meta's category for each template in
--    each account, and whether Meta's body and buttons are ours; a
--    follow-up goes on WhatsApp, and a form says so, only where the
--    template is approved in that business's account in the category the
--    deployment names, with our words.

ALTER TABLE form_left ADD COLUMN wa_state text CHECK (wa_state IN ('sending', 'sent', 'texted', 'stopped', 'limited', 'failed'));
UPDATE form_left SET wa_state = 'sent' WHERE channel = 'whatsapp';

-- A failure WhatsApp reported for a message whose id is not written yet
-- (the send's answer and its status crossed): applied when it is.
CREATE TABLE form_left_wa_early (
  tenant_id  uuid NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  wa_id      text NOT NULL CHECK (length(wa_id) <= 200),
  code       int NOT NULL DEFAULT 0,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, wa_id)
);
ALTER TABLE form_left_wa_early ENABLE ROW LEVEL SECURITY;
ALTER TABLE form_left_wa_early FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON form_left_wa_early TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON form_left_wa_early TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON form_left_wa_early FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON form_left_wa_early TO taskiem_dispatch;

-- Meta's category for a template in an account, and whether its body and
-- buttons as Meta holds them are the library's (NULL: not known yet).
ALTER TABLE whatsapp_templates ADD COLUMN meta_category text CHECK (length(meta_category) <= 40), ADD COLUMN meta_matches boolean;
-- The public host's definer reads whether a business's follow-up
-- template is ready in its own account: those columns only.
CREATE POLICY dispatch_read ON whatsapp_templates FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (status, meta_category, meta_matches) ON whatsapp_templates TO taskiem_dispatch;

-- +goose StatementBegin
-- A WhatsApp follow-up claimed: the record (just marked followed up on
-- WhatsApp) is sending.
CREATE FUNCTION taskiem_form_left_wa_sending(p_tenant uuid, p_id uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  UPDATE form_left SET wa_state = 'sending'
   WHERE tenant_id = p_tenant AND id = p_id AND channel = 'whatsapp' AND followed_up_at IS NOT NULL AND wa_state IS NULL;
  RETURN FOUND;
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text);
-- +goose StatementBegin
-- WhatsApp took the follow-up (p_wa_id its message's id), or its answer
-- was unclear (NULL): either way it was sent, never again. Returns the
-- code of a failure WhatsApp reported for it meanwhile, if any.
CREATE FUNCTION taskiem_form_left_wa_sent(p_tenant uuid, p_id uuid, p_wa_id text)
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  c int;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  UPDATE form_left SET wa_state = 'sent', wa_id = NULLIF(p_wa_id, '')
   WHERE tenant_id = p_tenant AND id = p_id AND wa_state = 'sending';
  IF NOT FOUND OR COALESCE(p_wa_id, '') = '' THEN
    RETURN NULL;
  END IF;
  DELETE FROM form_left_wa_early WHERE tenant_id = p_tenant AND wa_id = p_wa_id RETURNING code INTO c;
  RETURN c;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- WhatsApp clearly did not take the follow-up: the record goes by text,
-- with the link already made, once. False when it was settled already.
CREATE FUNCTION taskiem_form_left_wa_unsent(p_tenant uuid, p_id uuid)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  UPDATE form_left SET wa_state = 'texted', channel = 'sms'
   WHERE tenant_id = p_tenant AND id = p_id AND wa_state = 'sending';
  RETURN FOUND;
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- A follow-up claimed whose text the outbox then refused (the person's
-- limits, the plan's texts): not followed up after all, with why; its
-- count, its link and the person's allowance across businesses given
-- back.
CREATE FUNCTION taskiem_form_left_unfollow(p_tenant uuid, p_id uuid, p_outcome text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  SELECT * INTO l FROM form_left WHERE tenant_id = p_tenant AND id = p_id AND followed_up_at IS NOT NULL AND came_back_at IS NULL FOR UPDATE;
  IF l.id IS NULL THEN
    RETURN;
  END IF;
  UPDATE form_left SET followed_up_at = NULL, channel = NULL, wa_state = NULL, wa_id = NULL, outcome = p_outcome, claimed_until = NULL,
         expires_at = last_keep_at + interval '7 days'
   WHERE id = l.id;
  DELETE FROM form_restores WHERE left_id = l.id;
  UPDATE form_left_stats SET followed = followed - 1
   WHERE tenant_id = p_tenant AND scope_id = COALESCE(l.endpoint_id, l.action_id) AND day = (l.followed_up_at AT TIME ZONE 'UTC')::date AND followed > 0;
  DELETE FROM form_followup_contacts WHERE ctid = (SELECT ctid FROM form_followup_contacts WHERE contact_hash = l.contact_hash ORDER BY sent_at DESC LIMIT 1);
END
$$;
-- +goose StatementEnd

DROP FUNCTION taskiem_form_left_wa_failed(uuid, text, bytea);
-- +goose StatementBegin
-- WhatsApp reported a follow-up failed, with its error code. 131050 (the
-- person stopped marketing messages from the business) stops the
-- business's follow-ups to them; 131049 (Meta's limit of marketing
-- messages a person gets) leaves it followed up; neither is texted. Any
-- other failure goes by text, once (the channel becomes sms), with a
-- restore link of its own, unless the contact stopped the business's
-- follow-ups, the form no longer follows up, the buyer came back or the
-- page's order was recorded since. A failure for a message whose id is
-- not written yet, while a follow-up of the business is sending, is
-- remembered for when it is.
CREATE FUNCTION taskiem_form_left_wa_failed(p_tenant uuid, p_wa_id text, p_token_hash bytea, p_code int)
RETURNS TABLE (id uuid, action_id uuid, front_id uuid, site text, contact jsonb, contact_kind text, contact_hash bytea, choice jsonb, last_keep_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  IF p_wa_id IS NULL OR p_wa_id = '' THEN
    RETURN;
  END IF;
  SELECT f.* INTO l FROM form_left f WHERE f.tenant_id = p_tenant AND f.wa_id = p_wa_id AND f.channel = 'whatsapp' AND f.contact_kind = 'phone' FOR UPDATE;
  IF l.id IS NULL THEN
    IF EXISTS (SELECT 1 FROM form_left f WHERE f.tenant_id = p_tenant AND f.wa_state = 'sending') THEN
      INSERT INTO form_left_wa_early (tenant_id, wa_id, code) VALUES (p_tenant, p_wa_id, COALESCE(p_code, 0)) ON CONFLICT DO NOTHING;
    END IF;
    RETURN;
  END IF;
  IF l.wa_state IS DISTINCT FROM 'sent' THEN
    RETURN;
  END IF;
  IF p_code = 131050 THEN
    UPDATE form_left f SET wa_state = 'stopped' WHERE f.id = l.id;
    INSERT INTO form_followup_stops (tenant_id, contact_hash) VALUES (p_tenant, l.contact_hash) ON CONFLICT DO NOTHING;
    RETURN;
  END IF;
  IF p_code = 131049 THEN
    UPDATE form_left f SET wa_state = 'limited' WHERE f.id = l.id;
    RETURN;
  END IF;
  IF l.came_back_at IS NOT NULL OR l.expires_at <= now()
     OR EXISTS (SELECT 1 FROM form_followup_stops s WHERE s.tenant_id = p_tenant AND s.contact_hash = l.contact_hash)
     OR NOT EXISTS (SELECT 1 FROM storefront_actions a WHERE a.id = l.action_id AND a.followup_on)
     OR l.page_hash IS NOT NULL AND (EXISTS (SELECT 1 FROM storefront_orders o WHERE o.tenant_id = p_tenant AND o.status_hash = l.page_hash)
                                     OR EXISTS (SELECT 1 FROM storefront_inbox i WHERE i.tenant_id = p_tenant AND i.status_hash = l.page_hash)) THEN
    UPDATE form_left f SET wa_state = 'failed' WHERE f.id = l.id;
    RETURN;
  END IF;
  UPDATE form_left f SET channel = 'sms', wa_state = 'texted', wa_id = NULL WHERE f.id = l.id;
  INSERT INTO form_restores (token_hash, tenant_id, endpoint_id, action_id, choice, expires_at, left_id)
  VALUES (p_token_hash, p_tenant, l.endpoint_id, l.action_id, l.choice, l.expires_at, l.id);
  RETURN QUERY SELECT l.id, l.action_id, l.front_id, l.site, l.contact, l.contact_kind, l.contact_hash, l.choice, l.last_keep_at;
END
$$;
-- +goose StatementEnd

-- The public host: a follow-up may go on WhatsApp, and the form says so,
-- only where the business's active own number's account holds the
-- deployment's template approved, in the category the deployment names,
-- with our body and buttons.
DROP FUNCTION taskiem_public_form_followup_whatsapp(uuid);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_followup_whatsapp(p_storefront uuid, p_name text, p_language text, p_category text)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM storefronts s
      JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
      JOIN whatsapp_numbers n ON n.tenant_id = s.tenant_id AND n.status = 'active'
      JOIN whatsapp_templates w ON w.waba_id = n.waba_id AND w.tenant_id = n.tenant_id
     WHERE s.id = p_storefront AND w.name = p_name AND w.language = p_language AND w.status = 'approved'
       AND lower(w.meta_category) = lower(p_category) AND w.meta_matches)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Forgets what has lasted its time, as 00900, and failures remembered a
-- day for a message whose id never came.
CREATE OR REPLACE FUNCTION taskiem_form_left_purge()
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
  m int;
BEGIN
  DELETE FROM form_restores WHERE expires_at <= now() AND (used_at IS NULL OR used_at <= now() - interval '24 hours');
  GET DIAGNOSTICS m = ROW_COUNT;
  DELETE FROM form_left l WHERE l.expires_at <= now()
     AND NOT EXISTS (SELECT 1 FROM form_restores r WHERE r.left_id = l.id AND r.used_at > now() - interval '24 hours');
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM form_left_inbox WHERE created_at < now() - interval '1 day';
  DELETE FROM form_followup_contacts WHERE sent_at < now() - interval '8 days';
  DELETE FROM form_left_stats WHERE day < (now() AT TIME ZONE 'UTC')::date - 90;
  DELETE FROM form_left_wa_early WHERE created_at < now() - interval '1 day';
  RETURN n + m;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_wa_sending(uuid, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_wa_unsent(uuid, uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_unfollow(uuid, uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_wa_failed(uuid, text, bytea, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_followup_whatsapp(uuid, text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text), taskiem_form_left_wa_sending(uuid, uuid), taskiem_form_left_wa_unsent(uuid, uuid), taskiem_form_left_unfollow(uuid, uuid, text),
  taskiem_form_left_wa_failed(uuid, text, bytea, int), taskiem_public_form_followup_whatsapp(uuid, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text), taskiem_form_left_wa_sending(uuid, uuid), taskiem_form_left_wa_unsent(uuid, uuid), taskiem_form_left_unfollow(uuid, uuid, text),
  taskiem_form_left_wa_failed(uuid, text, bytea, int) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_public_form_followup_whatsapp(uuid, text, text, text) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_form_followup_whatsapp(uuid, text, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_followup_whatsapp(p_storefront uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM storefronts s JOIN whatsapp_numbers n ON n.tenant_id = s.tenant_id AND n.status = 'active' WHERE s.id = p_storefront)
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_form_left_wa_failed(uuid, text, bytea, int);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_left_wa_failed(p_tenant uuid, p_wa_id text, p_token_hash bytea)
RETURNS TABLE (id uuid, action_id uuid, front_id uuid, site text, contact jsonb, contact_kind text, contact_hash bytea, choice jsonb, last_keep_at timestamptz)
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  l form_left%ROWTYPE;
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  IF p_wa_id IS NULL OR p_wa_id = '' THEN
    RETURN;
  END IF;
  UPDATE form_left f SET channel = 'sms', wa_id = NULL
   WHERE f.tenant_id = p_tenant AND f.wa_id = p_wa_id AND f.channel = 'whatsapp' AND f.contact_kind = 'phone'
     AND f.came_back_at IS NULL AND f.expires_at > now()
  RETURNING f.* INTO l;
  IF l.id IS NULL THEN
    RETURN;
  END IF;
  INSERT INTO form_restores (token_hash, tenant_id, endpoint_id, action_id, choice, expires_at, left_id)
  VALUES (p_token_hash, p_tenant, l.endpoint_id, l.action_id, l.choice, l.expires_at, l.id);
  RETURN QUERY SELECT l.id, l.action_id, l.front_id, l.site, l.contact, l.contact_kind, l.contact_hash, l.choice, l.last_keep_at;
END
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_left_wa_sent(p_tenant uuid, p_id uuid, p_wa_id text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  UPDATE form_left SET wa_id = p_wa_id WHERE tenant_id = p_tenant AND id = p_id AND channel = 'whatsapp';
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_wa_failed(uuid, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_followup_whatsapp(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text), taskiem_form_left_wa_failed(uuid, text, bytea), taskiem_public_form_followup_whatsapp(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text), taskiem_form_left_wa_failed(uuid, text, bytea) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_public_form_followup_whatsapp(uuid) TO taskiem_public;
DROP FUNCTION taskiem_form_left_unfollow(uuid, uuid, text);
DROP FUNCTION taskiem_form_left_wa_unsent(uuid, uuid);
DROP FUNCTION taskiem_form_left_wa_sending(uuid, uuid);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_form_left_purge()
RETURNS int
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  n int;
  m int;
BEGIN
  DELETE FROM form_restores WHERE expires_at <= now() AND (used_at IS NULL OR used_at <= now() - interval '24 hours');
  GET DIAGNOSTICS m = ROW_COUNT;
  DELETE FROM form_left l WHERE l.expires_at <= now()
     AND NOT EXISTS (SELECT 1 FROM form_restores r WHERE r.left_id = l.id AND r.used_at > now() - interval '24 hours');
  GET DIAGNOSTICS n = ROW_COUNT;
  DELETE FROM form_left_inbox WHERE created_at < now() - interval '1 day';
  DELETE FROM form_followup_contacts WHERE sent_at < now() - interval '8 days';
  DELETE FROM form_left_stats WHERE day < (now() AT TIME ZONE 'UTC')::date - 90;
  RETURN n + m;
END
$$;
-- +goose StatementEnd
REVOKE SELECT (status, meta_category, meta_matches) ON whatsapp_templates FROM taskiem_dispatch;
DROP POLICY dispatch_read ON whatsapp_templates;
ALTER TABLE whatsapp_templates DROP COLUMN meta_matches, DROP COLUMN meta_category;
DROP TABLE form_left_wa_early;
ALTER TABLE form_left DROP COLUMN wa_state;
