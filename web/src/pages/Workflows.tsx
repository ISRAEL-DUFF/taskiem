import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { get, type WorkflowSummary } from "../api";
import { useAdvanced, useAuth } from "../auth";
import { EmptyState, ErrorBox, Item, Items, PageHeader, Skeleton, fmtTime, useLoad } from "../ui";
import { Icon } from "../icons";
import { AIBuilder } from "./AIBuild";
import { filterWorkflows } from "../lib/lists";
import { triggerWords } from "../lib/home";

export function Workflows() {
  const { can } = useAuth();
  const advanced = useAdvanced();
  const nav = useNavigate();
  const { data, error } = useLoad(() => get<{ workflows: WorkflowSummary[] }>(`/v1/workflows?tz=${encodeURIComponent(Intl.DateTimeFormat().resolvedOptions().timeZone)}`), []);
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const state = params.get("state") ?? "";
  const setParam = (k: string, v: string) => {
    const p = new URLSearchParams(params);
    if (v) p.set(k, v);
    else p.delete(k);
    setParams(p, { replace: true });
  };
  const [ai, setAI] = useState(false);
  const [goal, setGoal] = useState("");
  const editable = can("workflow.edit");
  const shown = data ? filterWorkflows(data.workflows, q, state) : [];
  return (
    <>
      <PageHeader
        title="Workflows"
        description="The jobs Taskiem does for you, on its own: on a schedule, when an order comes in, or when you start them."
        actions={
          editable && (
            // Three ways to start (docs/workflow-builder.md): choose what starts it and build, a template, or describe it.
            <>
              <Link className="button" to="/templates">
                Start from a template
              </Link>
              <Link className="button primary" to="/workflows/new">
                <Icon name="plus" />
                New workflow
              </Link>
            </>
          )
        }
      />
      <ErrorBox error={error} />
      {!data && !error && <Skeleton />}
      {data && data.workflows.length === 0 && (
        <EmptyState
          icon="workflows"
          action={
            editable ? (
              <>
                <Link className="button primary" to="/workflows/new">
                  Build your first workflow
                </Link>
                <button onClick={() => setAI(true)}>Describe what it should do</button>
                <Link className="button" to="/templates">
                  Start from a template
                </Link>
              </>
            ) : undefined
          }
        >
          Workflows are the automations your organisation runs. None exist yet.
        </EmptyState>
      )}
      {data && data.workflows.length > 0 && (
        <>
          {editable && (
            <form
              className="describe-bar"
              onSubmit={(e) => {
                e.preventDefault();
                setAI(true);
              }}
            >
              <Icon name="sparkle" />
              <input aria-label="Describe a new workflow" placeholder="Describe a job, e.g. every Friday, text customers who still owe me" value={goal} onChange={(e) => setGoal(e.target.value)} />
              <button type="submit" className="primary" disabled={!goal.trim()}>
                Draft it
              </button>
            </form>
          )}
          <div className="filters" role="search" aria-label="Filter workflows">
            <label className="search">
              <span className="sr-only">Search workflows</span>
              <Icon name="search" />
              <input type="search" placeholder="Search by name" value={q} onChange={(e) => setParam("q", e.target.value)} />
            </label>
            <label>
              <span className="sr-only">Published state</span>
              <select value={state} onChange={(e) => setParam("state", e.target.value)}>
                <option value="">Published and drafts</option>
                <option value="published">Published</option>
                <option value="draft">Drafts only</option>
              </select>
            </label>
            {(q || state) && (
              <span className="count" aria-live="polite">
                {shown.length} of {data.workflows.length}
              </span>
            )}
          </div>
          {shown.length === 0 ? (
            <EmptyState icon="search" action={<button onClick={() => setParams({}, { replace: true })}>Clear filters</button>}>
              No workflow matches these filters.
            </EmptyState>
          ) : (
            <Items label="Workflows">
              {shown.map((w) => (
                <Item
                  key={w.id}
                  testid="workflow"
                  to={`/workflows/${w.id}`}
                  icon={w.active_version ? "bolt" : "workflows"}
                  tone={w.active_version ? undefined : "muted"}
                  title={w.name}
                  sub={triggerWords(w.trigger)}
                  extra={
                    advanced && (
                      <span className="sub">
                        {w.active_version ? `v${w.active_version} live` : "Not published"} · latest v{w.latest_version} · made {fmtTime(w.created_at)}
                      </span>
                    )
                  }
                  side={w.active_version ? <RecentBars days={w.recent ?? []} /> : undefined}
                  sideSub={w.active_version ? <span className="badge published">Running</span> : <span className="badge draft">Draft</span>}
                />
              ))}
            </Items>
          )}
        </>
      )}
      {ai && <AIBuilder goal={goal} onClose={() => setAI(false)} onSaved={(id, v) => nav(`/workflows/${id}?v=${v}`)} />}
    </>
  );
}

/** Runs per day over the last two weeks, today last and darker. */
function RecentBars({ days }: { days: number[] }) {
  const top = Math.max(1, ...days);
  const total = days.reduce((a, b) => a + b, 0);
  return (
    <span className="bars" role="img" aria-label={total === 0 ? "No runs in the last 14 days" : `${total} runs in the last 14 days`}>
      {days.map((n, i) => (
        <span key={i} className={i === days.length - 1 ? "today" : ""} style={{ height: n === 0 ? 2 : 4 + Math.round((16 * n) / top) }} />
      ))}
    </span>
  );
}
