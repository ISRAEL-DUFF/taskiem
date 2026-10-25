import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { get, post } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Item, Items, PageHeader, fmtTime, useAction, useLoad } from "../ui";
import { schoolAccess } from "../lib/schools";
import { accountStatus, feeLine, jobLine, jobPercent, planSummary, type AccountJob, type AccountPlan } from "../lib/schoolAccounts";
import { plainly } from "./SchoolAccountParts";
import { SchoolTabs } from "./Schools";

// Schools > Account numbers (docs/spec/schools.md 6.2, B4;
// docs/schools.md#account-numbers): issuing every student their own
// account number in batches: the plan, its confirmation, the batch's
// progress, and who was not issued and why.

/** Schools > Account numbers (POST /v1/schools/accounts/issue): the plan, its confirmation, the batch's progress and who was not issued and why. */
export function SchoolAccountsPage() {
  const { me, can } = useAuth();
  const may = schoolAccess(can);
  const [plan, setPlan] = useState<AccountPlan>();
  const [job, setJob] = useState<string>();
  const act = useAction();
  const jobs = useLoad(() => get<{ jobs: AccountJob[] }>("/v1/schools/accounts/jobs"), [job]);
  const running = job ?? jobs.data?.jobs.find((j) => j.status === "running")?.id;
  // Followed every 2 seconds until it is done.
  const [finished, setFinished] = useState<string>();
  const live = useLoad(() => (running ? get<AccountJob>(`/v1/schools/accounts/jobs/${running}`) : Promise.resolve(undefined)), [running], !!running && finished !== running);
  useEffect(() => {
    if (live.data?.status === "done") setFinished(live.data.id);
  }, [live.data]);
  if (!me?.schools) return <PageHeader title="Account numbers" />;
  const shown = live.data ?? jobs.data?.jobs[0];
  const check = () =>
    act.run(async () => {
      const r = await plainly(post<{ plan: AccountPlan }>("/v1/schools/accounts/issue", {}));
      setPlan(r.plan);
    }, false);
  return (
    <>
      <PageHeader
        title="Account numbers"
        description="Every student their own bank account number on the school's issuing connection. A parent's transfer into it is matched to that child's fees by itself, oldest term first."
      />
      <SchoolTabs />
      {may.manage && (
        <section className="card" aria-labelledby="issue-heading">
          <h2 id="issue-heading" style={{ marginTop: 0 }}>
            Issue account numbers
          </h2>
          <p className="hint">
            Check first: nothing is issued until you confirm. Each student is issued one account once, however often you run it; a batch that stops is picked up where it stopped.
          </p>
          <button type="button" onClick={() => void check()} disabled={act.busy}>
            {plan ? "Check again" : "Check who gets one"}
          </button>
          <ErrorBox error={act.error} />
          {plan && (
            <PlanView
              plan={plan}
              busy={act.busy}
              onConfirm={() =>
                void act.run(async () => {
                  const j = await plainly(
                    post<AccountJob>("/v1/schools/accounts/issue", {
                      confirm: plan.hash,
                    }),
                  );
                  setPlan(undefined);
                  setJob(j.id);
                }, "Issuing account numbers")
              }
            />
          )}
        </section>
      )}
      <ErrorBox error={jobs.error ?? live.error} />
      {shown && <JobView job={shown} />}
      {jobs.data && jobs.data.jobs.length === 0 && !may.manage && <p className="hint">No account numbers have been issued in a batch yet.</p>}
    </>
  );
}

function PlanView({ plan, busy, onConfirm }: { plan: AccountPlan; busy: boolean; onConfirm: () => void }) {
  return (
    <div data-testid="accounts-plan" style={{ marginTop: 16 }}>
      <p>{planSummary(plan)}</p>
      <p className="hint">
        {plan.issuer.requirement} {feeLine(plan.issuer)}
      </p>
      {plan.running && <div className="notice">A batch is still issuing account numbers: wait for it to finish before starting another.</div>}
      {plan.cannot.length > 0 && (
        <>
          <h3>Cannot be issued yet</h3>
          <Items label="Cannot be issued yet">
            {plan.cannot.map((c) => (
              <Item key={c.student_id} to={`/schools/students/${c.student_id}`} icon="schools" tone="warn" title={c.admission_number} sub={c.reason} />
            ))}
          </Items>
        </>
      )}
      {plan.waiting.length > 0 && (
        <>
          <h3>Waiting</h3>
          <ul className="plain-list">
            {plan.waiting.map((w) => (
              <li key={w.student_id}>
                <Link to={`/schools/students/${w.student_id}`}>{w.admission_number}</Link>: {w.reason ?? "being issued now"}
              </li>
            ))}
          </ul>
        </>
      )}
      {plan.to_issue > 0 && !plan.running && (
        <button type="button" className="primary" onClick={onConfirm} disabled={busy}>
          Issue {plan.to_issue === 1 ? "1 account number" : `${plan.to_issue.toLocaleString("en-NG")} account numbers`}
        </button>
      )}
    </div>
  );
}

function JobView({ job }: { job: AccountJob }) {
  const pct = jobPercent(job);
  return (
    <section className="card" aria-labelledby="job-heading" data-testid="accounts-job">
      <h2 id="job-heading" style={{ marginTop: 0 }}>
        {job.status === "running" ? "Issuing now" : "The last batch"}
      </h2>
      <p role="status">{jobLine(job)}</p>
      <div className="progress" role="progressbar" aria-label="Account numbers issued" aria-valuemin={0} aria-valuemax={100} aria-valuenow={pct}>
        <span style={{ width: `${pct}%` }} />
      </div>
      <p className="hint">
        Started {fmtTime(job.created_at)} on {job.connection}
        {job.finished_at ? `; finished ${fmtTime(job.finished_at)}` : ""}.
      </p>
      {job.problems.length > 0 && (
        <>
          <h3>Not issued</h3>
          <Items label="Not issued">
            {job.problems.map((p) => (
              <Item
                key={p.student_id}
                testid="accounts-problem"
                to={`/schools/students/${p.student_id}`}
                icon="schools"
                tone="danger"
                title={p.admission_number}
                side={<span className={`badge sf-status ${accountStatus(p.status).tone}`}>{accountStatus(p.status).label}</span>}
                sub={`The rail's reason: ${p.reason}`}
              />
            ))}
          </Items>
          <p className="hint">Fix what the rail says (a guardian's email, the rail's own setup), then check again: the next batch tries them again.</p>
        </>
      )}
    </section>
  );
}
