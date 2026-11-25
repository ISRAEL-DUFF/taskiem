-- +goose Up
-- Forms that sell L4 (docs/spec/forms-that-sell.md §6.4, §11): a follow-up
-- of a form left half-filled may go on WhatsApp, by the approved
-- "finish your order" template from the business's own number, once the
-- deployment names that template (TASKIEM_WA_FOLLOWUP_TEMPLATE). Nothing
-- here sends anything by itself: with no template named, every follow-up
-- goes by text or email as in L3.
--
-- The record keeps the WhatsApp message's id, so a send WhatsApp later
-- reports failed (a number not on WhatsApp) is sent again by text, once,
-- with a link of its own. A STOP on WhatsApp says whether the business
-- had followed that contact up, so the chat can answer it. The public
-- host learns whether a storefront's business has its own number (the
-- consent line names WhatsApp only then), and where a follow-up's link
-- lives, so the template's button can open it on the form host.

ALTER TABLE form_left ADD COLUMN wa_id text CHECK (length(wa_id) <= 200);
CREATE INDEX form_left_wa_id ON form_left (tenant_id, wa_id) WHERE wa_id IS NOT NULL;

-- +goose StatementBegin
-- The WhatsApp message a record was followed up by (in the caller's
-- tenant only).
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

-- +goose StatementBegin
-- WhatsApp reported a follow-up failed: the record goes by text instead,
-- once (its channel becomes sms and it forgets the message's id), with a
-- restore link of its own lasting as long as the first. Nothing when the
-- record is unknown, went by text already or the buyer came back.
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

-- A contact stops a business's follow-ups, now saying whether the business
-- followed that contact up in the last 7 days (a STOP on WhatsApp is
-- answered then).
DROP FUNCTION taskiem_form_followup_stop(uuid, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_followup_stop(p_tenant uuid, p_hash bytea)
RETURNS boolean
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  INSERT INTO form_followup_stops (tenant_id, contact_hash) VALUES (p_tenant, p_hash) ON CONFLICT DO NOTHING;
  RETURN EXISTS (SELECT 1 FROM form_left WHERE tenant_id = p_tenant AND contact_hash = p_hash AND followed_up_at > now() - interval '7 days');
END
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The public host: whether a storefront's business has its own WhatsApp
-- number, so an order form's consent line may name WhatsApp. Nothing of
-- the number is returned.
CREATE FUNCTION taskiem_public_form_followup_whatsapp(p_storefront uuid)
RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM storefronts s JOIN whatsapp_numbers n ON n.tenant_id = s.tenant_id AND n.status = 'active' WHERE s.id = p_storefront)
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- The public host: the storefront a follow-up's link belongs to (its
-- address), for the link as the WhatsApp template's button opens it on
-- the form host. Only for a link not past its 7 days; nothing else of it.
CREATE FUNCTION taskiem_public_form_back_where(p_hash bytea)
RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(s.published_slug, s.slug) FROM form_restores r JOIN storefronts s ON s.tenant_id = r.tenant_id AND s.status = 'published'
   WHERE r.token_hash = p_hash AND r.expires_at > now()
   LIMIT 1
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_left_wa_failed(uuid, text, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_form_followup_stop(uuid, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_followup_whatsapp(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_public_form_back_where(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text), taskiem_form_left_wa_failed(uuid, text, bytea), taskiem_form_followup_stop(uuid, bytea),
  taskiem_public_form_followup_whatsapp(uuid), taskiem_public_form_back_where(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text), taskiem_form_left_wa_failed(uuid, text, bytea), taskiem_form_followup_stop(uuid, bytea) TO taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_public_form_followup_whatsapp(uuid), taskiem_public_form_back_where(bytea) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_form_back_where(bytea);
DROP FUNCTION taskiem_public_form_followup_whatsapp(uuid);
DROP FUNCTION taskiem_form_left_wa_failed(uuid, text, bytea);
DROP FUNCTION taskiem_form_left_wa_sent(uuid, uuid, text);
DROP FUNCTION taskiem_form_followup_stop(uuid, bytea);
-- +goose StatementBegin
CREATE FUNCTION taskiem_form_followup_stop(p_tenant uuid, p_hash bytea)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF NOT p_tenant = ANY (taskiem_tenant_scope()) THEN
    RAISE EXCEPTION 'tenant not in scope' USING ERRCODE = '42501';
  END IF;
  INSERT INTO form_followup_stops (tenant_id, contact_hash) VALUES (p_tenant, p_hash) ON CONFLICT DO NOTHING;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_form_followup_stop(uuid, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_form_followup_stop(uuid, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_form_followup_stop(uuid, bytea) TO taskiem_app;
DROP INDEX form_left_wa_id;
ALTER TABLE form_left DROP COLUMN wa_id;
