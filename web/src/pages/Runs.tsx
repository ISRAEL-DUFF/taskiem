import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { get, type RunSummary, type WorkflowSummary } from "../api";
import { useAuth } from "../auth";
import { useEnvironments } from "../environments";
import { Badge, EmptyState, ErrorBox, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { duration } from "../lib/timeline";
import { dayAfter, dayStart, filterRuns } from "../lib/lists";

const STATUSES = ["", "running", "waiting", "queued", "needs_reconciliation", "completed", "failed", "cancelled"];
const FILTERS = ["q", "status", "workflow", "env", "from", "to"];

export function Runs() {
  const { can } = useAuth();
  const nav = useNavigate();
  const [params, setParams] = useSearchParams();
  const p = (k: string) => params.get(k) ?? "";
  const [q, status, workflow, env, from, to] = FILTERS.map(p) as [string, string, string, string, string, string];
  // Status, workflow, environment and the end of the range go to the API;
  // the search and the start of the range filter what it returned.
  const query = new URLSearchParams({ ...(status && { status }), ...(workflow && { workflow }), ...(env && { environment: env }), ...(to && { before: dayAfter(to) }), limit: "50" });
  const { data, error } = useLoad(() => get<{ runs: RunSummary[] }>(`/v1/runs?${query}`), [status, workflow, env, to], true);
  const workflows = useLoad(() => (can("workflow.read") ? get<{ workflows: WorkflowSummary[] }>("/v1/workflows") : Promise.resolve({ workflows: [] })), []);
  const envs = useEnvironments();
  const [more, setMore] = useState<RunSummary[]>([]);
  const act = useAction();
  const loaded = [...(data?.runs ?? []), ...more.filter((m) => !data?.runs.some((r) => r.id === m.id))];
  const runs = filterRuns(loaded, q, from);
  const last = loaded[loaded.length - 1];
  // Older pages can still hold matches until the oldest loaded run is before the range.
  const older = loaded.length >= 50 && !!last && (!from || new Date(last.started_at).getTime() >= dayStart(from));
  const filtered = FILTERS.some((k) => params.has(k));
  const set = (k: string, v: string) => {
    if (k !== "q") setMore([]);
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };
  return (
    <>
      <PageHeader title="Runs" description="Every time a workflow runs: what started it, where it is now, and how long it took. The list refreshes itself." />
      <div className="filters" role="search" aria-label="Filter runs">
        <label className="search">
          <span className="sr-only">Search runs</span>
          <Icon name="search" />
          <input type="search" placeholder="Search workflow, run ID or who started it" value={q} onChange={(e) => set("q", e.target.value)} />
        </label>
        <label>
          <select aria-label="Status" value={status} onChange={(e) => set("status", e.target.value)}>
            {STATUSES.map((s) => (
              <option key={s} value={s}>
                {s ? s.replaceAll("_", " ") : "All statuses"}
              </option>
            ))}
          </select>
        </label>
        {(workflows.data?.workflows.length ?? 0) > 0 && (
          <label>
            <span className="sr-only">Workflow</span>
            <select value={workflow} onChange={(e) => set("workflow", e.target.value)}>
              <option value="">All workflows</option>
              {workflows.data?.workflows.map((w) => (
                <option key={w.id} value={w.id}>
                  {w.name}
                </option>
              ))}
            </select>
          </label>
        )}
        <label>
          <span className="sr-only">Environment</span>
          <select value={env} onChange={(e) => set("env", e.target.value)}>
            <option value="">All environments</option>
            {(envs.data?.environments ?? []).map((x) => (
              <option key={x.name} value={x.name}>
                {x.name}
              </option>
            ))}
          </select>
        </label>
        <label>
          Started from
          <input type="date" value={from} max={to || undefined} onChange={(e) => set("from", e.target.value)} />
        </label>
        <label>
          to
          <input type="date" aria-label="Started up to" value={to} min={from || undefined} onChange={(e) => set("to", e.target.value)} />
        </label>
        {filtered && (
          <button type="button" onClick={() => (setMore([]), setParams({}, { replace: true }))}>
            Clear filters
          </button>
        )}
      </div>
      <ErrorBox error={error ?? act.error} />
      {!data && !error && <Skeleton rows={6} />}
      {data && runs.length === 0 && !older &&
        (filtered ? (
          <EmptyState icon="search" action={<button onClick={() => (setMore([]), setParams({}, { replace: true }))}>Clear filters</button>}>
            No run matches these filters.
          </EmptyState>
        ) : (
          <EmptyState
            icon="runs"
            action={
              can("workflow.read") ? (
                <Link className="button primary" to="/workflows">
                  Pick a workflow to start
                </Link>
              ) : undefined
            }
          >
            Each run of a published workflow shows here, with its status and timeline. Nothing has run yet.
          </EmptyState>
        ))}
      {runs.length > 0 && (
        <table className="cards">
          <thead>
            <tr>
              <th scope="col">Workflow</th>
              <th scope="col">Status</th>
              <th scope="col">Environment</th>
              <th scope="col">Started</th>
              <th scope="col">Duration</th>
              <th scope="col">Started by</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr key={r.id} className="clickable" onClick={() => nav(`/runs/${r.id}`)}>
                <td className="primary">
                  <Link to={`/runs/${r.id}`} onClick={(e) => e.stopPropagation()}>
                    {r.workflow}
                  </Link>{" "}
                  <span className="hint">v{r.version}</span>
                </td>
                <td data-label="Status">
                  <Badge value={r.status} />
                </td>
                <td data-label="Environment">{r.environment}</td>
                <td data-label="Started">{fmtTime(r.started_at)}</td>
                <td data-label="Duration">{r.ended_at ? duration(r.started_at, r.ended_at) : "—"}</td>
                <td data-label="Started by" className="hint">
                  {r.started_by ?? "—"}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {older && (
        <div className="toolbar" style={{ marginTop: 10 }}>
          <button
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                if (!last) return;
                const next = new URLSearchParams(query);
                next.set("before", last.started_at);
                const r = await get<{ runs: RunSummary[] }>(`/v1/runs?${next}`);
                setMore([...more, ...r.runs]);
              })
            }
          >
            Load older runs
          </button>
        </div>
      )}
    </>
  );
}
