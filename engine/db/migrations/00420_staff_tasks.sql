-- Tasks for staff (docs/spec/chat-five-more-flows.md, flow 2 and the
-- phase 3 plan, step 4): something to do, given by an owner or a manager
-- to one person or to a role ("any cashier": the first to take it has
-- it), maybe by a time, maybe needing a photo when done, maybe about an
-- order, a bill, an invoice or a stock item. And tasks that repeat: each time
-- one falls due it makes a task of its own.

-- +goose Up
CREATE TABLE staff_task_repeats (
  id         uuid PRIMARY KEY,
  tenant_id  uuid NOT NULL REFERENCES tenants(id),
  what       text NOT NULL CHECK (length(what) BETWEEN 1 AND 200),
  given_by   uuid NOT NULL REFERENCES users(id),
  to_user    uuid REFERENCES users(id),
  to_role    text CHECK (to_role IS NULL OR length(to_role) BETWEEN 1 AND 60),
  photo      boolean NOT NULL DEFAULT false,
  -- Every day, or every week on one day (mon … sun), due at "HH:MM", Lagos.
  every      text NOT NULL CHECK (every IN ('day', 'week')),
  weekday    text CHECK (weekday IN ('mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun')),
  at         text NOT NULL CHECK (at ~ '^([01][0-9]|2[0-3]):[0-5][0-9]$'),
  -- The last day it made a task (Lagos).
  made_on    date,
  stopped_at timestamptz,
  created_at timestamptz NOT NULL DEFAULT now(),
  CHECK ((to_user IS NULL) <> (to_role IS NULL)),
  CHECK ((every = 'week') = (weekday IS NOT NULL))
);
ALTER TABLE staff_task_repeats ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_task_repeats FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_task_repeats TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE ON staff_task_repeats TO taskiem_app;

CREATE TABLE staff_tasks (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  what        text NOT NULL CHECK (length(what) BETWEEN 1 AND 200),
  given_by    uuid NOT NULL REFERENCES users(id),
  -- To one person, or to a role until someone takes it (taken_by).
  to_user     uuid REFERENCES users(id),
  to_role     text CHECK (to_role IS NULL OR length(to_role) BETWEEN 1 AND 60),
  taken_by    uuid REFERENCES users(id),
  due_at      timestamptz,
  photo       boolean NOT NULL DEFAULT false,
  state       text NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'done', 'cant', 'cancelled')),
  done_at     timestamptz,
  done_by     uuid REFERENCES users(id),
  -- The photo it was done with: a path in the business's own storage.
  photo_path  text CHECK (photo_path IS NULL OR length(photo_path) <= 500),
  -- DONE said for a task that needs a photo: the next photo closes it.
  photo_asked timestamptz,
  cant_why    text CHECK (cant_why IS NULL OR length(cant_why) <= 300),
  link_kind   text CHECK (link_kind IN ('order', 'invoice', 'bill', 'purchase', 'stock')),
  link_id     text CHECK (link_id IS NULL OR length(link_id) BETWEEN 1 AND 100),
  link_name   text CHECK (link_name IS NULL OR length(link_name) BETWEEN 1 AND 120),
  repeat_id   uuid REFERENCES staff_task_repeats(id),
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CHECK ((to_user IS NULL) <> (to_role IS NULL)),
  CHECK ((link_kind IS NULL) = (link_id IS NULL)),
  CHECK ((state = 'done') = (done_at IS NOT NULL))
);
CREATE INDEX staff_tasks_open ON staff_tasks (tenant_id, due_at) WHERE state = 'open';
CREATE INDEX staff_tasks_person ON staff_tasks (tenant_id, COALESCE(taken_by, to_user), created_at);
CREATE UNIQUE INDEX staff_tasks_repeat_day ON staff_tasks (repeat_id, due_at) WHERE repeat_id IS NOT NULL;
ALTER TABLE staff_tasks ENABLE ROW LEVEL SECURITY;
ALTER TABLE staff_tasks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON staff_tasks TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON staff_tasks TO taskiem_app;

-- The staff job also has work where a task is open or a repeat is live.
-- It reads which businesses only: no task's words, people or times.
CREATE POLICY dispatch ON staff_tasks FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, state) ON staff_tasks TO taskiem_dispatch;
CREATE POLICY dispatch ON staff_task_repeats FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, stopped_at) ON staff_task_repeats TO taskiem_dispatch;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_staff_tenants()
RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM staff_profiles WHERE rota <> '[]'::jsonb
  UNION
  SELECT tenant_id FROM staff_clock WHERE out_at IS NULL
  UNION
  SELECT tenant_id FROM staff_tasks WHERE state = 'open'
  UNION
  SELECT tenant_id FROM staff_task_repeats WHERE stopped_at IS NULL
$$;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_staff_tenants()
RETURNS SETOF uuid
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT tenant_id FROM staff_profiles WHERE rota <> '[]'::jsonb
  UNION
  SELECT tenant_id FROM staff_clock WHERE out_at IS NULL
$$;
-- +goose StatementEnd
REVOKE SELECT (tenant_id, stopped_at) ON staff_task_repeats FROM taskiem_dispatch;
REVOKE SELECT (tenant_id, state) ON staff_tasks FROM taskiem_dispatch;
DROP TABLE staff_tasks;
DROP TABLE staff_task_repeats;
