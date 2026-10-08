// The "Proposed fix" panel on a run's page (spec 12.2, docs/ai.md): what
// the repair pipeline made of a failed run. A patch shows its diff and the
// shadow run's evidence with Accept (publish and resume from the failed
// step) and Dismiss; a transient or credential failure shows its simple
// action; an uncertain write shows the reconcile action's answer for a
// person to resolve the parked step. Accepting is the person's own request,
// under their permissions; four-eyes publishing and promotion still apply.
import { Link } from "react-router-dom";
import { get, post } from "../api";
import { useAuth } from "../auth";
import { Badge, ErrorBox, Json, fmtTime, useAction, useLoad } from "../ui";

interface TestOutcome {
  name: string;
  passed: boolean;
  status: string;
  failures?: string[];
}

interface Evidence {
  classification?: { class: string; certain: boolean; why: string };
  shadow?: { passed: boolean; status: string; steps: Record<string, string>; replayed: string[]; failures?: string[]; input_problems?: string[]; write_mismatches?: string[] };
  resumable?: boolean;
  regression?: TestOutcome;
  reproduces_failure?: boolean;
  model_test?: TestOutcome | null;
  existing_tests?: TestOutcome[];
  attempts?: number;
  feedback?: string[];
}

interface Repair {
  id: string;
  run_id: string;
  workflow_id: string;
  version: number;
  environment: string;
  reason: string;
  class: string | null;
  classified_by: string | null;
  step: string | null;
  status: string;
  explanation: string;
  action: { kind: string; label: string; step?: string; connector?: string; connection?: string; link?: string; reconcile?: { available: boolean; note?: string; result?: unknown } } | null;
  definition: unknown;
  diff: string[] | null;
  evidence: Evidence | null;
  attempts: number;
  model: string | null;
  total_tokens: number;
  error: string | null;
  created_at: string;
  draft_version: number | null;
  accepted_by: string | null;
  resumed_run_id: string | null;
}

const CLASS: Record<string, string> = {
  transient: "Temporary provider failure",
  credential: "Credentials",
  data: "Unexpected data",
  schema_drift: "Provider response changed",
  logic: "Workflow logic",
  unknown_outcome: "Uncertain outcome",
};

