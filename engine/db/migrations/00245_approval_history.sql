-- Approval evidence outlives the run (Atlas part D findings): retention
-- deletes a run's approvals and decisions, and the approvals report read
-- only those, so after 90 days it no longer showed who approved what. Each
-- decision is now also kept here, written with it and never purged: who,
-- for whom, the level, step-up, channel, policy and the amount, but not
-- the approval's subject, which can hold personal data and goes with the
-- run.

-- +goose Up
-- +goose StatementBegin
-- The amount an approval is about, in naira: the first numeric field of the
-- subject, in key order, whose name says amount or total or ends in kobo
-- (divided by 100). The approvals report's amount band.
CREATE FUNCTION taskiem_subject_amount(p_subject jsonb) RETURNS numeric
LANGUAGE sql IMMUTABLE AS $$
  SELECT CASE WHEN lower(e.key) LIKE '%kobo' THEN (e.value::text)::numeric / 100 ELSE (e.value::text)::numeric END
    FROM jsonb_each(CASE WHEN jsonb_typeof(p_subject) = 'object' THEN p_subject ELSE '{}'::jsonb END) e
   WHERE jsonb_typeof(e.value) = 'number'
     AND (lower(e.key) LIKE '%amount%' OR lower(e.key) LIKE '%total%' OR lower(e.key) LIKE '%\_kobo')
   ORDER BY e.key COLLATE "C"
   LIMIT 1
$$;
-- +goose StatementEnd

CREATE TABLE approval_history (
  tenant_id     uuid NOT NULL,
  run_id        uuid NOT NULL,
  step_id       text NOT NULL,
  level         int  NOT NULL,
  user_id       uuid NOT NULL REFERENCES users(id),
  on_behalf_of  uuid REFERENCES users(id),
  decision      text NOT NULL,
  step_up       text,
  channel       text NOT NULL,
  self_approved boolean NOT NULL DEFAULT false,
  decided_at    timestamptz NOT NULL,
  workflow_id   uuid NOT NULL,
  policy        text,
  amount_naira  numeric,
  PRIMARY KEY (run_id, step_id, level, user_id)
);
CREATE INDEX approval_history_decided ON approval_history (tenant_id, decided_at);
ALTER TABLE approval_history ENABLE ROW LEVEL SECURITY;
ALTER TABLE approval_history FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON approval_history TO taskiem_app
  USING (tenant_id = ANY (taskiem_tenant_scope())) WITH CHECK (tenant_id = ANY (taskiem_tenant_scope()));
GRANT SELECT, INSERT ON approval_history TO taskiem_app;

-- The decisions already made. Row-level security is forced even on the
-- schema owner, so it is lifted for the owner for this copy only (the
-- application role is unaffected), and forced again after.
ALTER TABLE approval_decisions NO FORCE ROW LEVEL SECURITY;
ALTER TABLE approvals NO FORCE ROW LEVEL SECURITY;
ALTER TABLE runs NO FORCE ROW LEVEL SECURITY;
ALTER TABLE approval_history NO FORCE ROW LEVEL SECURITY;
INSERT INTO approval_history (tenant_id, run_id, step_id, level, user_id, on_behalf_of, decision, step_up, channel, self_approved, decided_at, workflow_id, policy, amount_naira)
SELECT d.tenant_id, d.run_id, d.step_id, d.level, d.user_id, d.on_behalf_of, d.decision, d.step_up, d.channel, d.self_approved, d.decided_at,
       r.workflow_id, a.policy, taskiem_subject_amount(a.subject)
  FROM approval_decisions d
  JOIN approvals a ON a.run_id = d.run_id AND a.step_id = d.step_id
  JOIN runs r ON r.id = d.run_id;
ALTER TABLE approval_decisions FORCE ROW LEVEL SECURITY;
ALTER TABLE approvals FORCE ROW LEVEL SECURITY;
ALTER TABLE runs FORCE ROW LEVEL SECURITY;
ALTER TABLE approval_history FORCE ROW LEVEL SECURITY;

-- +goose Down
DROP TABLE approval_history;
DROP FUNCTION taskiem_subject_amount(jsonb);
