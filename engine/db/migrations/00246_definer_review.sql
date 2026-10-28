-- The SECURITY DEFINER review (spec 5.3, engine findings 3), sign-in,
-- operator and partner functions first; docs/security/definer-review.md
-- records each finding and what was done.
--
-- 1. Operator accounts are made from the CLI only (decision 0027): the five
--    account functions move from taskiem_app, which the API server holds, to
--    taskiem_ops_admin, which only the CLI's own login is a member of
--    (TASKIEM_OPS_DATABASE_URL, docs/operator-console.md).
-- 2. Definers run as taskiem_dispatch: thirteen were owned by whoever ran
--    the migrations, so they ran with that role's privileges (every table
--    it owns, and every grant and policy ignored on a superuser) rather
--    than the columns dispatch is given. Nine move to taskiem_dispatch with
--    the columns they read. The two that create and drop run_events partitions need to own
--    the table, and two read columns dispatch is kept from: they stay.
-- 3. Partners: a sub-tenant no longer sees its siblings' run counts; it gets
--    the partner-wide totals its quota check needs. taskiem_partner_self and
--    taskiem_storefront_lock read nothing the caller cannot, and stop being
--    definers.
-- 4. Sign-in: the password hash leaves only for sign-in and its own flows;
--    the overloads 00130 kept for replicas mid-upgrade go; a reset link is
--    checked before the person's other links are cleared.
-- 5. Operator console: one review and one report by id, not the newest 200;
--    a tenant's id finds that tenant first; agents' referees stay in the
--    database.

-- +goose Up
-- +goose StatementBegin
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'taskiem_ops_admin') THEN
    CREATE ROLE taskiem_ops_admin NOLOGIN NOBYPASSRLS;
  END IF;
END
$$;
-- +goose StatementEnd
GRANT USAGE ON SCHEMA public TO taskiem_ops_admin;
GRANT EXECUTE ON FUNCTION taskiem_tenant_scope() TO taskiem_ops_admin;
GRANT EXECUTE ON FUNCTION taskiem_audit_append(uuid, text, text, text, text, jsonb) TO taskiem_ops_admin;
REVOKE EXECUTE ON FUNCTION taskiem_ops_operator_add(text, text, text) FROM taskiem_app;
REVOKE EXECUTE ON FUNCTION taskiem_ops_operator_disable(text, text) FROM taskiem_app;
REVOKE EXECUTE ON FUNCTION taskiem_ops_operator_reset(text) FROM taskiem_app;
REVOKE EXECUTE ON FUNCTION taskiem_ops_operators() FROM taskiem_app;
REVOKE EXECUTE ON FUNCTION taskiem_ops_enrol_issue(text, bytea, interval, text) FROM taskiem_app;
GRANT EXECUTE ON FUNCTION taskiem_ops_operator_add(text, text, text) TO taskiem_ops_admin;
GRANT EXECUTE ON FUNCTION taskiem_ops_operator_disable(text, text) TO taskiem_ops_admin;
GRANT EXECUTE ON FUNCTION taskiem_ops_operator_reset(text) TO taskiem_ops_admin;
GRANT EXECUTE ON FUNCTION taskiem_ops_operators() TO taskiem_ops_admin;
GRANT EXECUTE ON FUNCTION taskiem_ops_enrol_issue(text, bytea, interval, text) TO taskiem_ops_admin;

-- 2. Ownership. What each function reads or writes, granted to dispatch.
-- taskiem_partner_usage_snapshots and taskiem_remote_tenants read columns the
-- dispatch role is kept from (usage figures, remote ids: the security review
-- of 2026-10-08), so they keep their owner; their bodies are fixed and gated.
-- FOR SHARE needs UPDATE on a column; dispatch changes no incident.
GRANT SELECT (id, kind), UPDATE (kind), INSERT (id, kind, title, components, scheduled_start, scheduled_end, created_by)
  ON status_incidents TO taskiem_dispatch;
