import { useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { get, type RunSummary, type WorkflowSummary } from "../api";
import { useAuth } from "../auth";
import { useEnvironments } from "../environments";
import { Badge, EmptyState, ErrorBox, Item, Items, PageHeader, Skeleton, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { duration } from "../lib/timeline";
import { dayAfter, dayGroups, dayStart, filterRuns, runLook } from "../lib/lists";

const STATUSES = ["", "running", "waiting", "queued", "needs_reconciliation", "completed", "failed", "cancelled"];
const FILTERS = ["q", "status", "workflow", "env", "from", "to"];
/** A bare id says nothing to a person; who started a run shows only when it reads as a name or address. */
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export function Runs() {
  const { can } = useAuth();
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
      <PageHeader title="Activity" description="Every time a workflow runs: what started it, where it is now, and how long it took. The list refreshes itself." />
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
        <details className="more-filters" open={!!(workflow || env || from || to) || undefined}>
          <summary>More filters</summary>
          <div className="filters">
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
          </div>
        </details>
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
      {dayGroups(runs).map((g) => (
        <section className="panel day-group" key={g.label} aria-label={g.label}>
          <h2 className="day-head">{g.label}</h2>
          <Items>
            {g.runs.map((r) => {
              const look = runLook(r.status);
              return (
                <Item
                  key={r.id}
                  testid="run"
                  to={`/runs/${r.id}`}
                  icon={look.icon}
                  tone={look.tone}
                  title={r.workflow}
                  sub={[`v${r.version}`, r.environment, r.started_by && !UUID.test(r.started_by) ? r.started_by : ""].filter(Boolean).join(" · ")}
                  side={new Date(r.started_at).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" })}
                  sideSub={r.status === "completed" ? (r.ended_at ? duration(r.started_at, r.ended_at) : undefined) : <Badge value={r.status} />}
                />
              );
            })}
          </Items>
        </section>
      ))}
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
              }, false)
            }
          >
            Load older runs
          </button>
        </div>
      )}
    </>
  );
}
