-- +goose Up
-- Forms that sell L4, on the founder's direction of 25 November 2026
-- (docs/spec/forms-that-sell.md §19): follow-ups of a form left
-- half-filled go by text, email or WhatsApp, and WhatsApp is no longer
-- switched on by the deployment's environment. A business's follow-ups go
-- on WhatsApp where its own number is connected, Meta holds the built-in
-- "finish your order" template (taskiem_finish_order) approved in its
-- account with our words, and the owner has not turned WhatsApp off for
-- them; the category Meta gave it there decides the consent (utility: the
-- line names WhatsApp; marketing: the buyer ticks a box; anything else:
-- no WhatsApp).
--
-- - storefronts.followup_whatsapp: the owner's choice, "Also follow up on
--   WhatsApp from my number", for every order form of the business; on
--   unless turned off, and of use only once the template is approved.
-- - taskiem_public_form_followup_whatsapp(storefront, language) now takes
--   no template name or category from the deployment: it answers Meta's
--   category for taskiem_finish_order in the business's active own
--   number's account, where it is approved with our words and the owner's
--   choice is on, and NULL otherwise. The public host needs nothing of the
--   platform's number for it.

ALTER TABLE storefronts ADD COLUMN followup_whatsapp boolean NOT NULL DEFAULT true;
GRANT SELECT (followup_whatsapp) ON storefronts TO taskiem_dispatch;

DROP FUNCTION taskiem_public_form_followup_whatsapp(uuid, text, text, text);
-- +goose StatementBegin
CREATE FUNCTION taskiem_public_form_followup_whatsapp(p_storefront uuid, p_language text)
RETURNS text
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT lower(w.meta_category) FROM storefronts s
      JOIN tenants t ON t.id = s.tenant_id AND t.status = 'active'
      JOIN whatsapp_numbers n ON n.tenant_id = s.tenant_id AND n.status = 'active'
      JOIN whatsapp_templates w ON w.waba_id = n.waba_id AND w.tenant_id = n.tenant_id
     WHERE s.id = p_storefront AND s.followup_whatsapp AND w.name = 'taskiem_finish_order' AND w.language = p_language
       AND w.status = 'approved' AND w.meta_matches AND lower(w.meta_category) IN ('utility', 'marketing')
     LIMIT 1
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_public_form_followup_whatsapp(uuid, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_followup_whatsapp(uuid, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_followup_whatsapp(uuid, text) TO taskiem_public;

-- +goose Down
DROP FUNCTION taskiem_public_form_followup_whatsapp(uuid, text);
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
ALTER FUNCTION taskiem_public_form_followup_whatsapp(uuid, text, text, text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_public_form_followup_whatsapp(uuid, text, text, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_public_form_followup_whatsapp(uuid, text, text, text) TO taskiem_public;
REVOKE SELECT (followup_whatsapp) ON storefronts FROM taskiem_dispatch;
ALTER TABLE storefronts DROP COLUMN followup_whatsapp;
