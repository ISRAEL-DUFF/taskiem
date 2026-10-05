import { useState } from "react";
import { useNavigate } from "react-router-dom";
import type { WorkflowDefinition } from "@sdk/wd";
import { get, post, type WorkflowSummary } from "../api";
import { useAuth } from "../auth";
import { Badge, ErrorBox, Field, Modal, fmtTime, useAction, useLoad } from "../ui";

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
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const act = useAction();
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Workflows
        </h1>
        {can("workflow.edit") && (
          <button className="primary" onClick={() => setCreating(true)}>
            New workflow
          </button>
        )}
      </div>
      <ErrorBox error={error} />
      {data && data.workflows.length === 0 && <div className="empty card">No workflows yet.</div>}
      {data && data.workflows.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Published</th>
              <th>Latest</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {data.workflows.map((w) => (
              <tr key={w.id} className="clickable" onClick={() => nav(`/workflows/${w.id}`)}>
                <td>
                  <a href={`/workflows/${w.id}`} onClick={(e) => e.preventDefault()}>
                    {w.name}
                  </a>
                </td>
                <td>{w.active_version ? <Badge value="published" /> : <Badge value="draft" />} {w.active_version ? `v${w.active_version}` : ""}</td>
                <td>v{w.latest_version}</td>
                <td>{fmtTime(w.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
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
