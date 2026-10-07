import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";
import { get, post, type RunEvent, type RunSummary } from "../api";
import { useAuth } from "../auth";
import { RunCanvas } from "../canvas/RunCanvas";
import { duration, timeline, type StepRow } from "../lib/timeline";
import { Badge, ErrorBox, Field, Json, JsonInput, Modal, fmtTime, useAction, useLoad } from "../ui";

interface RunDoc {
  run: RunSummary;
  events: RunEvent[];
  revealed: boolean;
}
const TERMINAL = new Set(["completed", "failed", "cancelled"]);

/** A recorded decryption of a secret or connection credential (GET /v1/secrets/reads). */
interface SecretRead {
  id: string;
  at: string;
  kind: string;
  environment: string;
  name: string;
  connector?: string;
  purpose: string;
  step_id?: string;
  attempt?: number;
}

export function RunPage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const [reveal, setReveal] = useState(false);
  const [live, setLive] = useState(true);
  // Revealed history is fetched (each fetch is audited); sealed history
  // streams as it is recorded (spec 15.1, live view).
  const { data, error, reload } = useLoad(() => get<RunDoc>(`/v1/runs/${id}${reveal ? "?reveal=true" : ""}`), [id, reveal], live && reveal);
  const [streamed, setStreamed] = useState<RunEvent[]>([]);
  const [view, setView] = useState<"canvas" | "timeline">("canvas");
  const [resolving, setResolving] = useState<string | null>(null);
  const act = useAction();
  const status = data?.run.status;
  // Which secrets and connections each step decrypted (audit.read only).
  const reads = useLoad(
    () => (can("audit.read") ? get<{ reads: SecretRead[] }>(`/v1/secrets/reads?run=${id}&limit=1000`) : Promise.resolve({ reads: [] as SecretRead[] })),
    [id, status],
  );
  const readsByStep = useMemo(() => {
    const m = new Map<string, SecretRead[]>();
    for (const r of reads.data?.reads ?? []) {
      if (!r.step_id) continue;
      m.set(r.step_id, [...(m.get(r.step_id) ?? []), r]);
    }
    return m;
  }, [reads.data]);
  useEffect(() => {
    if (status && TERMINAL.has(status)) setLive(false);
  }, [status]);
  useEffect(() => {
    setStreamed([]);
    if (!data || reveal || TERMINAL.has(data.run.status) || typeof EventSource === "undefined") return;
    const es = new EventSource(`/v1/runs/${id}/stream?after=${data.events.at(-1)?.seq ?? 0}`);
    es.addEventListener("run_event", (m) => setStreamed((prev) => [...prev, JSON.parse((m as MessageEvent<string>).data) as RunEvent]));
    es.addEventListener("end", () => {
      es.close();
      reload();
    });
    return () => es.close();
  }, [data, id, reveal, reload]);
  const events = useMemo(() => {
    if (!data) return [];
    const seen = new Set(data.events.map((e) => e.seq));
    return [...data.events, ...streamed.filter((e) => !seen.has(e.seq))];
  }, [data, streamed]);
  if (error) return <ErrorBox error={error} />;
  if (!data) return <div className="empty">Loading…</div>;
  const r = data.run;
  const rows = timeline(events);
  const ended = events.find((e) => ["RunCompleted", "RunFailed", "RunCancelled"].includes(e.type));
  const ENDED: Record<string, string> = { RunCompleted: "completed", RunFailed: "failed", RunCancelled: "cancelled" };
  const shownStatus = (ended && ENDED[ended.type]) || r.status;
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          <Link to={`/workflows/${r.workflow_id}?v=${r.version}`}>{r.workflow}</Link> <span className="hint">run</span>
        </h1>
        <Badge value={shownStatus} />
        {!reveal && !TERMINAL.has(shownStatus) && <span className="hint live-dot">live</span>}
        {can("pii.reveal") && (
          <label className="inline" title="Decrypt personal data for this view; the access is audited">
            <input type="checkbox" checked={reveal} onChange={(e) => setReveal(e.target.checked)} /> Reveal personal data
          </label>
        )}
        {can("run.cancel") && !TERMINAL.has(shownStatus) && (
          <button
            className="danger"
            disabled={act.busy}
            onClick={() => confirm("Cancel this run? Steps already sent to a provider still record their results.") && void act.run(async () => (await post(`/v1/runs/${id}/cancel`), reload()))}
          >
            Cancel run
          </button>
        )}
      </div>
      <ErrorBox error={act.error} />
      <div className="card">
        <dl className="kv">
          <dt>Run</dt>
          <dd>
            <code>{r.id}</code>
          </dd>
          <dt>Version</dt>
          <dd>v{r.version}</dd>
          <dt>Environment</dt>
          <dd>{r.environment}</dd>
          <dt>Started</dt>
          <dd>
            {fmtTime(r.started_at)} by {r.started_by ?? "—"}
          </dd>
          <dt>Ended</dt>
          <dd>{r.ended_at ? `${fmtTime(r.ended_at)} (${duration(r.started_at, r.ended_at)})` : "—"}</dd>
        </dl>
        {r.status === "needs_reconciliation" && (
          <div className="notice">
            A step's outcome is unknown and cannot be safely retried. Check with the provider, then resolve the parked step below.
          </div>
        )}
      </div>
      <div className="toolbar">
        <h2 className="grow">Steps</h2>
        <div className="tabs" role="tablist">
          <button role="tab" className={view === "canvas" ? "active" : ""} onClick={() => setView("canvas")}>
            Canvas
          </button>
          <button role="tab" className={view === "timeline" ? "active" : ""} onClick={() => setView("timeline")}>
            Timeline
          </button>
        </div>
      </div>
      {view === "canvas" && <RunCanvas workflow={r.workflow_id} version={r.version} rows={rows} ended={TERMINAL.has(shownStatus)} onSelect={(step) => (setView("timeline"), setTimeout(() => document.querySelector(`[data-testid="step-${CSS.escape(step)}"]`)?.scrollIntoView({ block: "center" }), 0))} />}
      <div className="timeline" hidden={view !== "timeline"}>
        {rows.length === 0 && <div className="empty">No steps yet.</div>}
        {rows.map((s) => (
          <StepCard key={s.id} row={s} reads={readsByStep.get(s.id) ?? []} canResolve={can("run.resolve") && s.status === "parked"} onResolve={() => setResolving(s.id)} />
        ))}
      </div>
      {ended && ended.type === "RunFailed" && (
        <>
          <h2>Failure</h2>
          <Json value={ended.payload} />
        </>
      )}
      <details style={{ marginTop: 16 }}>
        <summary>All {events.length} events</summary>
        <table>
          <thead>
            <tr>
              <th>#</th>
              <th>Event</th>
              <th>Step</th>
              <th>Origin</th>
              <th>At</th>
            </tr>
          </thead>
          <tbody>
            {events.map((e) => (
              <tr key={e.seq}>
                <td>{e.seq}</td>
                <td>
                  {e.type}
                  {e.payload !== undefined && (
                    <details>
                      <summary>payload</summary>
                      <Json value={e.payload} />
                    </details>
                  )}
                </td>
                <td>
                  {e.step_id}
                  {e.attempt ? ` #${e.attempt}` : ""}
                </td>
                <td>{e.origin}</td>
                <td>{fmtTime(e.recorded_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </details>
      {resolving && <Resolve run={id} step={resolving} onClose={() => setResolving(null)} onDone={() => (setResolving(null), setLive(true), reload())} />}
    </>
  );
}

function StepCard({ row, reads, canResolve, onResolve }: { row: StepRow; reads: SecretRead[]; canResolve: boolean; onResolve: () => void }) {
  return (
    <div className={`step ${row.status}`} data-testid={`step-${row.id}`}>
      <div className="toolbar" style={{ marginBottom: 2 }}>
        <strong>{row.id}</strong>
        <Badge value={row.status} />
        {row.attempts > 1 && <span className="hint">{row.attempts} attempts</span>}
        <span className="hint">{duration(row.firstAt, row.lastAt)}</span>
        {row.waitingFor && row.status === "waiting" && <span className="hint">waiting for {row.waitingFor}</span>}
        {canResolve && (
          <button className="primary" onClick={onResolve}>
            Resolve
          </button>
        )}
      </div>
      {row.error && (
        <div className="error" style={{ margin: "4px 0" }}>
          {row.error.kind}: {row.error.message}
        </div>
      )}
      {row.input !== undefined && (
        <details>
          <summary>input</summary>
          <Json value={row.input} />
        </details>
      )}
      {row.output !== undefined && (
        <details open={row.status === "completed" && JSON.stringify(row.output).length < 400}>
          <summary>output</summary>
          <Json value={row.output} />
        </details>
      )}
      {Array.isArray(row.logs) && row.logs.length > 0 && (
        <details>
          <summary>logs</summary>
          <Json value={row.logs} />
        </details>
      )}
      {reads.length > 0 && (
        <details>
          <summary>secrets used ({reads.length})</summary>
          <table>
            <tbody>
              {reads.map((r) => (
                <tr key={r.id}>
                  <td>
                    <code>{r.connector ? `${r.connector}/${r.name}` : r.name}</code> <span className="hint">{r.kind}</span>
                  </td>
                  <td className="hint">attempt {r.attempt}</td>
                  <td className="hint">{fmtTime(r.at)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </details>
      )}
    </div>
  );
}

function Resolve({ run, step, onClose, onDone }: { run: string; step: string; onClose: () => void; onDone: () => void }) {
  const [resolution, setResolution] = useState("completed");
  const [note, setNote] = useState("");
  const [output, setOutput] = useState<unknown>({});
  const act = useAction();
  return (
    <Modal title={`Resolve ${step}`} onClose={onClose}>
      <Field label="What the provider shows">
        <select value={resolution} onChange={(e) => setResolution(e.target.value)}>
          <option value="completed">It happened: record it as completed</option>
          <option value="retry">It did not happen: send it again</option>
          <option value="failed">It did not happen: fail the step</option>
        </select>
      </Field>
      {resolution === "completed" && (
        <Field label="Output to record (JSON)">
          <JsonInput value={output} onChange={setOutput} rows={5} />
        </Field>
      )}
      <Field label="Note (required, audited)" hint="e.g. Paystack dashboard shows TRF_x successful at 10:42">
        <textarea rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          className="primary"
          disabled={act.busy || !note}
          onClick={() => void act.run(async () => (await post(`/v1/runs/${run}/steps/${encodeURIComponent(step)}/resolve`, { resolution, note, ...(resolution === "completed" && { output }) }), onDone()))}
        >
          Resolve
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
