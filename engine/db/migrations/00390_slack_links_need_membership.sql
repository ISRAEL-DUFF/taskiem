-- A Slack link speaks only for a current member (F19). Removing a person
-- from a business leaves their slack_bindings row, and
-- taskiem_slack_user_of returned it without asking whether they were
-- still a member: the Slack DM carried that business as "linked", so a
-- removed member's "new business" took its Slack token. Every caller (the
-- DM, button clicks, the chat history and sign-up from Slack) reads links
-- through this function, so the link of someone with no membership left
-- in that business now finds nothing; being added again brings it back.
-- +goose Up
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_slack_user_of(p_team text, p_user text)
RETURNS TABLE (tenant_id uuid, user_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.tenant_id, b.user_id FROM slack_bindings b
    JOIN tenants t ON t.id = b.tenant_id AND t.status = 'active'
   WHERE b.team_id = p_team AND b.slack_user_id = p_user AND b.verified_at IS NOT NULL
     AND EXISTS (SELECT 1 FROM memberships m WHERE m.tenant_id = b.tenant_id AND m.user_id = b.user_id)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_slack_user_of(text, text) OWNER TO taskiem_dispatch;

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_slack_user_of(p_team text, p_user text)
RETURNS TABLE (tenant_id uuid, user_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT b.tenant_id, b.user_id FROM slack_bindings b
    JOIN tenants t ON t.id = b.tenant_id AND t.status = 'active'
   WHERE b.team_id = p_team AND b.slack_user_id = p_user AND b.verified_at IS NOT NULL
$$;
-- +goose StatementEnd
