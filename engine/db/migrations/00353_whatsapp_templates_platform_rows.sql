-- The platform number's template registry (whatsapp_templates rows with no
-- tenant) was writable from any tenant's scope: the one policy admitted
-- tenant_id IS NULL rows for UPDATE and accepted the tenant's own id in
-- WITH CHECK, so an own number registered with the platform's WABA id
-- could take a platform row over (tenant_id set to its own), hiding it
-- from the platform and making every tenant's shared-number templates
-- read as not submitted; it could also insert platform rows of its own
-- (F36). Tenants now read platform rows and write only their own; the
-- platform writes its rows through taskiem_wa_platform_template_record,
-- only outside any tenant scope (the platform pool).
-- +goose Up
DROP POLICY tenant_isolation ON whatsapp_templates;
CREATE POLICY tenant_read ON whatsapp_templates FOR SELECT TO taskiem_app
  USING (tenant_id IS NULL OR tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY tenant_insert ON whatsapp_templates FOR INSERT TO taskiem_app
  WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY tenant_update ON whatsapp_templates FOR UPDATE TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY tenant_delete ON whatsapp_templates FOR DELETE TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope()));
CREATE POLICY dispatch ON whatsapp_templates TO taskiem_dispatch
  USING (tenant_id IS NULL) WITH CHECK (tenant_id IS NULL);
GRANT SELECT (waba_id, name, language, tenant_id, meta_id, submitted_at), INSERT, UPDATE ON whatsapp_templates TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_wa_platform_template_record(p_waba text, p_name text, p_lang text, p_meta_id text, p_status text, p_reason text, p_submitted timestamptz)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  IF cardinality(taskiem_tenant_scope()) <> 0 THEN
    RAISE EXCEPTION 'the platform''s templates are recorded outside any tenant scope' USING ERRCODE = '42501';
  END IF;
  INSERT INTO whatsapp_templates (waba_id, name, language, tenant_id, meta_id, status, reason, submitted_at, checked_at)
  VALUES (p_waba, p_name, p_lang, NULL, p_meta_id, p_status, p_reason, p_submitted, now())
  ON CONFLICT (waba_id, name, language) DO UPDATE SET meta_id = COALESCE(p_meta_id, whatsapp_templates.meta_id),
    status = p_status, reason = p_reason,
    submitted_at = COALESCE(p_submitted, whatsapp_templates.submitted_at), checked_at = now(), updated_at = now();
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_wa_platform_template_record(text, text, text, text, text, text, timestamptz) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_wa_platform_template_record(text, text, text, text, text, text, timestamptz) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_wa_platform_template_record(text, text, text, text, text, text, timestamptz) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_wa_platform_template_record(text, text, text, text, text, text, timestamptz);
REVOKE SELECT (waba_id, name, language, tenant_id, meta_id, submitted_at), INSERT, UPDATE ON whatsapp_templates FROM taskiem_dispatch;
DROP POLICY dispatch ON whatsapp_templates;
DROP POLICY tenant_delete ON whatsapp_templates;
DROP POLICY tenant_update ON whatsapp_templates;
DROP POLICY tenant_insert ON whatsapp_templates;
DROP POLICY tenant_read ON whatsapp_templates;
CREATE POLICY tenant_isolation ON whatsapp_templates TO taskiem_app
  USING (tenant_id IS NULL OR tenant_id = ANY (taskiem_tenant_scope()))
  WITH CHECK (tenant_id IS NULL OR tenant_id = ANY (taskiem_tenant_scope()));
