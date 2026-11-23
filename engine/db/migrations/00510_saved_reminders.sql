-- Saved objects S2 (docs/spec/saved-objects.md §6.3): "remind me when @car
-- insurance is due". A reminder is on a saved thing's date field: told on
-- that day (in the morning, Lagos time) to the person who asked, in the
-- business they asked in, and again each year after when yearly. It is
-- the owner's, as the saved thing is: a person's own (person_id, read
-- only in their own scope, never once a door names a business; told to
-- their own devices and chats, tied to no business) or a business's
-- (tenant_id; told through that business's chats, tell_tenant, to a
-- member). tell_user is who hears it. The date is read again from the field
-- when it is due, so a changed date moves it; a removed thing takes its
-- reminders with it.
-- +goose Up
CREATE TABLE saved_reminders (
  id          uuid PRIMARY KEY,
  person_id   uuid REFERENCES users(id) ON DELETE CASCADE,
  tenant_id   uuid REFERENCES tenants(id) ON DELETE CASCADE,
  object_id   uuid NOT NULL REFERENCES saved_objects(id) ON DELETE CASCADE,
  field       text NOT NULL CHECK (length(field) BETWEEN 1 AND 40),
  remind_on   date NOT NULL,
  yearly      boolean NOT NULL DEFAULT true,
  tell_tenant uuid REFERENCES tenants(id) ON DELETE CASCADE,
  tell_user   uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  last_sent   date,
  created_at  timestamptz NOT NULL DEFAULT now(),
  CHECK ((person_id IS NULL) <> (tenant_id IS NULL)),
  CHECK ((tenant_id IS NULL) = (tell_tenant IS NULL)),
  UNIQUE (object_id, field, tell_user)
);
CREATE INDEX saved_reminders_due ON saved_reminders (remind_on);
ALTER TABLE saved_reminders ENABLE ROW LEVEL SECURITY;
ALTER TABLE saved_reminders FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON saved_reminders TO taskiem_app
  USING (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL)
  WITH CHECK (person_id IS NOT NULL AND person_id = taskiem_person_scope() AND taskiem_person_business() IS NULL);
CREATE POLICY tenant_isolation ON saved_reminders TO taskiem_app
  USING (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()))
  WITH CHECK (tenant_id IS NOT NULL AND tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON saved_reminders TO taskiem_app;

-- The tick reads which reminders are due, across owners: only their ids,
-- whose they are and who hears them, never the date or the thing.
CREATE POLICY dispatch_due ON saved_reminders FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, person_id, tenant_id, remind_on, last_sent, tell_tenant, tell_user) ON saved_reminders TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_saved_reminders_due(p_today date)
RETURNS TABLE (id uuid, person_id uuid, tenant_id uuid, tell_tenant uuid, tell_user uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT r.id, r.person_id, r.tenant_id, r.tell_tenant, r.tell_user FROM saved_reminders r
   WHERE r.remind_on <= p_today AND (r.last_sent IS NULL OR r.last_sent < r.remind_on)
   ORDER BY r.remind_on
   LIMIT 500
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_saved_reminders_due(date) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_saved_reminders_due(date) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_saved_reminders_due(date) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_saved_reminders_due(date);
DROP TABLE saved_reminders;
