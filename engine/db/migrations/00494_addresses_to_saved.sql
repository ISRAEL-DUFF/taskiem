-- Saved addresses move over to saved things (saved objects S1, part 6;
-- docs/spec/saved-objects.md §12): each person_addresses row becomes one of
-- the person's own place objects, under the same id (so an address_id a
-- client holds still names it), its label the title, a name to call it up
-- by made from the label (@home, @work-2 when taken), and the default kept
-- as default_place. /v1/me/addresses reads and writes them there from now
-- on, unchanged for the app and checkout.
-- +goose Up
ALTER TABLE saved_objects ADD COLUMN default_place boolean NOT NULL DEFAULT false,
  ADD CONSTRAINT saved_objects_default_place CHECK (NOT default_place OR (kind = 'place' AND person_id IS NOT NULL));
CREATE UNIQUE INDEX saved_objects_default_place_one ON saved_objects (person_id) WHERE default_place AND archived_at IS NULL;

-- +goose StatementBegin
DO $$
DECLARE
  a record;
  base text;
  candidate text;
  n int;
BEGIN
  FOR a IN SELECT * FROM person_addresses ORDER BY person_id, created_at, id LOOP
    -- "Mum's place" is mums-place; a label with nothing to keep is place.
    base := trim(both '-' from regexp_replace(lower(replace(replace(a.label, '''', ''), '’', '')), '[^a-z0-9]+', '-', 'g'));
    base := left(base, 28);
    base := trim(both '-' from base);
    IF length(base) < 2 THEN
      base := 'place';
    END IF;
    candidate := base;
    n := 1;
    WHILE EXISTS (SELECT 1 FROM saved_objects WHERE person_id = a.person_id AND name = candidate AND archived_at IS NULL) LOOP
      n := n + 1;
      candidate := base || '-' || n;
    END LOOP;
    INSERT INTO saved_objects (id, person_id, name, title, kind, details, sealed, hint, check_state, default_place,
      created_by, updated_by, created_at, updated_at)
    VALUES (a.id, a.person_id, candidate, a.label, 'place',
      jsonb_strip_nulls(jsonb_build_object('line', a.line, 'area', nullif(a.area, ''), 'landmark', nullif(a.landmark, ''), 'phone', a.phone)),
      '{}', left(CASE WHEN a.area <> '' THEN a.area ELSE a.line END, 120), 'none', a.is_default,
      a.person_id, a.person_id, a.created_at, a.updated_at);
  END LOOP;
END
$$;
-- +goose StatementEnd

DROP TABLE person_addresses;

-- +goose Down
CREATE TABLE person_addresses (
  id          uuid PRIMARY KEY,
  person_id   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  label       text NOT NULL CHECK (length(label) BETWEEN 1 AND 40),
  line        text NOT NULL CHECK (length(line) BETWEEN 1 AND 200),
  area        text NOT NULL DEFAULT '' CHECK (length(area) <= 80),
  landmark    text NOT NULL DEFAULT '' CHECK (length(landmark) <= 200),
  phone       text CHECK (phone IS NULL OR phone ~ '^\+[1-9][0-9]{6,14}$'),
  is_default  boolean NOT NULL DEFAULT false,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX person_addresses_person ON person_addresses (person_id, created_at);
CREATE UNIQUE INDEX person_addresses_default ON person_addresses (person_id) WHERE is_default;
ALTER TABLE person_addresses ENABLE ROW LEVEL SECURITY;
ALTER TABLE person_addresses FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON person_addresses TO taskiem_app
  USING (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
GRANT SELECT, INSERT, UPDATE, DELETE ON person_addresses TO taskiem_app;
INSERT INTO person_addresses (id, person_id, label, line, area, landmark, phone, is_default, created_at, updated_at)
  SELECT id, person_id, left(title, 40), details->>'line', coalesce(details->>'area', ''), coalesce(details->>'landmark', ''), details->>'phone',
    default_place, created_at, updated_at
  FROM saved_objects WHERE kind = 'place' AND person_id IS NOT NULL AND archived_at IS NULL;
-- Moved back, not kept twice: Up moves them over again.
DELETE FROM saved_objects WHERE kind = 'place' AND person_id IS NOT NULL;
DROP INDEX saved_objects_default_place_one;
ALTER TABLE saved_objects DROP CONSTRAINT saved_objects_default_place, DROP COLUMN default_place;