export function RepairPanel({ run, status }: { run: string; status: string }) {
  const { can } = useAuth();
  const act = useAction();
  const relevant = ["failed", "needs_reconciliation", "completed"].includes(status);
  const { data, reload } = useLoad(
    () => (relevant ? get<{ repairs: Repair[] }>(`/v1/runs/${run}/repairs`) : Promise.resolve({ repairs: [] as Repair[] })),
    [run, relevant],
    relevant,
  );
  const r = data?.repairs.find((x) => x.status !== "dismissed");
  if (!r) return null;
  const ev = r.evidence ?? {};
  const decide = (path: string) => void act.run(async () => (await post(`/v1/repairs/${r.id}/${path}`), reload()));
  const canAccept = can("run.resolve") && (r.status !== "proposed" || can("workflow.publish"));
  return (
    <div className="card" data-testid="repair-panel">
      <div className="toolbar" style={{ marginBottom: 4 }}>
        <h2 className="grow" style={{ margin: 0 }}>
          Proposed fix
        </h2>
        {r.class && <Badge value={CLASS[r.class] ?? r.class} />}
        <Badge value={r.status.replace(/_/g, " ")} />
      </div>
      {r.status === "analysing" && <p className="hint">Analysing the failure…</p>}
      {r.explanation && <p>{r.explanation}</p>}
      <p className="hint">
        {r.step && (
          <>
            Failed step <code>{r.step}</code> ·{" "}
          </>
        )}
        classified by {r.classified_by === "model" ? "the AI" : "rules"}
        {r.attempts > 0 && ` · ${r.attempts} AI attempt${r.attempts > 1 ? "s" : ""}, ${r.total_tokens.toLocaleString()} tokens`} · {fmtTime(r.created_at)}
      </p>
      {r.error && <div className="notice">{r.error}</div>}

      {r.diff && r.diff.length > 0 && (
        <>
          <h3>Change to the workflow</h3>
          <ul>
            {r.diff.map((d) => (
              <li key={d}>
                <code>{d}</code>
              </li>
            ))}
          </ul>
          <details>
            <summary>Patched definition</summary>
            <Json value={r.definition} />
          </details>
        </>
      )}

      {ev.shadow && (
        <>
          <h3>Evidence</h3>
          <ul className="evidence">
            <li>
              Shadow run of this run's recorded data (every write mocked): <Badge value={ev.shadow.passed ? "passed" : "failed"} />{" "}
              <span className="hint">
                {ev.shadow.replayed.length > 0 && `replayed ${ev.shadow.replayed.join(", ")}`}
              </span>
            </li>
            {ev.regression && (
              <li>
                Test for the failing case: <Badge value={ev.regression.passed ? "passed" : "failed"} />{" "}
                {ev.reproduces_failure && <span className="hint">fails on v{r.version}, as the run did</span>}
              </li>
            )}
            {ev.model_test && (
              <li>
                The AI's test: <Badge value={ev.model_test.passed ? "passed" : "failed"} />
              </li>
            )}
            <li>
              Existing tests: {ev.existing_tests?.length ? `${ev.existing_tests.filter((t) => t.passed).length} of ${ev.existing_tests.length} pass` : "none yet"}
            </li>
            <li>
              Resume from the failed step: {ev.resumable ? "safe: completed steps are replayed, not sent again" : "not possible: a completed write would change; publish only"}
            </li>
          </ul>
          {!!ev.feedback?.length && (
            <details>
              <summary>Why the last attempt did not pass</summary>
              <ul>
                {ev.feedback.map((f, i) => (
                  <li key={i}>{f}</li>
                ))}
              </ul>
            </details>
          )}
        </>
      )}

      {r.status === "withheld" && <div className="notice">The AI could not prove a fix in the shadow sandbox, so nothing is proposed. Fix the workflow by hand.</div>}

      {r.action?.kind === "reconnect" && (
        <p>
          Reconnect <code>{r.action.connection || r.action.connector}</code> in {r.environment}: <Link to={r.action.link ?? "/connections"}>Connections</Link>
        </p>
      )}
      {r.action?.kind === "reconcile" && (
        <>
          <h3>What the provider says</h3>
          {r.action.reconcile?.available ? <Json value={r.action.reconcile.result} /> : <p className="hint">{r.action.reconcile?.note}</p>}
          <p className="hint">Check this against the provider, then resolve the parked step below. The AI never resolves it.</p>
        </>
      )}
      {r.status === "awaiting_publish" && (
        <div className="notice">
          Version {r.draft_version} waits for a second person to approve publishing it (<Link to="/approvals">publish requests</Link>). The run resumes once it is published.
        </div>
      )}
      {r.status === "awaiting_promotion" && <div className="notice">Version {r.draft_version} is published but not yet running in {r.environment}. Promote it, then resume.</div>}
      {r.resumed_run_id && (
        <p>
          Resumed as <Link to={`/runs/${r.resumed_run_id}`}>a new run</Link>
          {r.draft_version ? ` on version ${r.draft_version}` : ""}.
        </p>
      )}

      <ErrorBox error={act.error} />
      <div className="toolbar">
        {r.status === "proposed" && canAccept && (
          <button className="primary" disabled={act.busy} onClick={() => confirm(`Publish the patched version and resume this run from ${r.step ?? "the failed step"}? Publishing follows your organisation's approval rules.`) && decide("accept")}>
            Accept: publish and resume
          </button>
        )}
        {r.status === "action" && (r.action?.kind === "retry" || r.action?.kind === "reconnect") && canAccept && status === "failed" && (
          <button className="primary" disabled={act.busy} onClick={() => decide("accept")}>
            Retry from the failed step
          </button>
        )}
        {r.status === "awaiting_promotion" && canAccept && (
          <button disabled={act.busy} onClick={() => decide("accept")}>
            Resume now
          </button>
        )}
        {can("run.resolve") && ["proposed", "action", "withheld", "awaiting_publish", "awaiting_promotion"].includes(r.status) && (
          <button disabled={act.busy} onClick={() => decide("dismiss")}>
            Dismiss
          </button>
        )}
      </div>
    </div>
  );
}
