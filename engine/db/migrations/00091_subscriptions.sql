-- Subscriptions (spec 16, decision 0017): one per top-level tenant when
-- billing is on (TASKIEM_BILLING=on). Sub-tenants have none: they run on
-- their partner's plan (spec 13.1). A tenant without a subscription is on
-- the internal, unlimited plan of a self-hosted deployment.
--
-- Status: trial -> active -> past_due (unpaid; grace period) ->
-- degraded (new runs refused; approvals, signals and reconciliation of
-- running work continue) -> active again once paid. cancelled at period
-- end (also degraded). comped: granted by an operator (design partners),
-- optionally until a date.
--
-- The tenant's API writes its own row (plan changes, cancellation); the
-- billing job moves it along its periods. Every change is audited.

-- +goose Up
CREATE TABLE subscriptions (
  tenant_id              uuid PRIMARY KEY REFERENCES tenants(id),
  plan_id                text NOT NULL REFERENCES plans(id),
  billing_interval       text NOT NULL DEFAULT 'monthly' CHECK (billing_interval IN ('monthly', 'annual')),
  status                 text NOT NULL CHECK (status IN ('trial', 'active', 'past_due', 'degraded', 'cancelled', 'comped')),
  period_start           timestamptz NOT NULL,
  period_end             timestamptz NOT NULL CHECK (period_end > period_start),
  trial_end              timestamptz,
  comp_until             timestamptz,
  past_due_since         timestamptz,
  cancel_at_period_end   boolean NOT NULL DEFAULT false,
  pending_plan_id        text REFERENCES plans(id),          -- a downgrade taking effect at period end
  pending_interval       text CHECK (pending_interval IN ('monthly', 'annual')),
  credit_kobo            bigint NOT NULL DEFAULT 0 CHECK (credit_kobo >= 0), -- proration credit owed to the tenant
  overage_billed_through date,                               -- last UTC month whose overage is invoiced
  billing_email          text,
  -- A reusable card authorization for renewals (Paystack authorization
  -- code: usable only with the platform's secret key). Never returned by
  -- the API.
  provider               text,
  authorization_code     text,
  card_brand             text,
  card_last4             text,
  card_exp               text,
  dunning_attempts       int NOT NULL DEFAULT 0,
  next_action_at         timestamptz,                        -- when the billing job looks at it next
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  updated_by             text NOT NULL
);
CREATE INDEX subscriptions_next_action ON subscriptions (next_action_at) WHERE next_action_at IS NOT NULL;
ALTER TABLE subscriptions ENABLE ROW LEVEL SECURITY;
ALTER TABLE subscriptions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON subscriptions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
-- Routing (which tenants are due) and plan lookups for sub-tenants, through
-- the definer functions in 00094.
CREATE POLICY dispatch ON subscriptions FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT, INSERT, UPDATE ON subscriptions TO taskiem_app;
GRANT SELECT (tenant_id, plan_id, status, next_action_at) ON subscriptions TO taskiem_dispatch;

-- +goose Down
DROP TABLE subscriptions;
