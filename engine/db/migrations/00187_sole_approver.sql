-- A one-person business can approve (no-technical-steps N1, decision N0-1).
-- Four-eyes stays the default: the maker of a run or workflow may not
-- approve it. When the maker is the only active person who can decide the
-- approval, they may, and the decision is marked self-approved.

-- +goose Up
ALTER TABLE approval_decisions ADD COLUMN self_approved boolean NOT NULL DEFAULT false;

-- Whether nobody but p_user can decide an approval for p_role (any member
-- with approval.decide when p_role is NULL): no other active member holds
-- the role or owner, and nobody else has it delegated now. Runs as the
-- caller, inside the tenant's scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_sole_approver(p_role text, p_user uuid)
RETURNS boolean LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT NOT EXISTS (
    SELECT 1 FROM memberships m JOIN users u ON u.id = m.user_id
     WHERE m.user_id <> p_user AND u.status = 'active'
       AND CASE WHEN p_role IS NULL
             THEN m.role IN ('owner', 'approver')
               OR EXISTS (SELECT 1 FROM roles r WHERE r.tenant_id = m.tenant_id AND r.name = m.role AND 'approval.decide' = ANY (r.permissions))
             ELSE m.role IN (p_role, 'owner') END)
  AND NOT EXISTS (
    SELECT 1 FROM delegations g
     WHERE g.to_user <> p_user AND p_role = ANY (g.roles)
       AND g.revoked_at IS NULL AND g.starts_at <= now() AND g.ends_at > now())
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_sole_approver(text, uuid) TO taskiem_app;

-- A step's name in a workflow definition, wherever it is nested, or its id.
-- +goose StatementBegin
CREATE FUNCTION taskiem_step_name(p_def jsonb, p_step text)
RETURNS text LANGUAGE sql IMMUTABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE(NULLIF(jsonb_path_query_first(p_def, 'lax $.**.steps[*] ? (@.id == $id).name', jsonb_build_object('id', p_step)) #>> '{}', ''), p_step)
$$;
-- +goose StatementEnd
GRANT EXECUTE ON FUNCTION taskiem_step_name(jsonb, text) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_step_name(jsonb, text);
DROP FUNCTION taskiem_sole_approver(text, uuid);
ALTER TABLE approval_decisions DROP COLUMN self_approved;
