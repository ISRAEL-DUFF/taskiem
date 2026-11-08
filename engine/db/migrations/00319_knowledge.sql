-- What a business's assistant knows (decision 0062 D12, plan V5b): each
-- knowledge source (the About my business page, the storefront, a data
-- table, the business's own verified website, a file, a Google Sheet, its
-- books' items) is read as text, split into passages and searched per
-- question, so a reply sends only the passages that answer it.
--
-- A source is used only once the owner has seen what it gave and approved
-- it (approved_at); until then, and while a refresh's text waits for a
-- first approval, its passages are pending. A refresh replaces a source's
-- passages whole; a failed one keeps the last text. Deleting a source
-- deletes its passages.

-- +goose Up
CREATE TABLE knowledge_sources (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  kind         text NOT NULL CHECK (kind IN ('about', 'storefront', 'table', 'website', 'file', 'sheet', 'books')),
  title        text NOT NULL CHECK (length(title) BETWEEN 1 AND 120),
  config       jsonb NOT NULL DEFAULT '{}',
  status       text NOT NULL DEFAULT 'new' CHECK (status IN ('new', 'review', 'active', 'failed', 'paused')),
  approved_at  timestamptz,
  approved_by  text,
  read_at      timestamptz,               -- the last read that succeeded
  changed_at   timestamptz,               -- the last time the text changed
  digest       bytea,                     -- of the text read last, to tell a change
  error        text,                      -- the last failure, in plain words
  failed_at    timestamptz,
  refresh_at   timestamptz,               -- a read asked for now (a workflow's refresh, the owner's button)
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX knowledge_sources_tenant ON knowledge_sources (tenant_id, created_at);
ALTER TABLE knowledge_sources ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_sources FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON knowledge_sources TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON knowledge_sources TO taskiem_app;

CREATE TABLE knowledge_passages (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  source_id  uuid NOT NULL REFERENCES knowledge_sources(id) ON DELETE CASCADE,
  pending    boolean NOT NULL,            -- waiting for the owner's first approval
  position   int NOT NULL,
  title      text NOT NULL DEFAULT '',
  body       text NOT NULL,
  link       text NOT NULL DEFAULT '',
  read_at    timestamptz NOT NULL,
  words      tsvector GENERATED ALWAYS AS (to_tsvector('simple', title || ' ' || body)) STORED
);
CREATE INDEX knowledge_passages_source ON knowledge_passages (tenant_id, source_id, pending, position);
CREATE INDEX knowledge_passages_words ON knowledge_passages USING gin (words);
ALTER TABLE knowledge_passages ENABLE ROW LEVEL SECURITY;
ALTER TABLE knowledge_passages FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON knowledge_passages TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON knowledge_passages TO taskiem_app;
GRANT UPDATE (pending) ON knowledge_passages TO taskiem_app;

-- The daily refresh, across tenants: the sources due (read over a day
-- ago, never paused, of a kind that changes outside Taskiem's page), ids
-- only.
CREATE POLICY refresh_read ON knowledge_sources FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, kind, status, read_at, failed_at, refresh_at) ON knowledge_sources TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_knowledge_due(p_limit int) RETURNS TABLE (tenant_id uuid, source_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT s.tenant_id, s.id FROM knowledge_sources s
   WHERE s.status <> 'paused'
     AND (s.refresh_at <= now()
          OR (s.kind IN ('storefront', 'table', 'website', 'sheet', 'books') AND s.status IN ('review', 'active', 'failed')
              AND COALESCE(GREATEST(s.read_at, s.failed_at), '-infinity') < now() - interval '1 day'))
   ORDER BY s.refresh_at NULLS LAST, COALESCE(GREATEST(s.read_at, s.failed_at), '-infinity'), s.id
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_knowledge_due(int) OWNER TO taskiem_dispatch;
REVOKE ALL ON FUNCTION taskiem_knowledge_due(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_knowledge_due(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_knowledge_due(int);
DROP TABLE knowledge_passages;
DROP TABLE knowledge_sources;
