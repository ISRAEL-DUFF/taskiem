-- People in sub-tenants (decision 0030; docs/embedding.md#13-people-in-a-sub-tenant).
--
-- A partner invites a person into one of its sub-tenants: an ordinary
-- invitation (member_invitations, source 'partner'), which the person
-- accepts like any other. Someone with no account yet gets a single-use
-- link by email, which lets them create a passkey and accept in one step.
-- The link goes only to the email address: the partner never sees it.

-- +goose Up
ALTER TABLE member_invitations DROP CONSTRAINT member_invitations_source_check;
ALTER TABLE member_invitations ADD CONSTRAINT member_invitations_source_check CHECK (source IN ('manual', 'scim', 'partner'));

-- A link carries a selector, to find the row, and a 256-bit secret, kept
-- only as its SHA-256. It works once, for seven days, and not after five
-- wrong secrets for its selector, and only while the account it is for
-- is unclaimed (no password, no passkey, no membership) and the invitation
-- still stands. Rows are reached only through the functions below.
CREATE TABLE invitation_links (
  selector     bytea PRIMARY KEY CHECK (length(selector) = 16),
  secret_hash  bytea NOT NULL CHECK (length(secret_hash) = 32),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  user_id      uuid NOT NULL REFERENCES users(id),
  created_at   timestamptz NOT NULL DEFAULT now(),
  expires_at   timestamptz NOT NULL,
  used_at      timestamptz,
  failures     int NOT NULL DEFAULT 0
);
CREATE INDEX ON invitation_links (user_id);
ALTER TABLE invitation_links ENABLE ROW LEVEL SECURITY;
ALTER TABLE invitation_links FORCE ROW LEVEL SECURITY;
CREATE POLICY no_direct_access ON invitation_links TO taskiem_app USING (false) WITH CHECK (false);
CREATE POLICY dispatch ON invitation_links TO taskiem_dispatch USING (true) WITH CHECK (true);
REVOKE ALL ON invitation_links FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE (used_at, failures), DELETE ON invitation_links TO taskiem_dispatch;

-- Whether nobody can sign in as a person yet: no password, no passkey,
-- and no membership anywhere (so no single sign-on either).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_unclaimed(p_user uuid) RETURNS boolean
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT EXISTS (SELECT 1 FROM users u WHERE u.id = p_user AND u.status = 'active' AND u.password_hash IS NULL)
     AND NOT EXISTS (SELECT 1 FROM webauthn_credentials c WHERE c.user_id = p_user)
     AND NOT EXISTS (SELECT 1 FROM memberships m WHERE m.user_id = p_user)
$$;
-- +goose StatementEnd

-- Issues a link for the invitation of p_user by the one tenant in scope,
-- replacing that invitation's unused links. False, and no link, when the
-- account is already claimed: its owner signs in and accepts as usual.
-- +goose StatementBegin
CREATE FUNCTION taskiem_invitation_link_issue(p_user uuid, p_selector bytea, p_hash bytea, p_ttl interval)
RETURNS boolean
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  v_scope uuid[] := taskiem_tenant_scope();
BEGIN
  IF cardinality(v_scope) <> 1 OR NOT EXISTS (
      SELECT 1 FROM member_invitations i WHERE i.tenant_id = v_scope[1] AND i.user_id = p_user) THEN
    RAISE EXCEPTION 'no such invitation' USING ERRCODE = 'P0002';
  END IF;
  DELETE FROM invitation_links WHERE expires_at < now() - interval '1 day';
  DELETE FROM invitation_links WHERE tenant_id = v_scope[1] AND user_id = p_user AND used_at IS NULL;
  IF NOT taskiem_auth_unclaimed(p_user) THEN
    RETURN false;
  END IF;
  INSERT INTO invitation_links (selector, secret_hash, tenant_id, user_id, expires_at) VALUES (p_selector, p_hash, v_scope[1], p_user, now() + p_ttl);
  RETURN true;
END
$$;
-- +goose StatementEnd

-- Checks a link without using it: ok (with the person, their email and
-- the tenant), invalid (unknown, used, expired, locked, the invitation
-- withdrawn, the tenant not active, or the account claimed since) or
-- wrong (a wrong secret, which counts toward the lock).
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_invitation_link_check(p_selector bytea, p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid, email text, outcome text)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r invitation_links;
BEGIN
  SELECT * INTO r FROM invitation_links l WHERE l.selector = p_selector FOR UPDATE;
  IF NOT FOUND OR r.used_at IS NOT NULL OR r.expires_at <= now() OR r.failures >= 5 THEN
    RETURN QUERY SELECT NULL::uuid, NULL::uuid, NULL::text, 'invalid'::text;
    RETURN;
  END IF;
  -- Digests of a 256-bit secret: comparing them in variable time tells
  -- nothing about the secret.
  IF r.secret_hash <> p_hash THEN
    UPDATE invitation_links l SET failures = l.failures + 1 WHERE l.selector = p_selector;
    RETURN QUERY SELECT NULL::uuid, NULL::uuid, NULL::text, 'wrong'::text;
    RETURN;
  END IF;
  IF NOT EXISTS (SELECT 1 FROM member_invitations i WHERE i.tenant_id = r.tenant_id AND i.user_id = r.user_id)
     OR NOT EXISTS (SELECT 1 FROM tenants t WHERE t.id = r.tenant_id AND t.status = 'active')
     OR NOT taskiem_auth_unclaimed(r.user_id) THEN
    RETURN QUERY SELECT NULL::uuid, NULL::uuid, NULL::text, 'invalid'::text;
    RETURN;
  END IF;
  RETURN QUERY SELECT r.user_id, r.tenant_id, (SELECT u.email FROM users u WHERE u.id = r.user_id), 'ok'::text;
END
$$;
-- +goose StatementEnd

-- Uses a link, at most once, in the transaction (scoped to its tenant)
-- that accepts the invitation. The person's other unused links go with it.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_invitation_link_use(p_selector bytea, p_hash bytea)
RETURNS TABLE (user_id uuid, tenant_id uuid)
LANGUAGE plpgsql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
  r invitation_links;
BEGIN
  SELECT * INTO r FROM invitation_links l WHERE l.selector = p_selector AND l.secret_hash = p_hash AND l.used_at IS NULL
     AND l.expires_at > now() AND l.failures < 5 FOR UPDATE;
  IF NOT FOUND OR NOT (r.tenant_id = ANY (taskiem_tenant_scope())) OR NOT taskiem_auth_unclaimed(r.user_id)
     OR NOT EXISTS (SELECT 1 FROM member_invitations i WHERE i.tenant_id = r.tenant_id AND i.user_id = r.user_id) THEN
    RETURN;
  END IF;
  UPDATE invitation_links l SET used_at = now() WHERE l.selector = p_selector;
  DELETE FROM invitation_links l WHERE l.user_id = r.user_id AND l.used_at IS NULL;
  RETURN QUERY SELECT r.user_id, r.tenant_id;
END
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_unclaimed(uuid) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_invitation_link_issue(uuid, bytea, bytea, interval) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_invitation_link_check(bytea, bytea) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_invitation_link_use(bytea, bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_unclaimed(uuid), taskiem_invitation_link_issue(uuid, bytea, bytea, interval),
  taskiem_auth_invitation_link_check(bytea, bytea), taskiem_auth_invitation_link_use(bytea, bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_invitation_link_issue(uuid, bytea, bytea, interval),
  taskiem_auth_invitation_link_check(bytea, bytea), taskiem_auth_invitation_link_use(bytea, bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_invitation_link_use(bytea, bytea);
DROP FUNCTION taskiem_auth_invitation_link_check(bytea, bytea);
DROP FUNCTION taskiem_invitation_link_issue(uuid, bytea, bytea, interval);
DROP FUNCTION taskiem_auth_unclaimed(uuid);
DROP TABLE invitation_links;
DELETE FROM member_invitations WHERE source = 'partner';
ALTER TABLE member_invitations DROP CONSTRAINT member_invitations_source_check;
ALTER TABLE member_invitations ADD CONSTRAINT member_invitations_source_check CHECK (source IN ('manual', 'scim'));
