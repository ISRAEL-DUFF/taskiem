-- Where a password reset link was delivered (docs/spec/chat-sign-in.md 6).
-- A link sent to the mailbox proves the mailbox, so using it confirms the
-- email of an account never confirmed; one sent in chat ("password")
-- proves only the chat, and confirms nothing. The origin is kept with the
-- link. Chat links issued before this carry the channel's name where the
-- request's address goes.
-- +goose Up
ALTER TABLE password_resets ADD COLUMN origin text NOT NULL DEFAULT 'email' CHECK (origin IN ('email', 'chat'));
UPDATE password_resets SET origin = 'chat' WHERE ip IN ('whatsapp', 'telegram', 'slack', 'web');

-- Issues a link delivered by p_origin ('email' or 'chat'), replacing the
-- person's unused ones.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_password_reset_issue(p_user uuid, p_selector bytea, p_hash bytea, p_ttl interval, p_ip text, p_origin text)
RETURNS void LANGUAGE sql VOLATILE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  DELETE FROM password_resets WHERE expires_at < now() - interval '1 day';
  DELETE FROM password_resets WHERE user_id = p_user AND used_at IS NULL;
  INSERT INTO password_resets (selector, secret_hash, user_id, expires_at, ip, origin) VALUES (p_selector, p_hash, p_user, now() + p_ttl, p_ip, p_origin);
$$;
-- +goose StatementEnd

-- How a link was delivered; NULL for one that is unknown.
-- +goose StatementBegin
CREATE FUNCTION taskiem_auth_password_reset_origin(p_selector bytea)
RETURNS text LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT pr.origin FROM password_resets pr WHERE pr.selector = p_selector
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text, text) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_auth_password_reset_origin(bytea) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text, text), taskiem_auth_password_reset_origin(bytea) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text, text), taskiem_auth_password_reset_origin(bytea) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_auth_password_reset_origin(bytea);
DROP FUNCTION taskiem_auth_password_reset_issue(uuid, bytea, bytea, interval, text, text);
ALTER TABLE password_resets DROP COLUMN origin;
