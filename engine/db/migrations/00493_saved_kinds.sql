-- Kinds an owner makes (saved objects S1, part 5; docs/spec/saved-objects.md
-- §4.2, §12): "Car", "Generator", "Tenant", each with its fields (an
-- ordered list: key, label, type, required, sensitive, never_share, the
-- options of a choice). One owner, as saved_objects: a person (their own)
-- or a business. Removing one archives it, once nothing live is of it; an
-- object of a made-up kind is of kind custom, its kind_id beside it, its
-- values in details and its sensitive ones sealed.
-- +goose Up
CREATE TABLE saved_kinds (
  id          uuid PRIMARY KEY,
  person_id   uuid REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   uuid REFERENCES tenants(id) ON DELETE CASCADE,
  name        text NOT NULL CHECK (length(name) BETWEEN 1 AND 40),
  fields      jsonb NOT NULL CHECK (jsonb_typeof(fields) = 'array' AND jsonb_array_length(fields) BETWEEN 1 AND 30),
  created_by  uuid,
  updated_by  uuid,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  archived_at timestamptz,
  CHECK ((person_id IS NULL) <> (tenant_id IS NULL))
);
CREATE UNIQUE INDEX saved_kinds_person_name ON saved_kinds (person_id, lower(name)) WHERE person_id IS NOT NULL AND archived_at IS NULL;
CREATE UNIQUE INDEX saved_kinds_tenant_name ON saved_kinds (tenant_id, lower(name)) WHERE tenant_id IS NOT NULL AND archived_at IS NULL;
ALTER TABLE saved_kinds ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_kinds FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON saved_kinds TO taskiem_app
  USING (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
CREATE POLICY tenant_isolation ON saved_kinds TO taskiem_app
  USING (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()))
  WITH CHECK (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()));
-- DELETE: a person's own, when they delete their account.
GRANT SELECT, INSERT, UPDATE, DELETE ON saved_kinds TO taskiem_app;

ALTER TABLE saved_objects ADD COLUMN kind_id uuid REFERENCES saved_kinds(id) ON DELETE CASCADE,
  DROP CONSTRAINT saved_objects_kind_check,
  ADD CONSTRAINT saved_objects_kind_check CHECK (kind IN ('bank_account', 'person', 'place', 'text', 'provider_account', 'custom')),
  ADD CONSTRAINT saved_objects_custom_kind CHECK ((kind = 'custom') = (kind_id IS NOT NULL));
CREATE INDEX saved_objects_kind ON saved_objects (kind_id) WHERE kind_id IS NOT NULL;

-- +goose Down
DELETE FROM saved_objects WHERE kind = 'custom';
ALTER TABLE saved_objects DROP CONSTRAINT saved_objects_custom_kind,
  DROP CONSTRAINT saved_objects_kind_check,
  ADD CONSTRAINT saved_objects_kind_check CHECK (kind IN ('bank_account', 'person', 'place', 'text', 'provider_account')),
  DROP COLUMN kind_id;
DROP TABLE saved_kinds;
