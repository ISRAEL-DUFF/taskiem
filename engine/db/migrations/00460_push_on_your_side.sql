-- Push on a person's own side (taskiem-for-everyone §8.3, §9; founder's
-- decision, 2026-11-17). A person's devices may be registered with no
-- business (tenant_id NULL), from a personal session, or from a business
-- session for "You": they take one kind of push, 'you' (a business's
-- message in their chat with it, and its order updates there), and open
-- that chat on You. They are the person's own: scoped by
-- taskiem_person_scope(), like person_conversations, and never dropped for
-- having no membership. The queue (push_outbox) holds their pushes the same
-- way, drained person by person in each one's own scope.
-- +goose Up
ALTER TABLE push_subscriptions
  ALTER COLUMN tenant_id DROP NOT NULL,
  DROP CONSTRAINT push_subscriptions_kinds_check,
  ADD CONSTRAINT push_subscriptions_kinds_check CHECK (kinds <@ ARRAY['business', 'customers', 'orders', 'approvals', 'alerts', 'you']),
  -- A business's device never takes 'you'; a person's own takes nothing else.
  ADD CONSTRAINT push_subscriptions_side CHECK (CASE WHEN tenant_id IS NULL THEN kinds <@ ARRAY['you'] ELSE NOT ('you' = ANY (kinds)) END);
CREATE UNIQUE INDEX push_subscriptions_person ON push_subscriptions (user_id, endpoint_hash) WHERE tenant_id IS NULL;
CREATE POLICY person_own ON push_subscriptions TO taskiem_app
  USING (tenant_id IS NULL AND user_id = taskiem_person_scope())
  WITH CHECK (tenant_id IS NULL AND user_id = taskiem_person_scope());

ALTER TABLE push_outbox
  ALTER COLUMN tenant_id DROP NOT NULL,
  DROP CONSTRAINT push_outbox_kind_check,
  ADD CONSTRAINT push_outbox_kind_check CHECK (kind IN ('business', 'customers', 'orders', 'approvals', 'alerts', 'you')),
  ADD CONSTRAINT push_outbox_side CHECK ((tenant_id IS NULL) = (kind = 'you'));
CREATE INDEX push_outbox_person_due ON push_outbox (user_id, next_at) WHERE tenant_id IS NULL;
CREATE POLICY person_own ON push_outbox TO taskiem_app
  USING (tenant_id IS NULL AND user_id = taskiem_person_scope())
  WITH CHECK (tenant_id IS NULL AND user_id = taskiem_person_scope());

-- The businesses with pushes due (a person's own are not a business's).
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_push_due(p_limit int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.tenant_id FROM push_outbox o WHERE o.next_at <= now() AND o.tenant_id IS NOT NULL LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd

-- The people with pushes of their own due: the sender drains each in the
-- person's own scope.
-- +goose StatementBegin
CREATE FUNCTION taskiem_push_due_people(p_limit int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.user_id FROM push_outbox o WHERE o.next_at <= now() AND o.tenant_id IS NULL LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_push_due_people(int) OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_push_due_people(int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_push_due_people(int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_push_due_people(int);
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_push_due(p_limit int) RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT o.tenant_id FROM push_outbox o WHERE o.next_at <= now() LIMIT LEAST(GREATEST(p_limit, 1), 500)
$$;
-- +goose StatementEnd
DROP POLICY person_own ON push_outbox;
DROP INDEX push_outbox_person_due;
DELETE FROM push_outbox WHERE tenant_id IS NULL;
ALTER TABLE push_outbox
  DROP CONSTRAINT push_outbox_side,
  DROP CONSTRAINT push_outbox_kind_check,
  ADD CONSTRAINT push_outbox_kind_check CHECK (kind IN ('business', 'customers', 'orders', 'approvals', 'alerts')),
  ALTER COLUMN tenant_id SET NOT NULL;
DROP POLICY person_own ON push_subscriptions;
DROP INDEX push_subscriptions_person;
DELETE FROM push_subscriptions WHERE tenant_id IS NULL;
ALTER TABLE push_subscriptions
  DROP CONSTRAINT push_subscriptions_side,
  DROP CONSTRAINT push_subscriptions_kinds_check,
  ADD CONSTRAINT push_subscriptions_kinds_check CHECK (kinds <@ ARRAY['business', 'customers', 'orders', 'approvals', 'alerts']),
  ALTER COLUMN tenant_id SET NOT NULL;
