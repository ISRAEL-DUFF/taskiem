-- Fronts (docs/spec/fronts.md §5 and §9, decision 0059; F1): a storefront
-- has fronts, pages under it, each with blocks (text, photos, a list of a
-- data table) and actions (forms that start workflows). Every storefront
-- gets its first front from what it has: its catalogue and its services as
-- list blocks, and its four built-in actions, so nothing a customer sees
-- changes. An action is found by its id from now on; the built-in four stay
-- findable by (storefront_id, kind) WHERE builtin for as long as anything
-- looks one up that way. A submission names its front and its action, and a
-- storefront trigger the action it starts on.
-- +goose Up
CREATE TABLE storefront_fronts (
  id             uuid PRIMARY KEY,
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  storefront_id  uuid NOT NULL REFERENCES storefronts(id),
  -- The first front's slug is empty: it is the storefront's home page.
  slug           text NOT NULL CHECK (slug = '' OR slug ~ '^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$'),
  kind           text NOT NULL DEFAULT 'custom' CHECK (kind IN ('shop', 'food', 'services', 'hire', 'spaces', 'tickets', 'classes', 'quotes', 'questions', 'callbacks', 'custom')),
  name           text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  tagline        text CHECK (length(tagline) <= 120),
  photo          text,                          -- a storefront_media id
  position       int NOT NULL DEFAULT 0,
  enabled        boolean NOT NULL DEFAULT false,
  options        jsonb NOT NULL DEFAULT '{}',
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now(),
  UNIQUE (storefront_id, slug)
);
ALTER TABLE storefront_fronts ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_fronts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_fronts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_fronts TO taskiem_app;
CREATE POLICY dispatch ON storefront_fronts FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, storefront_id, slug, kind, name, tagline, photo, position, enabled) ON storefront_fronts TO taskiem_dispatch;

-- What a front shows: text, photos, or a list of one of the business's
-- data tables (its mapping and the columns shown).
CREATE TABLE storefront_blocks (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  front_id   uuid NOT NULL REFERENCES storefront_fronts(id) ON DELETE CASCADE,
  position   int NOT NULL DEFAULT 0,
  kind       text NOT NULL CHECK (kind IN ('text', 'photos', 'list')),
  config     jsonb NOT NULL DEFAULT '{}',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX storefront_blocks_front ON storefront_blocks (front_id, position);
ALTER TABLE storefront_blocks ENABLE ROW LEVEL SECURITY;
ALTER TABLE storefront_blocks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON storefront_blocks TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON storefront_blocks TO taskiem_app;
CREATE POLICY dispatch ON storefront_blocks FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, front_id, position, kind, config) ON storefront_blocks TO taskiem_dispatch;

-- Every storefront's first front, with its catalogue and services as list
-- blocks.
INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, position, enabled)
  SELECT gen_random_uuid(), s.tenant_id, s.id, '', 'shop', COALESCE(NULLIF(left(s.draft->>'name', 80), ''), s.slug), 0, true FROM storefronts s;
INSERT INTO storefront_blocks (id, tenant_id, front_id, position, kind, config)
  SELECT gen_random_uuid(), f.tenant_id, f.id, 0, 'list', jsonb_build_object('role', 'catalogue', 'table', s.catalogue_table, 'columns', s.catalogue_columns)
    FROM storefront_fronts f JOIN storefronts s ON s.id = f.storefront_id WHERE f.slug = '' AND s.catalogue_table IS NOT NULL;
INSERT INTO storefront_blocks (id, tenant_id, front_id, position, kind, config)
  SELECT gen_random_uuid(), f.tenant_id, f.id, 1, 'list', jsonb_build_object('role', 'services', 'table', s.services_table, 'columns', s.services_columns)
    FROM storefront_fronts f JOIN storefronts s ON s.id = f.storefront_id WHERE f.slug = '' AND s.services_table IS NOT NULL;

-- Actions by id. The four built-in kinds stay one each, on the first
-- front, findable by kind.
ALTER TABLE storefront_actions ADD COLUMN id uuid NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE storefront_actions ADD COLUMN front_id uuid REFERENCES storefront_fronts(id);
ALTER TABLE storefront_actions ADD COLUMN slug text CHECK (slug ~ '^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$');
ALTER TABLE storefront_actions ADD COLUMN position int NOT NULL DEFAULT 0;
ALTER TABLE storefront_actions ADD COLUMN builtin boolean NOT NULL DEFAULT true;
-- The action's own questions (its form's schema with x-form hints), when
-- it asks more than its kind's (§5.3).
ALTER TABLE storefront_actions ADD COLUMN form jsonb;
UPDATE storefront_actions a SET front_id = f.id, slug = a.kind,
    position = array_position(ARRAY['order', 'booking', 'enquiry', 'callback'], a.kind)
  FROM storefront_fronts f WHERE f.storefront_id = a.storefront_id AND f.slug = '';
