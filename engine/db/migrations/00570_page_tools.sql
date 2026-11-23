-- Pages G2 (docs/spec/pages.md §5, §7, §12): made tools on a page.
--
-- page_source_cache keeps what a tool that feeds a part answered, for one
-- viewer and the values it was given (block is "tool:<command>": two
-- parts one tool feeds share its run), for a short while (sixty seconds),
-- so a page opened again does not start the tool again; Refresh and an
-- action pressed on the page clear it. It keeps the run, never its
-- outputs: they are read from the run, masked, each time.
--
-- A button pressed on a page is a tool_buttons row like an answer's
-- (made tools M2): from_page names the page and place the row, part and
-- button (a once button is spent by the first run started from it, and
-- held there). Deleting the page deletes its buttons.
-- +goose Up
CREATE TABLE page_source_cache (
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  page_id    uuid NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
  block      text NOT NULL,
  viewer     text NOT NULL,
  inputs     bytea NOT NULL,
  run_id     uuid NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, page_id, block, viewer, inputs)
);
CREATE INDEX page_source_cache_started ON page_source_cache (started_at);
ALTER TABLE page_source_cache ENABLE ROW LEVEL SECURITY;
ALTER TABLE page_source_cache FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON page_source_cache TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON page_source_cache TO taskiem_app;

ALTER TABLE tool_buttons ADD COLUMN from_page uuid REFERENCES pages(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX tool_buttons_page_place ON tool_buttons (tenant_id, from_page, place) WHERE from_page IS NOT NULL;

-- +goose Down
DROP INDEX tool_buttons_page_place;
ALTER TABLE tool_buttons DROP COLUMN from_page;
DROP TABLE page_source_cache;
