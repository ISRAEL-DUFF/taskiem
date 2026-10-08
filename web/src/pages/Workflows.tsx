import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import type { WorkflowDefinition } from "@sdk/wd";
import { get, post, type WorkflowSummary } from "../api";
import { useAuth } from "../auth";
import { Badge, EmptyState, ErrorBox, Field, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { AIBuilder } from "./AIBuild";
import { filterWorkflows } from "../lib/lists";

/** A new workflow's first draft: a manual trigger and one step. */
export function starter(name: string): WorkflowDefinition {
  const slug = name.replace(/[^0-9A-Za-z]/g, "").slice(0, 40) || "workflow";
  return {
    schema: "wd/v1",
    id: `wf_${slug}`,
    version: 1,
    name,
    trigger: { type: "manual" },
    steps: [{ id: "start", type: "transform", config: { output: { received: "=trigger.body" } } }],
  };
}

export function Workflows() {
  const { can } = useAuth();
  const nav = useNavigate();
  const { data, error } = useLoad(() => get<{ workflows: WorkflowSummary[] }>("/v1/workflows"), []);
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
  const act = useAction();
  const editable = can("workflow.edit");
  const shown = data ? filterWorkflows(data.workflows, q, state) : [];
  return (
    <>
      <PageHeader
        title="Workflows"
        description="The automations your organisation runs: draft them on the canvas, publish a version, then start runs by hand, on a schedule or from an event."
        actions={
          editable && (
            <>
              <button onClick={() => setAI(true)}>Build with AI</button>
              <button className="primary" onClick={() => setCreating(true)}>
                <Icon name="plus" />
                New workflow
              </button>
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
                <button className="primary" onClick={() => setCreating(true)}>
                  Build your first workflow
                </button>
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
            <table className="cards">
              <thead>
                <tr>
                  <th scope="col">Name</th>
                  <th scope="col">Published</th>
                  <th scope="col">Latest</th>
                  <th scope="col">Created</th>
                </tr>
              </thead>
              <tbody>
                {shown.map((w) => (
                  <tr key={w.id} className="clickable" onClick={() => nav(`/workflows/${w.id}`)}>
                    <td className="primary">
                      <Link to={`/workflows/${w.id}`} onClick={(e) => e.stopPropagation()}>
                        {w.name}
                      </Link>
                    </td>
                    <td data-label="Published">
                      {w.active_version ? <Badge value="published" /> : <Badge value="draft" />} {w.active_version ? `v${w.active_version}` : ""}
                    </td>
                    <td data-label="Latest">v{w.latest_version}</td>
                    <td data-label="Created">{fmtTime(w.created_at)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </>
      )}
      {ai && <AIBuilder onClose={() => setAI(false)} onSaved={(id, v) => nav(`/workflows/${id}?v=${v}`)} />}
      {creating && (
        <Modal title="New workflow" onClose={() => setCreating(false)}>
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                const r = await post<{ id: string }>("/v1/workflows", { name, definition: starter(name) });
                nav(`/workflows/${r.id}`);
              });
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
