-- Billing routing and notices (spec 16, decision 0017).
--
-- taskiem_tenant_plan: a tenant's plan and subscription status, or for a
-- sub-tenant its partner's (spec 13.1: sub-tenants inherit the partner's
-- plan). No row: no subscription (the internal, unlimited plan).
--
-- taskiem_billing_due / taskiem_billing_pending_payments /
-- taskiem_billing_tenants: the billing job's cross-tenant routing (ids
-- only); the work itself runs in each tenant's scope.
--
-- billing_notices: dunning and lifecycle emails sent, so a retried job
-- never sends the same one twice.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_tenant_plan(p_tenant uuid)
RETURNS TABLE (plan_id text, status text, limits jsonb, features jsonb, partner jsonb, overage jsonb, inherited boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.id, s.status, p.limits, p.features, p.partner, p.overage, t.parent_id IS NOT NULL
    FROM tenants t
    JOIN subscriptions s ON s.tenant_id = COALESCE(t.parent_id, t.id)
    JOIN plans p ON p.id = s.plan_id
   WHERE t.id = p_tenant AND p_tenant = ANY (taskiem_tenant_scope())
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_tenant_plan(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_tenant_plan(uuid) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_billing_due(p_now timestamptz, p_limit int)
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id FROM subscriptions s
   WHERE s.next_action_at IS NOT NULL AND s.next_action_at <= p_now
   ORDER BY s.next_action_at LIMIT p_limit
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_billing_due(timestamptz, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_billing_due(timestamptz, int) TO taskiem_app;

-- +goose StatementBegin
CREATE FUNCTION taskiem_billing_pending_payments(p_older_than timestamptz, p_limit int)
RETURNS TABLE (tenant_id uuid, reference text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT p.tenant_id, p.reference FROM billing_payments p
   WHERE p.status = 'pending' AND p.created_at <= p_older_than
   ORDER BY p.created_at LIMIT p_limit
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_billing_pending_payments(timestamptz, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_billing_pending_payments(timestamptz, int) TO taskiem_app;

-- Every live tenant (top-level and sub-tenants), for daily snapshots and
-- enrolling tenants without a subscription when billing is on.
-- +goose StatementBegin
CREATE FUNCTION taskiem_billing_tenants()
RETURNS TABLE (tenant_id uuid, parent_id uuid, subscribed boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, t.parent_id, EXISTS (SELECT 1 FROM subscriptions s WHERE s.tenant_id = t.id)
    FROM tenants t WHERE t.status <> 'deleted' ORDER BY t.id
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_billing_tenants() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_billing_tenants() TO taskiem_app;

CREATE TABLE billing_notices (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  kind        text NOT NULL,   -- trial_ending, payment_failed, past_due, degraded, recovered, invoice
  ref         text NOT NULL,   -- what it is about (an invoice number, a period)
  sent_to     text[] NOT NULL DEFAULT '{}',
  sent_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, kind, ref)
);
ALTER TABLE billing_notices ENABLE ROW LEVEL SECURITY;
ALTER TABLE billing_notices FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON billing_notices TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON billing_notices TO taskiem_app;

-- +goose Down
DROP TABLE billing_notices;
DROP FUNCTION taskiem_billing_tenants();
DROP FUNCTION taskiem_billing_pending_payments(timestamptz, int);
DROP FUNCTION taskiem_billing_due(timestamptz, int);
DROP FUNCTION taskiem_tenant_plan(uuid);
