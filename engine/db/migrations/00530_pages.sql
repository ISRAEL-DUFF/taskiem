-- Pages G0 and G1 (decision 0072, docs/spec/pages.md §12): a screen a
-- business makes for itself, a page/v1 document. pages holds each page's
-- name (unique in the business), what its list shows (title, icon), who
-- may open it (the published version's roles), whether it is pinned to
-- the navigation and where, the published version, and the one draft
-- being changed. page_versions keeps every published version, so an
-- earlier one can be brought back. Both under forced row-level security.
-- max_pages is the plan's limit (10 on the lowest plan, 50 on the middle,
-- none above; drafts count).
-- +goose Up
CREATE TABLE pages (
  id                uuid PRIMARY KEY,
  tenant_id         uuid NOT NULL REFERENCES tenants(id),
  name              text NOT NULL CHECK (name ~ '^[a-z][a-z0-9-]{1,23}$'),
  title             text NOT NULL,
  icon              text NOT NULL DEFAULT '',
  roles             text[] NOT NULL DEFAULT '{}',
  pinned            boolean NOT NULL DEFAULT false,
  pin_order         int NOT NULL DEFAULT 0,
  published_version int,
  published_at      timestamptz,
  draft             jsonb,
  draft_by          text,
  draft_at          timestamptz,
  created_by        text NOT NULL,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, name),
  CHECK (draft IS NOT NULL OR published_version IS NOT NULL)
);
ALTER TABLE pages ENABLE ROW LEVEL SECURITY;
ALTER TABLE pages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON pages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON pages TO taskiem_app;

CREATE TABLE page_versions (
  page_id      uuid NOT NULL REFERENCES pages(id) ON DELETE CASCADE,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  version      int NOT NULL CHECK (version >= 1),
  document     jsonb NOT NULL,
  published_by text NOT NULL,
  published_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (page_id, version)
);
ALTER TABLE page_versions ENABLE ROW LEVEL SECURITY;
ALTER TABLE page_versions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON page_versions TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON page_versions TO taskiem_app;

ALTER TABLE tenant_limits ADD COLUMN max_pages int CHECK (max_pages >= 0);

-- +goose Down
ALTER TABLE tenant_limits DROP COLUMN max_pages;
DROP TABLE page_versions;
DROP TABLE pages;
