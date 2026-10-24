-- Students' account numbers (decision 0046; docs/spec/schools.md 6, B4;
-- docs/schools.md#account-numbers), and, for every business, the
-- look-back of credits and reversed money (docs/matching.md).
--
-- A student's account number IS a customer's permanent account
-- (customer_accounts, 00210) for the customer key:student:<id>: B1's rule
-- matches its credits. school_student_accounts is the school's side of
-- issuing it: once per student and connection (the rail's key is
-- student:<id>:acct:<connection>), what was asked and what the rail said,
-- and why it failed. A batch is a job (school_account_jobs) listing its
-- students; issuing each is claimed on its row, so a batch picked up
-- after an interruption never issues twice.
--
-- money_lookbacks are the connections whose credits the look-back reads
-- again over an overlapping window (every 15 minutes, the last 48 hours),
-- with how it went. money_reversals records, once per entry, money that
-- went back to the payer (a payment returned, reversed or refunded) and
-- the credit it took back: what an entry counts for is then less what
-- was reversed (taskiem_money_entry_reversed), so its matches are undone
-- and nothing counts it again.
-- +goose Up
ALTER TABLE school_settings ADD COLUMN issuing_channel text CHECK (issuing_channel ~ '^[a-z0-9_]{1,60}/[A-Za-z0-9_.-]{1,100}$');
ALTER TABLE school_settings ADD COLUMN issuing_account text CHECK (length(issuing_account) BETWEEN 1 AND 200);
ALTER TABLE school_settings ADD COLUMN siblings_share_credit boolean NOT NULL DEFAULT false;

CREATE TABLE school_student_accounts (
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  student_id   uuid NOT NULL REFERENCES school_students(id),
  channel      text NOT NULL CHECK (channel ~ '^[a-z0-9_]{1,60}/[A-Za-z0-9_.-]{1,100}$'),
  account      text NOT NULL DEFAULT '' CHECK (length(account) <= 200),  -- the wallet, on rails that hold several
  status       text NOT NULL CHECK (status IN ('issuing', 'issued', 'failed', 'unknown')),
  account_id   uuid REFERENCES customer_accounts(id),
  details      jsonb,                     -- sealed under the student's subject: {asked, named}, the names
  shortened    boolean NOT NULL DEFAULT false,  -- the school's name was shortened to fit
  flagged      boolean NOT NULL DEFAULT false,  -- the child's name alone did not fit, and was cut
  error        text CHECK (length(error) <= 300),
  retry        boolean NOT NULL DEFAULT false,  -- the failure may pass on its own (sent again)
  attempts     int NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  job_id       uuid,
  claimed_at   timestamptz,
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, student_id, channel),
  CHECK ((status = 'issued') = (account_id IS NOT NULL))
);
CREATE UNIQUE INDEX school_student_accounts_account ON school_student_accounts (account_id) WHERE account_id IS NOT NULL;
ALTER TABLE school_student_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_student_accounts FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_student_accounts TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_student_accounts TO taskiem_app;
GRANT UPDATE (status, account_id, details, shortened, flagged, error, retry, attempts, job_id, claimed_at, updated_at) ON school_student_accounts TO taskiem_app;

-- A batch: the students it issues to (at most 500), on one connection.
CREATE TABLE school_account_jobs (
  id           uuid PRIMARY KEY,
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  channel      text NOT NULL CHECK (length(channel) <= 200),
  account      text NOT NULL DEFAULT '' CHECK (length(account) <= 200),
  status       text NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'done')),
  total        int NOT NULL CHECK (total BETWEEN 1 AND 500),
  plan_hash    text NOT NULL CHECK (length(plan_hash) <= 64),
  created_by   text NOT NULL,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now(),
  finished_at  timestamptz
);
CREATE INDEX school_account_jobs_recent ON school_account_jobs (tenant_id, created_at DESC);
CREATE UNIQUE INDEX school_account_jobs_plan ON school_account_jobs (tenant_id, plan_hash);
ALTER TABLE school_account_jobs ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_account_jobs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_account_jobs TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_account_jobs TO taskiem_app;
GRANT UPDATE (status, updated_at, finished_at) ON school_account_jobs TO taskiem_app;

CREATE TABLE school_account_job_students (
  job_id      uuid NOT NULL REFERENCES school_account_jobs(id),
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  student_id  uuid NOT NULL REFERENCES school_students(id),
  position    int NOT NULL,
  PRIMARY KEY (job_id, student_id)
);
ALTER TABLE school_account_job_students ENABLE ROW LEVEL SECURITY;
ALTER TABLE school_account_job_students FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON school_account_job_students TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON school_account_job_students TO taskiem_app;

