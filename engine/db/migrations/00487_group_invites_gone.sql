-- People chatting with people, stage 5b review fixes: an invite to a group
-- that goes (taken back by an admin, accepted or declined on another
-- device, the group gone) is told on the person's live connections, so it
-- leaves their Requests at once. "As built: stage 5b, groups (web)" and
-- "(app)", "Review fixes".
--
-- group_invites_gone keeps, for an hour, each invite the person could see
-- that went: written by a definer trigger on group_invites (the admin who
-- takes an invite back may not write the invited person's rows), read by
-- the person's own live connections. No foreign key on the person: a
-- deleted account's invites go with it (the trigger skips those).

-- +goose Up
CREATE TABLE group_invites_gone (
  person_id   uuid NOT NULL,
  group_id    uuid NOT NULL,
  at          timestamptz NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX group_invites_gone_at ON group_invites_gone (person_id, at);

ALTER TABLE group_invites_gone ENABLE ROW LEVEL SECURITY;
ALTER TABLE group_invites_gone FORCE ROW LEVEL SECURITY;
CREATE POLICY person_own ON group_invites_gone TO taskiem_app
  USING (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope())
  WITH CHECK (taskiem_person_business() IS NULL AND person_id = taskiem_person_scope());
GRANT SELECT, DELETE ON group_invites_gone TO taskiem_app;
CREATE POLICY dispatch_all ON group_invites_gone TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT (person_id, at), INSERT, DELETE ON group_invites_gone TO taskiem_dispatch;

-- +goose StatementBegin
CREATE FUNCTION taskiem_group_invite_gone() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
BEGIN
  -- A hidden invite (from someone they blocked) was never seen.
  IF OLD.hidden OR NOT EXISTS (SELECT 1 FROM person_profiles p WHERE p.person_id = OLD.person_id) THEN
    RETURN OLD;
  END IF;
  DELETE FROM group_invites_gone WHERE person_id = OLD.person_id AND at < now() - interval '1 hour';
  INSERT INTO group_invites_gone (person_id, group_id) VALUES (OLD.person_id, OLD.group_id);
  RETURN OLD;
END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_group_invite_gone() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_group_invite_gone() FROM PUBLIC;
CREATE TRIGGER group_invites_gone AFTER DELETE ON group_invites FOR EACH ROW EXECUTE FUNCTION taskiem_group_invite_gone();

-- +goose Down
DROP TRIGGER group_invites_gone ON group_invites;
DROP FUNCTION taskiem_group_invite_gone();
DROP TABLE group_invites_gone;
