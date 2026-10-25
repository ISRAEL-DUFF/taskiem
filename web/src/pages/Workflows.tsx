import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import type { WorkflowDefinition } from "@sdk/wd";
import { get, post, type WorkflowSummary } from "../api";
import { useAdvanced, useAuth } from "../auth";
import { EmptyState, ErrorBox, Field, Item, Items, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { AIBuilder } from "./AIBuild";
import { filterWorkflows } from "../lib/lists";
import { workflowID } from "../lib/aitables";
import { triggerWords } from "../lib/home";

/** A new workflow's first draft: a manual trigger and one step. */
export function starter(name: string): WorkflowDefinition {
  return {
    schema: "wd/v1",
    id: workflowID(name),
    version: 1,
    name,
    trigger: { type: "manual" },
    steps: [{ id: "start", type: "transform", config: { output: { received: "=trigger.body" } } }],
  };
}

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
  const [creating, setCreating] = useState(false);
  const [ai, setAI] = useState(false);
  const [name, setName] = useState("");
  const [goal, setGoal] = useState("");
  const act = useAction();
  const editable = can("workflow.edit");
  const shown = data ? filterWorkflows(data.workflows, q, state) : [];
  return (
    <>
      <PageHeader
        title="Workflows"
        description="The jobs Taskiem does for you, on its own: on a schedule, when an order comes in, or when you start them."
        actions={
          editable &&
          (advanced ? (
            <>
              <button onClick={() => setAI(true)}>Build with AI</button>
              <button className="primary" onClick={() => setCreating(true)}>
                <Icon name="plus" />
                New workflow
              </button>
            </>
          ) : (
            // The two ways to start (no-technical-steps N8); a blank canvas is under Advanced.
            <>
              <Link className="button" to="/templates">
                Start from a template
              </Link>
              <button className="primary" onClick={() => setAI(true)}>
                <Icon name="plus" />
                Describe it
              </button>
            </>
          ))
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
                {advanced ? (
                  <button className="primary" onClick={() => setCreating(true)}>
                    Build your first workflow
                  </button>
                ) : (
                  <button className="primary" onClick={() => setAI(true)}>
                    Describe what it should do
                  </button>
                )}
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
      {creating && (
        <Modal title="New workflow" onClose={() => setCreating(false)}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                const r = await post<{ id: string }>("/v1/workflows", { name, definition: starter(name) });
                nav(`/workflows/${r.id}`);
              }, false);
            }}
          >
            <Field label="Name">
              <input value={name} onChange={(e) => setName(e.target.value)} required autoFocus />
            </Field>
            <ErrorBox error={act.error} />
            <div className="toolbar">
              <button type="submit" className="primary" disabled={act.busy || !name}>
                Create
              </button>
              <button type="button" onClick={() => setCreating(false)}>
                Cancel
              </button>
            </div>
          </form>
        </Modal>
      )}
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
