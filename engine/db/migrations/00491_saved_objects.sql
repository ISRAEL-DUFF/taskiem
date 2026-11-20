-- Saved objects, S0: the store for saved accounts
-- (docs/spec/saved-objects.md; docs/spec/build-plan-saved-tools-payments.md
-- S0; decision 0071).
--
-- * saved_objects: a thing a person or a business keeps and calls up by
--   @name. One owner: a person (person_id, their own, on You) or a
--   business (tenant_id, shared with its people by role), never both.
--   The kinds Taskiem knows: the bank account (S0); a person, a place, a
--   text and an account with a provider (S1).
-- * A person's own objects are read and written only in their own scope,
--   and never once a door names a business in the transaction, as
--   person_addresses are (00465). A business's are under tenant
--   isolation; who may change them is the server's (saved.manage).
-- * What is sensitive (the account number) is sealed: under the person's
--   own key for theirs, under the business's subject keys for its own.
--   details hold only what is not; hint is what a list shows, masked
--   ("GTBank ••6789").
-- * The name check: the bank's name for the account, asked through a
--   payments connection (check_state checked), or not yet (unchecked).
-- * Removing one archives it: the name is free again, and what was done
--   with it stays explained. A person's go when they delete their
--   account; a business's with the business.
-- * saved_object_events: a person's own log of their objects (added,
--   changed, shown in full, removed). A business's are in its audit chain.
-- +goose Up
CREATE TABLE saved_objects (
  id           uuid PRIMARY KEY,
  person_id    uuid REFERENCES users(id) ON DELETE CASCADE,
  tenant_id    uuid REFERENCES tenants(id) ON DELETE CASCADE,
  -- @mum, @bola-stores: lowercase letters, digits and "-", 2 to 32.
  name         text NOT NULL CHECK (name ~ '^[a-z0-9][a-z0-9-]{0,30}[a-z0-9]$' AND name !~ '--'),
  title        text NOT NULL CHECK (length(title) BETWEEN 1 AND 80),
  kind         text NOT NULL CHECK (kind IN ('bank_account', 'person', 'place', 'text', 'provider_account')),
  -- What is not sensitive: for a bank account, the bank's name, its code
  -- and the list the code came from, a note.
  details      jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(details) = 'object'),
  -- What is: a business's sealed envelope ({"$pii", "subject", "ct"}), or
  -- a person's value sealed under their key ({"person": base64}).
  sealed       jsonb NOT NULL CHECK (jsonb_typeof(sealed) = 'object'),
  hint         text NOT NULL DEFAULT '' CHECK (length(hint) <= 120),
  -- A bank account's name check; none for the kinds that have none.
  check_state  text NOT NULL DEFAULT 'unchecked' CHECK (check_state IN ('unchecked', 'checked', 'none')),
  CHECK ((kind = 'bank_account') = (check_state <> 'none')),
  -- The bank's name for the account, when checked, and through which
  -- connection ("iswallet/taskiem").
  checked_name text CHECK (checked_name IS NULL OR length(checked_name) <= 200),
  checked_via  text,
  checked_at   timestamptz,
  -- Why the name could not be checked, when it will not be until the
  -- account changes (the connection does not list the bank by that name;
  -- the bank has no such account): shown on the account, which is not
  -- asked about again. NULL when it is checked or only waiting.
  check_problem text CHECK (check_problem IS NULL OR length(check_problem) <= 300),
  created_by   uuid,
  updated_by   uuid,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  archived_at  timestamptz,
  CHECK ((person_id IS NULL) <> (tenant_id IS NULL)),
  CHECK ((check_state = 'checked') = (checked_name IS NOT NULL AND checked_at IS NOT NULL)),
  CHECK (check_problem IS NULL OR check_state = 'unchecked')
);
CREATE UNIQUE INDEX saved_objects_person_name ON saved_objects (person_id, name) WHERE person_id IS NOT NULL AND archived_at IS NULL;
CREATE UNIQUE INDEX saved_objects_tenant_name ON saved_objects (tenant_id, name) WHERE tenant_id IS NOT NULL AND archived_at IS NULL;
CREATE INDEX saved_objects_person ON saved_objects (person_id, created_at) WHERE person_id IS NOT NULL;
CREATE INDEX saved_objects_tenant ON saved_objects (tenant_id, created_at) WHERE tenant_id IS NOT NULL;
ALTER TABLE saved_objects ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_objects FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON saved_objects TO taskiem_app
  USING (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
CREATE POLICY tenant_isolation ON saved_objects TO taskiem_app
  USING (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()))
  WITH CHECK (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()));
-- DELETE: a person's own, when they delete their account.
GRANT SELECT, INSERT, UPDATE, DELETE ON saved_objects TO taskiem_app;

CREATE TABLE saved_object_events (
  id         uuid PRIMARY KEY,
  person_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  object_id  uuid NOT NULL REFERENCES saved_objects(id) ON DELETE CASCADE,
  action     text NOT NULL CHECK (action IN ('added', 'changed', 'revealed', 'checked', 'removed', 'shared')),
  -- What changed, with sensitive values masked; how a change or a reveal
  -- was confirmed (passkey).
  detail     jsonb NOT NULL DEFAULT '{}' CHECK (jsonb_typeof(detail) = 'object'),
  at         timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX saved_object_events_object ON saved_object_events (object_id, at);
CREATE INDEX saved_object_events_person ON saved_object_events (person_id, at);
ALTER TABLE saved_object_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_object_events FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON saved_object_events TO taskiem_app
  USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
-- Never changed; removed only with the person's account.
GRANT SELECT, INSERT, DELETE ON saved_object_events TO taskiem_app;

-- +goose Down
DROP TABLE saved_object_events;
DROP TABLE saved_objects;
