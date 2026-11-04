-- Business areas (docs/spec/business-areas.md): each business turns on only
-- what it uses. One row per business and area, for the five areas that are
-- not the Schools area (whose switch stays in school_settings, 00211).
--
-- Every business that exists now keeps everything on: this migration writes
-- an "on" row for each of them, sub-tenants included. A business without a
-- row follows the default in engine/areas (on for now; the signup question
-- of phase 4 turns it off for new businesses, after writing the rows that
-- keep the businesses made until then on).
--
-- The backfill runs before row-level security is turned on for the table,
-- so it does not depend on which role runs migrations.
-- +goose Up
CREATE TABLE business_areas (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  area        text NOT NULL CHECK (area IN ('storefront', 'orders', 'stock', 'money', 'links')),
  enabled     boolean NOT NULL,
  updated_by  text NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, area)
);
INSERT INTO business_areas (tenant_id, area, enabled, updated_by)
  SELECT t.id, a.area, true, 'migration:00309'
    FROM tenants t CROSS JOIN (VALUES ('storefront'), ('orders'), ('stock'), ('money'), ('links')) AS a(area);
ALTER TABLE business_areas ENABLE ROW LEVEL SECURITY;
ALTER TABLE business_areas FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON business_areas TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON business_areas TO taskiem_app;

-- +goose Down
DROP TABLE business_areas;
