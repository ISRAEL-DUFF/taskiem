-- Security review of the Phase 4 work (2026-10-08; docs/security/self-review.md).
--
-- R4: a publisher's name is what an operator verified and what installing
-- tenants see beside every version. Once the publisher is verified (or
-- suspended) the application role cannot change it any more; only the
-- operator's definer functions (running as taskiem_dispatch) could. The
-- signing key may still be rotated.
--
-- R6: the dispatch role read every column of usage_snapshots (each
-- tenant's daily usage and effective limits). Nothing running as
-- taskiem_dispatch reads it (the partner aggregate is the schema owner's
-- function), so it keeps the routing columns only.

-- +goose Up
-- +goose StatementBegin
CREATE FUNCTION taskiem_publisher_name_guard() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  IF NEW.name IS DISTINCT FROM OLD.name AND OLD.status <> 'pending' AND current_user <> 'taskiem_dispatch' THEN
    RAISE EXCEPTION 'publisher % is %: its verified name changes only through Taskiem', OLD.slug, OLD.status USING ERRCODE = '42501';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
CREATE TRIGGER connector_publishers_name_guard BEFORE UPDATE ON connector_publishers
  FOR EACH ROW EXECUTE FUNCTION taskiem_publisher_name_guard();

REVOKE SELECT ON usage_snapshots FROM taskiem_dispatch;
GRANT SELECT (tenant_id, day) ON usage_snapshots TO taskiem_dispatch;

-- +goose Down
REVOKE SELECT (tenant_id, day) ON usage_snapshots FROM taskiem_dispatch;
GRANT SELECT ON usage_snapshots TO taskiem_dispatch;
DROP TRIGGER connector_publishers_name_guard ON connector_publishers;
DROP FUNCTION taskiem_publisher_name_guard();