ALTER TABLE storefront_actions ALTER COLUMN front_id SET NOT NULL;
ALTER TABLE storefront_actions ALTER COLUMN slug SET NOT NULL;
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_pkey;
ALTER TABLE storefront_actions ADD PRIMARY KEY (id);
CREATE UNIQUE INDEX storefront_actions_builtin ON storefront_actions (storefront_id, kind) WHERE builtin;
ALTER TABLE storefront_actions ADD CONSTRAINT storefront_actions_front_slug UNIQUE (front_id, slug);
GRANT SELECT (id, front_id, slug, position, builtin, form) ON storefront_actions TO taskiem_dispatch;

-- A submission names its front and its action. A kind with submissions and
-- no action row (none should exist) gets one, off, so every row is filled.
INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment, front_id, slug, position)
  SELECT DISTINCT o.storefront_id, o.kind, o.tenant_id, false, initcap(o.kind), CASE WHEN o.kind IN ('order', 'booking') THEN 'after_accept' ELSE 'none' END,
         f.id, o.kind, array_position(ARRAY['order', 'booking', 'enquiry', 'callback'], o.kind)
    FROM storefront_orders o JOIN storefront_fronts f ON f.storefront_id = o.storefront_id AND f.slug = ''
   WHERE NOT EXISTS (SELECT 1 FROM storefront_actions a WHERE a.storefront_id = o.storefront_id AND a.kind = o.kind AND a.builtin);
ALTER TABLE storefront_orders ADD COLUMN front_id uuid REFERENCES storefront_fronts(id);
ALTER TABLE storefront_orders ADD COLUMN action_id uuid REFERENCES storefront_actions(id);
UPDATE storefront_orders o SET action_id = a.id, front_id = a.front_id
  FROM storefront_actions a WHERE a.storefront_id = o.storefront_id AND a.kind = o.kind AND a.builtin;
ALTER TABLE storefront_orders ALTER COLUMN front_id SET NOT NULL;
ALTER TABLE storefront_orders ALTER COLUMN action_id SET NOT NULL;
CREATE INDEX storefront_orders_action ON storefront_orders (action_id, created_at DESC);
GRANT SELECT (front_id, action_id) ON storefront_orders TO taskiem_dispatch;

-- A submission on its way names its action too (set by the public host
-- from F1c; empty for one sent by an older public host, which names a kind).
ALTER TABLE storefront_inbox ADD COLUMN action_id uuid;

-- A storefront trigger names the action it starts on: by id, or by the
-- built-in kind (storefront_action), resolved to that action's id.
ALTER TABLE triggers ADD COLUMN storefront_action_id uuid;
UPDATE triggers t SET storefront_action_id = a.id
  FROM storefront_actions a WHERE t.type = 'storefront' AND a.tenant_id = t.tenant_id AND a.kind = t.storefront_action AND a.builtin;

-- The catalogue copy names the table it copies, so a table shown by two
-- lists is copied once (§5.2).
ALTER TABLE storefront_catalogue ADD COLUMN table_name text NOT NULL DEFAULT '';
UPDATE storefront_catalogue c SET table_name = d.name FROM storefronts s JOIN data_tables d ON d.id = s.catalogue_table WHERE s.id = c.storefront_id;
ALTER TABLE storefront_catalogue DROP CONSTRAINT storefront_catalogue_pkey;
ALTER TABLE storefront_catalogue ADD PRIMARY KEY (storefront_id, table_name, row_id);
GRANT SELECT (table_name) ON storefront_catalogue TO taskiem_dispatch;

-- What an older binary, or a hand-made row, leaves out is filled in: a
-- built-in action goes on the storefront's first front (made if missing),
-- its slug its kind; a submission names the built-in action of its kind
-- (made, off, if missing) and that action's front. So a deploy's old
-- binary keeps writing while the new one rolls out.
-- +goose StatementBegin
CREATE FUNCTION taskiem_storefront_first_front(p_storefront uuid) RETURNS uuid
LANGUAGE plpgsql AS $$
DECLARE
  f uuid;
BEGIN
  SELECT id INTO f FROM storefront_fronts WHERE storefront_id = p_storefront AND slug = '';
  IF f IS NULL THEN
    INSERT INTO storefront_fronts (id, tenant_id, storefront_id, slug, kind, name, position, enabled)
      SELECT gen_random_uuid(), s.tenant_id, s.id, '', 'shop', COALESCE(NULLIF(left(s.draft->>'name', 80), ''), s.slug), 0, true FROM storefronts s WHERE s.id = p_storefront
      ON CONFLICT (storefront_id, slug) DO NOTHING;
    SELECT id INTO f FROM storefront_fronts WHERE storefront_id = p_storefront AND slug = '';
  END IF;
  RETURN f;
