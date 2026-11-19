-- People chatting with people, stage 5b load test ("As built: stage 5b,
-- load test"): a group's member count without row-level security's check
-- on each member's row.
--
-- A Group (each row of the groups list, and the live connection's group
-- event, sent to every member's connections on every message) counted its
-- members with count(*) over group_members, where member_read calls
-- taskiem_group_member for each of up to 256 rows: about 17 ms a group at
-- 256, paid by each of the group's members' connections for each message,
-- the largest share of the database's time under group load.
-- taskiem_group_count checks the person in scope once (as member_read
-- does: they are or were in the group, no business in scope) and counts
-- the members now; anyone else is answered 0, as member_read hid every
-- row from them.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_group_count(p_group uuid) RETURNS integer
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT CASE WHEN taskiem_group_member(p_group)
    THEN (SELECT count(*)::int FROM group_members m WHERE m.group_id = p_group AND m.left_at IS NULL)
    ELSE 0 END
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_group_count(uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_group_count(uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_group_count(uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_group_count(uuid);
