-- Pages H5 (docs/spec/pages.md §13, §16 Q3; build-plan-gaps §7,
-- g-pages-runs): how many times a day a page is opened, for the estimate
-- of a tool's runs a day on the page's settings ("about 40 runs a day").
--
-- One row a page and day (Lagos), counted as the page is drawn for a
-- viewer (the view, and its short form in a chat), never who opened it.
-- Only the last eight days are kept: older rows are taken away when a
-- page is first opened on a new day.
-- +goose Up
CREATE TABLE page_views (
  tenant_id uuid NOT NULL REFERENCES tenants(id),
  page_id   uuid NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
  day       date NOT NULL,
  views     integer NOT NULL DEFAULT 0 CHECK (views >= 0),
  PRIMARY KEY (tenant_id, page_id, day)
);
ALTER TABLE page_views ENABLE ROW LEVEL SECURITY;
ALTER TABLE page_views FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON page_views TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON page_views TO taskiem_app;

-- +goose Down
DROP TABLE page_views;
