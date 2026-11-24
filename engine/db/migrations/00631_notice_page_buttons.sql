-- Pages H5 (docs/spec/pages.md §9, "From a notification"; build-plan-gaps
-- §7, g-pages-notif): a Tell me message's button that opens one of the
-- business's pages with its filters set ("Disk alerts" filtered to db-2).
--
-- One row a run, step's button and place, as a tool's button is minted
-- (tool_buttons): the chat's reply button carries its id, never the page
-- or its values. Pressed, the page is opened for whoever pressed it, held
-- to the page's roles then: its short form in the chat and its link, with
-- the filters, for the app and the web. A filter value that was masked
-- (personal) is left out, never kept. It lasts as long as a tool's button.
-- +goose Up
CREATE TABLE notice_page_buttons (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  run_id     uuid NOT NULL,
  place      text NOT NULL,
  page       text NOT NULL CHECK (page ~ '^[a-z][a-z0-9-]{1,23}$'),
  label      text NOT NULL,
  filters    jsonb NOT NULL DEFAULT '{}',
  expires_at timestamptz NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, run_id, place)
);
CREATE INDEX notice_page_buttons_expires ON notice_page_buttons (expires_at);
ALTER TABLE notice_page_buttons ENABLE ROW LEVEL SECURITY;
ALTER TABLE notice_page_buttons FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON notice_page_buttons TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON notice_page_buttons TO taskiem_app;

-- +goose Down
DROP TABLE notice_page_buttons;
