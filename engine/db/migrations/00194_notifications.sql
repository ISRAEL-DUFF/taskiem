-- +goose Up
-- Notifications (no-technical-steps N12, decision N0-10): what each person
-- needs to know, in the app's bell, with nothing to set up. A run that
-- failed tells the owners, whoever started it and whoever published its
-- workflow; a run a member started finishes to them; every owner alert
-- (alerts.TellOwners) and every alert rule's finding is there too. Repeats
-- of one thing are one item while it is unread (group_key), counted.

CREATE TABLE notifications (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  kind        text NOT NULL CHECK (kind ~ '^[a-z_]{1,40}$'),
  level       text NOT NULL CHECK (level IN ('info', 'success', 'warning', 'problem')),
  title       text NOT NULL CHECK (length(title) BETWEEN 1 AND 200),
  body        text NOT NULL DEFAULT '' CHECK (length(body) <= 1000),
  link        text CHECK (length(link) <= 500),
  group_key   text NOT NULL CHECK (length(group_key) <= 200),
  count       int NOT NULL DEFAULT 1 CHECK (count >= 1),
  run_id      uuid,
  alert_id    uuid,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  read_at     timestamptz
);
CREATE UNIQUE INDEX notifications_open ON notifications (tenant_id, user_id, group_key) WHERE read_at IS NULL;
CREATE INDEX notifications_recent ON notifications (tenant_id, user_id, updated_at DESC);
CREATE INDEX notifications_expired ON notifications (updated_at);

-- Each person's choice, per kind of notification, of chat and email
-- besides the app (which always shows them). No row: the default.
CREATE TABLE notification_settings (
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  user_id     uuid NOT NULL REFERENCES users(id),
  kind        text NOT NULL CHECK (kind IN ('failures', 'setup', 'finished')),
  chat        boolean NOT NULL,
  email       boolean NOT NULL,
  updated_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, user_id, kind)
);

-- +goose StatementBegin
DO $$
DECLARE t text;
BEGIN
  FOREACH t IN ARRAY ARRAY['notifications', 'notification_settings'] LOOP
    EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
    EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
    EXECUTE format('CREATE POLICY tenant_isolation ON %I TO taskiem_app USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()))', t);
    EXECUTE format('GRANT SELECT, INSERT, UPDATE, DELETE ON %I TO taskiem_app', t);
  END LOOP;
END
$$;
-- +goose StatementEnd

-- How runs ended, for the notifier: written as a run fails, finishes from a
-- person's start, or waits for reconciliation, in the same transaction;
-- taken once.
CREATE TABLE run_outcomes (
  run_id       uuid NOT NULL,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  status       text NOT NULL CHECK (status IN ('failed', 'completed', 'needs_reconciliation')),
  created_at   timestamptz NOT NULL DEFAULT now(),
  claimed_until timestamptz,
  PRIMARY KEY (run_id, status)
);
CREATE INDEX run_outcomes_due ON run_outcomes (created_at);
ALTER TABLE run_outcomes ENABLE ROW LEVEL SECURITY;
ALTER TABLE run_outcomes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON run_outcomes TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, DELETE ON run_outcomes TO taskiem_app;
CREATE POLICY dispatch ON run_outcomes TO taskiem_dispatch USING (true) WITH CHECK (true);
GRANT SELECT, UPDATE, DELETE ON run_outcomes TO taskiem_dispatch;
CREATE POLICY dispatch ON notifications FOR DELETE TO taskiem_dispatch USING (true);
GRANT SELECT (updated_at), DELETE ON notifications TO taskiem_dispatch;

-- +goose StatementBegin
-- Claims up to p_limit outcomes for five minutes, oldest first.
CREATE FUNCTION taskiem_run_outcomes_claim(p_limit int)
RETURNS TABLE (run_id uuid, tenant_id uuid, status text)
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  UPDATE run_outcomes o SET claimed_until = now() + interval '5 minutes'
   WHERE (o.run_id, o.status) IN (SELECT x.run_id, x.status FROM run_outcomes x WHERE x.claimed_until IS NULL OR x.claimed_until < now()
                                   ORDER BY x.created_at LIMIT p_limit FOR UPDATE SKIP LOCKED)
  RETURNING o.run_id, o.tenant_id, o.status
$$;
-- +goose StatementEnd

-- +goose StatementBegin
-- Notifications are kept 90 days.
CREATE FUNCTION taskiem_notifications_expire()
RETURNS bigint
LANGUAGE sql SECURITY DEFINER SET search_path = public, pg_temp AS $$
  WITH d AS (DELETE FROM notifications WHERE updated_at < now() - interval '90 days' RETURNING 1) SELECT count(*) FROM d
$$;
-- +goose StatementEnd

ALTER FUNCTION taskiem_run_outcomes_claim(int) OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_notifications_expire() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_run_outcomes_claim(int), taskiem_notifications_expire() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_run_outcomes_claim(int), taskiem_notifications_expire() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_notifications_expire();
DROP FUNCTION taskiem_run_outcomes_claim(int);
DROP TABLE run_outcomes;
DROP TABLE notification_settings;
DROP TABLE notifications;