-- The rail's own id for one credit to a permanent account (in the key
-- since B0), kept so a refund naming it finds the credit.
ALTER TABLE money_entries ADD COLUMN credit_id text CHECK (length(credit_id) <= 200);
CREATE INDEX money_entries_credit ON money_entries (tenant_id, channel, credit_id) WHERE credit_id IS NOT NULL;

-- The look-back's connections, and how it went on each.
CREATE TABLE money_lookbacks (
  tenant_id      uuid NOT NULL REFERENCES tenants(id),
  channel        text NOT NULL CHECK (length(channel) <= 200),
  account        text NOT NULL DEFAULT '' CHECK (length(account) <= 200),
  last_run_at    timestamptz,
  last_ok_at     timestamptz,
  failures       int NOT NULL DEFAULT 0 CHECK (failures >= 0),
  last_error     text CHECK (length(last_error) <= 500),
  found          int NOT NULL DEFAULT 0 CHECK (found >= 0),    -- credits only the look-back found, ever
  missed_on      date,                                         -- the day the owner was last told the webhook missed one
  created_at     timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (tenant_id, channel, account)
);
ALTER TABLE money_lookbacks ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_lookbacks FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_lookbacks TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON money_lookbacks TO taskiem_app;
GRANT UPDATE (last_run_at, last_ok_at, failures, last_error, found, missed_on) ON money_lookbacks TO taskiem_app;

-- Money that went back to the payer, once per entry saying so (by_entry:
-- a payment returned or reversed, or a refund settled), and the credit it
-- took back (entry_id; NULL when the log has no such credit). amount_kobo
-- is how much went back; NULL is all of it.
CREATE TABLE money_reversals (
  by_entry     uuid PRIMARY KEY REFERENCES money_entries(id),
  tenant_id    uuid NOT NULL REFERENCES tenants(id),
  entry_id     uuid REFERENCES money_entries(id),
  amount_kobo  bigint CHECK (amount_kobo >= 0),
  undone       int NOT NULL DEFAULT 0 CHECK (undone >= 0),   -- matches undone
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX money_reversals_entry ON money_reversals (entry_id) WHERE entry_id IS NOT NULL;
ALTER TABLE money_reversals ENABLE ROW LEVEL SECURITY;
ALTER TABLE money_reversals FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON money_reversals TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON money_reversals TO taskiem_app;
GRANT UPDATE (undone) ON money_reversals TO taskiem_app;

-- What of an entry went back to the payer: all of what it counts for when
-- a reversal says all, else the reversals' sum, at most what it counts
-- for. 0 when none.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_entry_reversed(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT COALESCE((SELECT CASE WHEN count(*) = 0 THEN 0 WHEN bool_or(r.amount_kobo IS NULL) THEN COALESCE(c.countable, 0)
                               ELSE LEAST(sum(r.amount_kobo), COALESCE(c.countable, sum(r.amount_kobo))) END
                     FROM money_reversals r WHERE r.entry_id = p_entry), 0)
    FROM (SELECT taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net) AS countable FROM money_entries e WHERE e.id = p_entry) c
$$;
-- +goose StatementEnd
-- What is left of an entry to count, now less what went back.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_left(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net) - taskiem_money_entry_reversed(e.id) - taskiem_money_entry_counted(e.id)
    FROM money_entries e WHERE e.id = p_entry
$$;
-- +goose StatementEnd
-- A part is refused when the parts would add up to more than the entry
-- counts for, less what went back.
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_part() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  countable bigint;
  counted bigint;
BEGIN
  IF NEW.entry_id IS NULL OR NEW.undoes IS NOT NULL THEN
    RETURN NEW;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('taskiem money entry ' || NEW.entry_id::text, 0));
  SELECT taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net) INTO countable FROM money_entries e WHERE e.id = NEW.entry_id;
  IF countable IS NULL THEN
    RETURN NEW; -- an entry with no amount: what the rail said is counted
  END IF;
  countable := countable - taskiem_money_entry_reversed(NEW.entry_id);
  counted := taskiem_money_entry_counted(NEW.entry_id);
  IF counted + NEW.amount_kobo > countable THEN
    RAISE EXCEPTION 'the parts of a payment cannot add up to more than it (% counted of %, % more)', counted, countable, NEW.amount_kobo
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd

-- Across tenants, for the background work (names only, so the dispatch
-- role's): tenants with reversals not yet looked at; batches still
-- running; the look-back's connections that are still active in prod.
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_unreversed_tenants()
RETURNS TABLE (tenant_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT DISTINCT e.tenant_id FROM money_entries e
   WHERE (e.direction = 'in' AND e.status = 'reversed' OR e.kind = 'refund' AND e.status = 'settled')
     AND NOT EXISTS (SELECT 1 FROM money_reversals r WHERE r.by_entry = e.id)
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_school_account_jobs()
RETURNS TABLE (tenant_id uuid, job_id uuid)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT j.tenant_id, j.id FROM school_account_jobs j JOIN school_settings s ON s.tenant_id = j.tenant_id AND s.enabled
   WHERE j.status = 'running' ORDER BY j.created_at
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE FUNCTION taskiem_money_lookback_targets()
RETURNS TABLE (tenant_id uuid, channel text, account text)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT l.tenant_id, l.channel, l.account FROM money_lookbacks l JOIN tenants t ON t.id = l.tenant_id AND t.status = 'active'
   WHERE EXISTS (SELECT 1 FROM connections c WHERE c.tenant_id = l.tenant_id AND c.environment = 'prod' AND c.status = 'active'
                   AND c.connector || '/' || c.name = l.channel)
     AND (l.last_run_at IS NULL OR l.last_run_at < now() - interval '15 minutes')
   ORDER BY l.last_run_at NULLS FIRST
$$;
-- +goose StatementEnd
CREATE POLICY dispatch ON money_reversals FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (by_entry, tenant_id) ON money_reversals TO taskiem_dispatch;
CREATE POLICY dispatch ON school_account_jobs FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (id, tenant_id, status, created_at) ON school_account_jobs TO taskiem_dispatch;
CREATE POLICY dispatch ON money_lookbacks FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, channel, account, last_run_at) ON money_lookbacks TO taskiem_dispatch;
ALTER FUNCTION taskiem_money_unreversed_tenants() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_school_account_jobs() OWNER TO taskiem_dispatch;
ALTER FUNCTION taskiem_money_lookback_targets() OWNER TO taskiem_dispatch;
REVOKE EXECUTE ON FUNCTION taskiem_money_unreversed_tenants(), taskiem_school_account_jobs(), taskiem_money_lookback_targets() FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_money_unreversed_tenants(), taskiem_school_account_jobs(), taskiem_money_lookback_targets() TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_money_lookback_targets();
DROP FUNCTION taskiem_school_account_jobs();
DROP FUNCTION taskiem_money_unreversed_tenants();
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_part() RETURNS trigger
LANGUAGE plpgsql SET search_path = public, pg_temp AS $$
DECLARE
  countable bigint;
  counted bigint;
BEGIN
  IF NEW.entry_id IS NULL OR NEW.undoes IS NOT NULL THEN
    RETURN NEW;
  END IF;
  PERFORM pg_advisory_xact_lock(hashtextextended('taskiem money entry ' || NEW.entry_id::text, 0));
  SELECT taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net) INTO countable FROM money_entries e WHERE e.id = NEW.entry_id;
  IF countable IS NULL THEN
    RETURN NEW;
  END IF;
  counted := taskiem_money_entry_counted(NEW.entry_id);
  IF counted + NEW.amount_kobo > countable THEN
    RAISE EXCEPTION 'the parts of a payment cannot add up to more than it (% counted of %, % more)', counted, countable, NEW.amount_kobo
      USING ERRCODE = '23514';
  END IF;
  RETURN NEW;
END
$$;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION taskiem_money_entry_left(p_entry uuid) RETURNS bigint
LANGUAGE sql STABLE SET search_path = public, pg_temp AS $$
  SELECT taskiem_money_entry_countable(e.amount_kobo, e.fee_kobo, e.net) - taskiem_money_entry_counted(e.id) FROM money_entries e WHERE e.id = p_entry
$$;
-- +goose StatementEnd
DROP FUNCTION taskiem_money_entry_reversed(uuid);
DROP TABLE money_reversals;
DROP TABLE money_lookbacks;
DROP INDEX money_entries_credit;
ALTER TABLE money_entries DROP COLUMN credit_id;
DROP TABLE school_account_job_students;
DROP TABLE school_account_jobs;
DROP TABLE school_student_accounts;
ALTER TABLE school_settings DROP COLUMN siblings_share_credit;
ALTER TABLE school_settings DROP COLUMN issuing_account;
ALTER TABLE school_settings DROP COLUMN issuing_channel;