GRANT SELECT (id, incident_id, status), INSERT (incident_id, status, impact, message, actor) ON status_updates TO taskiem_dispatch;
GRANT SELECT (at), INSERT (ok, accept_ms, complete_ms, error), DELETE ON status_probes TO taskiem_dispatch;
CREATE POLICY dispatch ON status_incidents TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON status_updates TO taskiem_dispatch USING (true) WITH CHECK (true);
CREATE POLICY dispatch ON status_probes TO taskiem_dispatch USING (true) WITH CHECK (true);
ALTER FUNCTION taskiem_put_plan(jsonb, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_next_invoice_number(text, timestamptz) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_tenant_plan(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_billing_due(timestamptz, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_billing_pending_payments(timestamptz, int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_billing_tenants() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_status_update(uuid, text, text, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_status_open(text, text, text[], timestamptz, timestamptz, text, text, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_status_record_probe(boolean, int, int, text) OWNER TO taskiem_dispatch;

-- 3. Partners. Per-sub-tenant usage is the partner's alone.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_partner_usage(p_partner uuid)
RETURNS TABLE (tenant_id uuid, runs_today bigint, runs_month bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id,
         COALESCE(sum(u.runs_started) FILTER (WHERE u.day = (now() AT TIME ZONE 'UTC')::date), 0)::bigint,
         COALESCE(sum(u.runs_started), 0)::bigint
    FROM tenants t
    LEFT JOIN tenant_usage u ON u.tenant_id = t.id AND u.day >= date_trunc('month', now() AT TIME ZONE 'UTC')::date
   WHERE t.parent_id = p_partner AND p_partner = ANY (taskiem_tenant_scope())
   GROUP BY t.id
$$;
-- +goose StatementEnd

-- The runs every sub-tenant of a partner started today and this month, for
-- the partner-wide quota a sub-tenant's start is checked against: the
-- totals only, to the partner or one of its sub-tenants.
-- +goose StatementBegin
CREATE FUNCTION taskiem_partner_run_totals(p_partner uuid)
RETURNS TABLE (runs_today bigint, runs_month bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT COALESCE(sum(u.runs_started) FILTER (WHERE u.day = (now() AT TIME ZONE 'UTC')::date), 0)::bigint,
         COALESCE(sum(u.runs_started), 0)::bigint
    FROM tenants t
    JOIN tenant_usage u ON u.tenant_id = t.id AND u.day >= date_trunc('month', now() AT TIME ZONE 'UTC')::date
   WHERE t.parent_id = p_partner
     AND (p_partner = ANY (taskiem_tenant_scope())
          OR EXISTS (SELECT 1 FROM tenants s WHERE s.id = ANY (taskiem_tenant_scope()) AND s.parent_id = p_partner))
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_partner_run_totals(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_partner_run_totals(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_partner_run_totals(uuid) TO taskiem_app;

-- The partner in scope reads its own rows; inside the partner definers it
-- runs as dispatch, which reads them too.
ALTER FUNCTION taskiem_partner_self() SECURITY INVOKER;
-- An advisory lock reads no table.
ALTER FUNCTION taskiem_storefront_lock(uuid) SECURITY INVOKER;

-- 4. Sign-in. Who has an email, without the password hash.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_user_of(p_email text)
RETURNS TABLE (user_id uuid, status text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.id, u.status FROM users u WHERE lower(u.email) = lower(p_email)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_user_of(text) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_user_of(text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_user_of(text) TO taskiem_app;

DROP FUNCTION taskiem_auth_challenge_issue(bytea, text, uuid, interval);
DROP FUNCTION taskiem_auth_challenge_take(bytea, text);

-- The link is spent first; the person's other links go only if it was.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_auth_password_reset_use(p_selector bytea, p_hash bytea)
RETURNS uuid
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH used AS (
    UPDATE password_resets pr SET used_at = now()
     WHERE pr.selector = p_selector AND pr.secret_hash = p_hash AND pr.used_at IS NULL AND pr.expires_at > now() AND pr.failures < 5
    RETURNING pr.user_id
  ), others AS (
    DELETE FROM password_resets o USING used
     WHERE o.user_id = used.user_id AND o.selector <> p_selector AND o.used_at IS NULL
  )
  SELECT user_id FROM used
$$;
-- +goose StatementEnd

-- 5. Operator console.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_agents()
RETURNS TABLE (id uuid, code text, name text, phone text, region text, status text, checks jsonb, registered_by text, created_at timestamptz,
               businesses bigint, published bigint, verified bigint, live_sessions bigint, reports bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT a.id, a.code, a.name, a.phone, a.region, a.status, a.checks - 'referee_name' - 'referee_phone', a.registered_by, a.created_at,
         (SELECT count(*) FROM tenants t WHERE t.agent_id = a.id),
         (SELECT count(*) FROM tenants t JOIN storefronts f ON f.tenant_id = t.id AND f.published_version > 0 WHERE t.agent_id = a.id),
         (SELECT count(DISTINCT v.tenant_id) FROM tenant_verifications v JOIN tenants t ON t.id = v.tenant_id
           WHERE t.agent_id = a.id AND v.level = 'agent_onboarded'),
         (SELECT count(*) FROM agent_sessions s WHERE s.agent_id = a.id AND s.status = 'active' AND s.expires_at > now()),
         (SELECT count(*) FROM page_reports p JOIN tenants t ON t.id = p.tenant_id WHERE t.agent_id = a.id AND p.status <> 'dismissed')
    FROM agents a ORDER BY a.created_at DESC
$$;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_ops_tenants(p_query text, p_limit int)
RETURNS TABLE (id uuid, name text, parent_id uuid, status text, created_at timestamptz, plan_id text, subscription_status text, pool text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.name, t.parent_id, t.status, t.created_at, s.plan_id, s.status, taskiem_tenant_worker_pool(t.id)
    FROM tenants t LEFT JOIN subscriptions s ON s.tenant_id = t.id
   WHERE COALESCE(p_query, '') = '' OR t.id::text = lower(p_query) OR t.name ILIKE '%' || p_query || '%'
   ORDER BY t.id::text = lower(COALESCE(p_query, '')) DESC, t.created_at DESC, t.id
   LIMIT LEAST(GREATEST(COALESCE(p_limit, 100), 1), 500)
$$;
-- +goose StatementEnd

-- One review, as the list gives it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_storefront_review(p_id uuid)
RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, storefront_id uuid, slug text, page_status text, kind text, version int, status text,
               findings jsonb, detail jsonb, created_by text, created_at timestamptz, decided_by text, decision_note text, decided_at timestamptz,
               verified boolean, agent_code text, open_reports bigint)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.id, r.tenant_id, t.name, r.storefront_id, s.slug, s.status, r.kind, r.version, r.status, r.findings, r.detail, r.created_by, r.created_at,
         r.decided_by, r.decision_note, r.decided_at,
         EXISTS (SELECT 1 FROM tenant_verifications v WHERE v.tenant_id = r.tenant_id AND v.level IN ('business', 'agent_onboarded')),
         (SELECT a.code FROM agents a WHERE a.id = t.agent_id),
         (SELECT count(*) FROM page_reports p WHERE p.site_id = r.storefront_id AND p.status = 'open')
    FROM storefront_reviews r JOIN tenants t ON t.id = r.tenant_id JOIN storefronts s ON s.id = r.storefront_id
   WHERE r.id = p_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_storefront_review(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_storefront_review(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_storefront_review(uuid) TO taskiem_app;

-- One page report, as the list gives it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_ops_page_report(p_id uuid)
RETURNS TABLE (id uuid, tenant_id uuid, tenant_name text, kind text, site_id uuid, slug text, reason text, message text, has_contact boolean,
               status text, created_at timestamptz, handled_by text, handled_at timestamptz, reports_on_site bigint, agent_code text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.id, p.tenant_id, t.name, p.kind, p.site_id, (SELECT s.slug FROM storefronts s WHERE s.id = p.site_id), p.reason, p.message,
         p.contact IS NOT NULL, p.status, p.created_at, p.handled_by, p.handled_at,
         (SELECT count(DISTINCT q.ip_hash) FROM page_reports q WHERE q.site_id = p.site_id AND q.status = 'open'),
         (SELECT a.code FROM agents a WHERE a.id = t.agent_id)
    FROM page_reports p JOIN tenants t ON t.id = p.tenant_id
   WHERE p.id = p_id
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_ops_page_report(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_ops_page_report(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_ops_page_report(uuid) TO taskiem_app;

-- +goose Down
SELECT 'down migration not supported; restore from backup';