END
$$;
CREATE FUNCTION taskiem_storefront_actions_fill() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.front_id IS NULL THEN
    NEW.front_id := taskiem_storefront_first_front(NEW.storefront_id);
  END IF;
  IF NEW.slug IS NULL THEN
    NEW.slug := NEW.kind;
  END IF;
  IF NEW.position = 0 AND NEW.builtin THEN
    NEW.position := COALESCE(array_position(ARRAY['order', 'booking', 'enquiry', 'callback'], NEW.kind), 0);
  END IF;
  RETURN NEW;
END
$$;
CREATE FUNCTION taskiem_storefront_orders_fill() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
  a uuid;
  f uuid;
BEGIN
  IF NEW.action_id IS NULL THEN
    SELECT id, front_id INTO a, f FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = NEW.kind AND builtin;
    IF a IS NULL THEN
      INSERT INTO storefront_actions (storefront_id, kind, tenant_id, enabled, label, payment)
        VALUES (NEW.storefront_id, NEW.kind, NEW.tenant_id, false, initcap(NEW.kind), CASE WHEN NEW.kind IN ('order', 'booking') THEN 'after_accept' ELSE 'none' END)
        ON CONFLICT (storefront_id, kind) WHERE builtin DO NOTHING;
      SELECT id, front_id INTO a, f FROM storefront_actions WHERE storefront_id = NEW.storefront_id AND kind = NEW.kind AND builtin;
    END IF;
    NEW.action_id := a;
    NEW.front_id := COALESCE(NEW.front_id, f);
  ELSIF NEW.front_id IS NULL THEN
    SELECT front_id INTO NEW.front_id FROM storefront_actions WHERE id = NEW.action_id;
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER storefront_actions_fill BEFORE INSERT ON storefront_actions FOR EACH ROW EXECUTE FUNCTION taskiem_storefront_actions_fill();
CREATE TRIGGER storefront_orders_fill BEFORE INSERT ON storefront_orders FOR EACH ROW EXECUTE FUNCTION taskiem_storefront_orders_fill();

-- +goose Down
DROP TRIGGER storefront_orders_fill ON storefront_orders;
DROP TRIGGER storefront_actions_fill ON storefront_actions;
DROP FUNCTION taskiem_storefront_orders_fill();
DROP FUNCTION taskiem_storefront_actions_fill();
DROP FUNCTION taskiem_storefront_first_front(uuid);
REVOKE SELECT (table_name) ON storefront_catalogue FROM taskiem_dispatch;
ALTER TABLE storefront_catalogue DROP CONSTRAINT storefront_catalogue_pkey;
-- One table per storefront before fronts: anything else copied goes.
DELETE FROM storefront_catalogue c USING storefronts s LEFT JOIN data_tables d ON d.id = s.catalogue_table
 WHERE s.id = c.storefront_id AND c.table_name <> COALESCE(d.name, c.table_name);
ALTER TABLE storefront_catalogue ADD PRIMARY KEY (storefront_id, row_id);
ALTER TABLE storefront_catalogue DROP COLUMN table_name;
ALTER TABLE triggers DROP COLUMN storefront_action_id;
ALTER TABLE storefront_inbox DROP COLUMN action_id;
REVOKE SELECT (front_id, action_id) ON storefront_orders FROM taskiem_dispatch;
DROP INDEX storefront_orders_action;
ALTER TABLE storefront_orders DROP COLUMN action_id;
ALTER TABLE storefront_orders DROP COLUMN front_id;
REVOKE SELECT (id, front_id, slug, position, builtin, form) ON storefront_actions FROM taskiem_dispatch;
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_front_slug;
DROP INDEX storefront_actions_builtin;
ALTER TABLE storefront_actions DROP CONSTRAINT storefront_actions_pkey;
-- Refused while a storefront has a second action of a kind.
ALTER TABLE storefront_actions ADD PRIMARY KEY (storefront_id, kind);
ALTER TABLE storefront_actions DROP COLUMN form;
ALTER TABLE storefront_actions DROP COLUMN builtin;
ALTER TABLE storefront_actions DROP COLUMN position;
ALTER TABLE storefront_actions DROP COLUMN slug;
ALTER TABLE storefront_actions DROP COLUMN front_id;
ALTER TABLE storefront_actions DROP COLUMN id;
DROP TABLE storefront_blocks;
DROP TABLE storefront_fronts;
