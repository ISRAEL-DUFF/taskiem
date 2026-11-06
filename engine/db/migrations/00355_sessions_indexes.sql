-- +goose NO TRANSACTION
-- Revoking a person's sign-in sessions (a password change or reset, a
-- passkey reset, a role revoked, SCIM deprovisioning) filters sessions by
-- user_id, as does deleting a person (the foreign key), and sessions had
-- no index but its token hash, so each scanned every session ever made.
-- The purge of old sessions (00356) reads expires_at. sessions is large,
-- so the indexes are built CONCURRENTLY (CONTRIBUTING.md, Migrations) (F77).

-- +goose Up
CREATE INDEX CONCURRENTLY sessions_user ON sessions (user_id);
CREATE INDEX CONCURRENTLY sessions_expires ON sessions (expires_at);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS sessions_expires;
DROP INDEX CONCURRENTLY IF EXISTS sessions_user;
