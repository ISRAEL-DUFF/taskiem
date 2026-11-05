-- +goose Up
-- Passkeys optional for owners and administrators
-- (docs/spec/optional-passkeys.md): a person may hold themselves to their
-- passkey (users.passkey_only), and a business may hold its
-- administrators to passkeys (tenants.require_admin_passkeys). Both are
-- off by default; the deployment's switch still holds everyone when set.

ALTER TABLE users ADD COLUMN passkey_only boolean NOT NULL DEFAULT false;
ALTER TABLE tenants ADD COLUMN require_admin_passkeys boolean NOT NULL DEFAULT false;
GRANT SELECT (passkey_only) ON users TO taskiem_dispatch;
GRANT SELECT (require_admin_passkeys) ON tenants TO taskiem_dispatch;

-- What holds a person to passkeys in a business, for sign-in and each
-- request: their own setting, the business's, and whether they have one.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_passkey_rule(p_user uuid, p_tenant uuid)
RETURNS TABLE (passkey_only boolean, require_admin_passkeys boolean, has_passkey boolean)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT u.passkey_only,
         COALESCE((SELECT t.require_admin_passkeys FROM tenants t WHERE t.id = p_tenant), false),
         EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = p_user)
    FROM users u WHERE u.id = p_user
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_auth_passkey_rule(uuid, uuid) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_passkey_rule(uuid, uuid) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_passkey_rule(uuid, uuid) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_passkey_rule(uuid, uuid);
REVOKE SELECT (require_admin_passkeys) ON tenants FROM taskiem_dispatch;
REVOKE SELECT (passkey_only) ON users FROM taskiem_dispatch;
ALTER TABLE tenants DROP COLUMN require_admin_passkeys;
ALTER TABLE users DROP COLUMN passkey_only;
