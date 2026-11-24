-- Saved files (saved objects S1; docs/spec/saved-objects.md §4.1, §4.2): a
-- file object (a photo or a document, sent as itself in a chat) and a
-- made-up kind's file fields. A file is uploaded first, checked as a chat's
-- file is (scanned, a photo's metadata cleaned), then claimed by the saved
-- thing saved with it (object_id, and field for a made-up kind's field;
-- '' for a file object's own). One owner, as saved_objects: a person's kept
-- here (data), a business's in its own storage (path). One never claimed
-- goes after a day, when the owner next uploads.
-- +goose Up
ALTER TABLE saved_objects DROP CONSTRAINT saved_objects_kind_check,
  ADD CONSTRAINT saved_objects_kind_check CHECK (kind IN ('bank_account', 'person', 'place', 'text', 'provider_account', 'file', 'custom'));

CREATE TABLE saved_files (
  id         uuid PRIMARY KEY,
  person_id  uuid REFERENCES users(id) ON DELETE CASCADE,
  tenant_id  uuid REFERENCES tenants(id) ON DELETE CASCADE,
  object_id  uuid REFERENCES saved_objects(id) ON DELETE CASCADE,
  field      text NOT NULL DEFAULT '' CHECK (length(field) <= 40),
  type       text NOT NULL CHECK (length(type) BETWEEN 1 AND 100),
  size       bigint NOT NULL CHECK (size > 0),
  name       text NOT NULL DEFAULT '' CHECK (length(name) <= 200),
  -- A person's file; a business's is in its storage, at path.
  data       bytea,
  path       text,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((person_id IS NULL) <> (tenant_id IS NULL)),
  CHECK ((person_id IS NOT NULL) = (data IS NOT NULL)),
  CHECK ((tenant_id IS NOT NULL) = (path IS NOT NULL))
);
CREATE UNIQUE INDEX saved_files_field ON saved_files (object_id, field) WHERE object_id IS NOT NULL;
CREATE INDEX saved_files_person_waiting ON saved_files (person_id, created_at) WHERE person_id IS NOT NULL AND object_id IS NULL;
CREATE INDEX saved_files_tenant_waiting ON saved_files (tenant_id, created_at) WHERE tenant_id IS NOT NULL AND object_id IS NULL;
ALTER TABLE saved_files ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_files FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON saved_files TO taskiem_app
  USING (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
CREATE POLICY tenant_isolation ON saved_files TO taskiem_app
  USING (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()))
  WITH CHECK (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON saved_files TO taskiem_app;

-- +goose Down
DROP TABLE saved_files;
DELETE FROM saved_objects WHERE kind = 'file';
ALTER TABLE saved_objects DROP CONSTRAINT saved_objects_kind_check,
  ADD CONSTRAINT saved_objects_kind_check CHECK (kind IN ('bank_account', 'person', 'place', 'text', 'provider_account', 'custom'));
