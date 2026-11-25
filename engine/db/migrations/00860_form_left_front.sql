-- +goose Up
-- Forms that sell, a review of L0: a keep's front is the front of its
-- action, never another of the business's fronts (nor any front for a
-- hosted form, which has none). taskiem_form_left_put checked only that
-- the front was the tenant's, and read the action's and the front's
-- tenant_id, which its owner (taskiem_dispatch) may not read, so a keep
-- for an action was refused: the action's business is now read through
-- its storefront, as the other definers read it.

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_form_left_put(p_tenant uuid, p_endpoint uuid, p_action uuid, p_front uuid, p_site text, p_contact jsonb,
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
     OR p_action IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id
                                              WHERE a.id = p_action AND s.tenant_id = p_tenant)
     OR p_front IS NOT NULL AND NOT EXISTS (SELECT 1 FROM storefront_actions a JOIN storefronts s ON s.id = a.storefront_id
                                             WHERE a.id = p_action AND s.tenant_id = p_tenant AND a.front_id = p_front) THEN
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

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_form_left_put(p_tenant uuid, p_endpoint uuid, p_action uuid, p_front uuid, p_site text, p_contact jsonb,
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
