-- Conversations (decision 0062 V7; docs/conversations.md): what a business's
-- assistant and its people said to each customer, for the conversations
-- page, and runs that only pretend to send (Talk to it here).
--
-- assistant_turns   one row per message: the customer's (recorded by the
--                   guard when it arrives, with what the guard did), and
--                   every message sent to them (by a run's send step, a
--                   member by hand, or the guard's fixed answers through a
--                   run), a voice note's transcript, and a reply the check
--                   held back. Text is sealed under the customer's key, so
--                   erasing them erases it; rows go with the business's
--                   conversation days.
-- runs.sandbox      a test conversation's run: everything is real except
--                   what it would change (sends, writes), which is
--                   simulated and recorded.

-- +goose Up
CREATE TABLE assistant_turns (
  id          uuid PRIMARY KEY,
  tenant_id   uuid NOT NULL REFERENCES tenants(id),
  channel     text NOT NULL,                -- <connector ref>/<connection>/<environment>, or test/<member>
  subject     text NOT NULL,                -- the customer's subject
  at          timestamptz NOT NULL DEFAULT now(),
  role        text NOT NULL CHECK (role IN ('customer', 'assistant', 'member', 'held')),
  kind        text NOT NULL DEFAULT 'text', -- text, voice, audio, image, ... (and template for a member's)
  body        jsonb,                        -- the words, sealed under the customer
  transcript  jsonb,                        -- a voice note's words, sealed under the customer
  file        jsonb,                        -- a run file's reference (a voice note, a spoken reply)
  status      text,                         -- the customer's: answer, reply, quiet, or why it got nothing
  reason      text,                         -- the guard's reason, or the check's problems
  dedup       text,                         -- the customer's: the delivery's id
  run_id      uuid,                         -- the run the customer's message started (a child run's sends: its root)
  step_run    uuid,                         -- the run whose step sent it (the root, or a child of it)
  step        text,
  by_actor    text,                         -- a member's
  request_id  text                          -- a member's send, so a retry does not send twice
);
CREATE INDEX assistant_turns_conversation ON assistant_turns (tenant_id, channel, subject, at);
CREATE INDEX assistant_turns_run ON assistant_turns (tenant_id, run_id) WHERE run_id IS NOT NULL;
CREATE INDEX assistant_turns_age ON assistant_turns (tenant_id, at);
CREATE UNIQUE INDEX assistant_turns_inbound ON assistant_turns (tenant_id, channel, dedup) WHERE dedup IS NOT NULL;
CREATE UNIQUE INDEX assistant_turns_request ON assistant_turns (tenant_id, request_id) WHERE request_id IS NOT NULL;
CREATE UNIQUE INDEX assistant_turns_step ON assistant_turns (tenant_id, step_run, step, role) WHERE step_run IS NOT NULL;
CREATE INDEX assistant_turns_subject ON assistant_turns (tenant_id, subject text_pattern_ops);
ALTER TABLE assistant_turns ENABLE ROW LEVEL SECURITY;
ALTER TABLE assistant_turns FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON assistant_turns TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT, UPDATE, DELETE ON assistant_turns TO taskiem_app;

ALTER TABLE runs ADD COLUMN sandbox boolean NOT NULL DEFAULT false;

-- The customer list: when each customer's conversation last moved and who
-- spoke, and a hint to show them by (a number's last four digits; never
-- the number), so the list reads one index and opens no envelope.
ALTER TABLE assistant_contacts
  ADD COLUMN last_turn_at timestamptz,
  ADD COLUMN last_role    text,
  ADD COLUMN hint         text NOT NULL DEFAULT '';
CREATE INDEX assistant_contacts_recent ON assistant_contacts (tenant_id, channel, (COALESCE(last_turn_at, last_inbound_at)) DESC);

-- The hourly sweep, across tenants: the businesses with turns older than
-- a week (the shortest a plan keeps conversations), a page at a time after
-- a tenant id, with their conversation days; the api role caps the days by
-- the plan and deletes in each business's scope. Ids and days only.
CREATE POLICY turns_expiry ON assistant_turns FOR SELECT TO taskiem_dispatch USING (true);
GRANT SELECT (tenant_id, at) ON assistant_turns TO taskiem_dispatch;
-- +goose StatementBegin
CREATE FUNCTION taskiem_conversation_turns_due(p_after uuid, p_limit int) RETURNS TABLE (tenant_id uuid, days int)
LANGUAGE sql STABLE SECURITY DEFINER SET search_path = public, pg_temp AS $$
  SELECT t.id, COALESCE(cs.conversation_days, 30) FROM tenants t
    LEFT JOIN tenant_channel_settings cs ON cs.tenant_id = t.id
   WHERE t.id > p_after
     AND EXISTS (SELECT 1 FROM assistant_turns a WHERE a.tenant_id = t.id AND a.at < now() - interval '1 day')
   ORDER BY t.id
   LIMIT p_limit
$$;
-- +goose StatementEnd
ALTER FUNCTION taskiem_conversation_turns_due(uuid, int) OWNER TO taskiem_dispatch;
REVOKE ALL ON FUNCTION taskiem_conversation_turns_due(uuid, int) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION taskiem_conversation_turns_due(uuid, int) TO taskiem_app;

-- +goose Down
DROP FUNCTION taskiem_conversation_turns_due(uuid, int);
ALTER TABLE assistant_contacts DROP COLUMN last_turn_at, DROP COLUMN last_role, DROP COLUMN hint;
ALTER TABLE runs DROP COLUMN sandbox;
DROP TABLE assistant_turns;
