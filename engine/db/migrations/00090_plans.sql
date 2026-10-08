-- Plans (spec 16, Phase 4 billing; decision 0017). A plan is a flat-priced
-- tier: a price in kobo per month and per year, the limits it gives (keys
-- of tenant_limits), the features it unlocks, partner caps for the
-- embedded tiers, and overage rates for pass-through costs. Operators load
-- the catalogue from a config file (`taskiem billing plans --load`), so
-- prices change without code. A tenant's effective limits are its plan's
-- limits overlaid by its tenant_limits overrides, which stay the operator's
-- override mechanism.
--
-- tenants.plan_id (a uuid placeholder since 00002) is not used: a tenant's
-- plan is its subscription's (00091).
--
-- max_retention_days caps how long a run's history is kept after it ends
-- (spec 16.2: retention is a plan capacity), whatever a workflow asks for.

-- +goose Up
ALTER TABLE tenant_limits ADD COLUMN max_retention_days int CHECK (max_retention_days >= 0);

CREATE TABLE plans (
  id            text PRIMARY KEY CHECK (id ~ '^[a-z][a-z0-9_]{1,39}$'),
  name          text NOT NULL,
  tier          text NOT NULL CHECK (tier IN ('internal', 'starter', 'growth', 'business', 'enterprise')),
  monthly_kobo  bigint NOT NULL CHECK (monthly_kobo >= 0),
  annual_kobo   bigint NOT NULL CHECK (annual_kobo >= 0),
  limits        jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(limits) = 'object'),
  features      jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(features) = 'object'),
  partner       jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(partner) = 'object'),
  overage       jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(overage) = 'object'),
  public        boolean NOT NULL DEFAULT true,  -- offered to tenants self-serve
  active        boolean NOT NULL DEFAULT true,  -- false: retired; subscribers keep it, no one new gets it
  sort          int NOT NULL DEFAULT 0,
  updated_by    text NOT NULL,
  updated_at    timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE plans FORCE ROW LEVEL SECURITY;
-- The catalogue is readable by every tenant; only the definer function
-- below writes it.
CREATE POLICY catalogue ON plans FOR SELECT TO taskiem_app USING (true);
CREATE POLICY dispatch ON plans TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT ON plans TO taskiem_app;
GRANT SELECT, INSERT, UPDATE ON plans TO taskiem_dispatch;

-- Operators load a plan (taskiem billing plans --load): insert or replace.
-- +goose StatementBegin
CREATE FUNCTION taskiem_put_plan(p jsonb, p_by text)
RETURNS void
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  INSERT INTO plans (id, name, tier, monthly_kobo, annual_kobo, limits, features, partner, overage, public, active, sort, updated_by)
  VALUES (p->>'id', p->>'name', p->>'tier', (p->>'monthly_kobo')::bigint, (p->>'annual_kobo')::bigint,
          COALESCE(p->'limits', '{}'), COALESCE(p->'features', '{}'), COALESCE(p->'partner', '{}'), COALESCE(p->'overage', '{}'),
          COALESCE((p->>'public')::boolean, true), COALESCE((p->>'active')::boolean, true), COALESCE((p->>'sort')::int, 0), p_by)
  ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, tier = EXCLUDED.tier, monthly_kobo = EXCLUDED.monthly_kobo,
    annual_kobo = EXCLUDED.annual_kobo, limits = EXCLUDED.limits, features = EXCLUDED.features, partner = EXCLUDED.partner,
    overage = EXCLUDED.overage, public = EXCLUDED.public, active = EXCLUDED.active, sort = EXCLUDED.sort,
    updated_by = EXCLUDED.updated_by, updated_at = now();
END
$$;
-- +goose StatementEnd
REVOKE EXECUTE ON FUNCTION taskiem_put_plan(jsonb, text) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_put_plan(jsonb, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_put_plan(jsonb, text);
DROP TABLE plans;
ALTER TABLE tenant_limits DROP COLUMN max_retention_days;
