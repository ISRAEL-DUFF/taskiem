-- Single sign-on signs in an owner only through a connection an owner
-- approved (F39b). Since F39 only an owner adds a connection, or adds or
-- verifies a domain, but a connection an administrator added before then
-- still signed in whoever its provider asserted on its verified domains,
-- owners included: an administrator who ran that provider could sign in
-- as an owner.
--
-- owner_approved_at marks a connection an owner set up or approved. The
-- API sets it when an owner creates a connection, or approves one with
-- PUT /v1/sso/{id}; finishSSO refuses a member who holds owner when it is
-- empty. Existing connections count as approved when the member who
-- created them holds owner in that tenant now; the rest wait for an owner
-- (owners are exempt from enforcement, so they keep their password or
-- passkey meanwhile). taskiem_dispatch reads sso_connections whole, so the
-- column is in dispatch_columns.txt; no definer returns it.
-- +goose Up
ALTER TABLE sso_connections ADD COLUMN owner_approved_at timestamptz;

UPDATE sso_connections c SET owner_approved_at = c.created_at
 WHERE EXISTS (SELECT 1 FROM memberships m
                WHERE m.tenant_id = c.tenant_id AND m.role = 'owner' AND m.user_id::text = c.created_by);
