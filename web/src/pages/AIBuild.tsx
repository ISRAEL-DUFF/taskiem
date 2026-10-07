import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Background, ReactFlow } from "@xyflow/react";
import type { WorkflowDefinition } from "@sdk/wd";
import { get, post, type Problem } from "../api";
import { StepNode, type StepFlowNode } from "../canvas/StepNode";
import { toGraph } from "../lib/graph";
import { Badge, ErrorBox, Field, Modal, useAction } from "../ui";

// "Build with AI" (docs/ai.md): describe a goal, the server drafts,
// validates, self-corrects and dry-runs a workflow, and the person reviews
// it here before saving it as a draft. Nothing is published from here.

interface AIStatus {
  enabled: boolean;
  provider?: string;
  model?: string;
  budget?: { monthly_tokens: number; used_tokens: number };
}

interface Warning {
  kind: string;
  rule?: string;
  path?: string;
  message: string;
}

interface TestResult {
  name: string;
  source: "generated" | "model";
  passed: boolean;
  status: string;
  failures?: string[];
}

interface Proposal {
  definition?: WorkflowDefinition;
  summary: string;
  assumptions: string[];
  problems: Problem[];
  warnings: Warning[];
  results: TestResult[];
  rounds: number;
  valid_first_try: boolean;
  connectors: string[];
  usage: { input_tokens: number; output_tokens: number; cache_creation_tokens: number; cache_read_tokens: number };
  model: string;
}

interface Build {
  id: string;
  status: "running" | "proposed" | "failed" | "saved";
  stage: string;
  error: string | null;
  proposal: Proposal | null;
  saved_workflow_id: string | null;
  saved_version: number | null;
}

const STAGES: Record<string, string> = {
  queued: "Starting",
  context: "Gathering connectors, connections and policies",
  draft: "Drafting",
  validate: "Checking the draft as if it were being published",
  dry_run: "Dry-running the draft with mocked connectors",
  done: "Done",
};

const stageText = (s: string) => STAGES[s] ?? (s.startsWith("correct") ? `Fixing problems (round ${s.split(" ")[1] ?? ""} of 3)` : s);

const nodeTypes = { step: StepNode };

/** The AI builder: for a new workflow, or to modify `workflow`. */
export function AIBuilder({ workflow, onClose, onSaved }: { workflow?: string; onClose: () => void; onSaved: (id: string, version: number) => void }) {
  const [status, setStatus] = useState<AIStatus>();
  const [goal, setGoal] = useState("");
  const [name, setName] = useState("");
  const [build, setBuild] = useState<Build>();
  const act = useAction();

  useEffect(() => {
    void get<AIStatus>("/v1/ai/status").then(setStatus, () => setStatus({ enabled: false }));
  }, []);

  // Poll a running build.
  useEffect(() => {
    if (build?.status !== "running") return;
    const t = setTimeout(() => {
      void get<Build>(`/v1/ai/builds/${build.id}`).then(setBuild, () => undefined);
    }, 1500);
    return () => clearTimeout(t);
  }, [build]);

  const start = () =>
    act.run(async () => {
      const r = await post<{ id: string }>("/v1/ai/build", workflow ? { goal, workflow } : { goal });
      setBuild({ id: r.id, status: "running", stage: "queued", error: null, proposal: null, saved_workflow_id: null, saved_version: null });
    });

  const save = () =>
    act.run(async () => {
      if (!build) return;
      const r = await post<{ id: string; version: number }>(`/v1/ai/builds/${build.id}/save`, workflow ? {} : { name: name || undefined });
      onSaved(r.id, r.version);
    });

  const p = build?.proposal;
  const budget = status?.budget;
  return (
    <Modal title={workflow ? "Change with AI" : "Build with AI"} onClose={onClose}>
      <div className="ai-builder">
        {status && !status.enabled && (
          <div className="notice">AI building is not set up on this installation. Build the workflow on the canvas instead.</div>
        )}
        {status?.enabled && !build && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void start();
            }}
          >
            <Field
              label={workflow ? "What should change?" : "What should the workflow do?"}
              hint="Say what starts it, what it should do, and who approves anything that moves money. Personal data is masked before it reaches the model; secrets never do."
            >
              <textarea rows={5} value={goal} maxLength={4000} onChange={(e) => setGoal(e.target.value)} required autoFocus />
            </Field>
            {budget && budget.monthly_tokens > 0 && (
              <p className="hint">
                {status.model} · this month: {budget.used_tokens.toLocaleString()} of {budget.monthly_tokens.toLocaleString()} tokens
              </p>
            )}
            <ErrorBox error={act.error} />
            <div className="toolbar">
              <button type="submit" className="primary" disabled={act.busy || !goal.trim()}>
                Draft it
              </button>
              <button type="button" onClick={onClose}>
                Cancel
              </button>
            </div>
          </form>
        )}
        {build?.status === "running" && (
          <div className="notice" role="status" aria-live="polite">
            {stageText(build.stage)}…
          </div>
        )}
        {build?.status === "failed" && (
          <>
            <div className="error" role="alert">
              {build.error ?? "The build failed."}
            </div>
            <div className="toolbar">
              <button onClick={() => setBuild(undefined)}>Try again</button>
            </div>
          </>
        )}
        {p && build && build.status !== "running" && (
          <Review proposal={p}>
            {build.status === "proposed" && p.definition && (
              <>
                {!workflow && (
                  <Field label="Name">
                    <input value={name} placeholder={p.definition.name} onChange={(e) => setName(e.target.value)} />
                  </Field>
                )}
                <ErrorBox error={act.error} />
                <div className="toolbar">
                  <button className="primary" onClick={() => void save()} disabled={act.busy}>
                    Save as draft
                  </button>
                  <button onClick={() => setBuild(undefined)} disabled={act.busy}>
                    Start over
                  </button>
                </div>
                <p className="hint">
                  Saving creates a draft version with you as its author and the AI as co-author. Publishing it goes through the usual checks and
                  approvals.
                </p>
              </>
            )}
          </Review>
        )}
      </div>
    </Modal>
  );
}

function Review({ proposal: p, children }: { proposal: Proposal; children: ReactNode }) {
  const graph = useMemo(() => (p.definition ? toGraph(p.definition) : { nodes: [], edges: [] }), [p.definition]);
  const nodes: StepFlowNode[] = graph.nodes.map((n) => ({ id: n.id, type: "step", position: n.position, data: { step: n.step, invalid: false } }));
  const tokens = p.usage.input_tokens + p.usage.output_tokens + p.usage.cache_creation_tokens + p.usage.cache_read_tokens;
  return (
    <>
      {p.definition && (
        <div className="canvas ai-preview" aria-label="Proposed workflow">
          <ReactFlow
            nodes={nodes}
            edges={graph.edges}
            nodeTypes={nodeTypes}
            nodesDraggable={false}
            nodesConnectable={false}
            elementsSelectable={false}
            fitView
            fitViewOptions={{ maxZoom: 1 }}
            proOptions={{ hideAttribution: true }}
          >
            <Background />
          </ReactFlow>
        </div>
      )}
      {p.summary && <p>{p.summary}</p>}
      {p.assumptions.length > 0 && (
        <>
          <h3>To confirm</h3>
          <ul>
            {p.assumptions.map((a, i) => (
              <li key={i}>{a}</li>
            ))}
          </ul>
        </>
      )}
      {p.problems.length > 0 && (
        <div className="error">
          It would not publish yet ({p.problems.length} problem{p.problems.length > 1 ? "s" : ""}):
          <ul>
            {p.problems.map((x, i) => (
              <li key={i}>
                <code>{x.path}</code> {x.message}
              </li>
            ))}
          </ul>
        </div>
      )}
      {p.warnings.length > 0 && (
        <div className="notice">
          <strong>Warnings</strong>
          <ul>
            {p.warnings.map((w, i) => (
              <li key={i}>
                <Badge value={w.kind} /> {w.message}
              </li>
            ))}
          </ul>
        </div>
      )}
      {p.results.length > 0 && (
        <>
          <h3>Dry run</h3>
          <table>
            <thead>
              <tr>
                <th>Test</th>
                <th>Result</th>
              </tr>
            </thead>
            <tbody>
              {p.results.map((r, i) => (
                <tr key={i}>
                  <td>
                    {r.name} {r.source === "generated" && <span className="hint">(generated)</span>}
                  </td>
                  <td>
                    <Badge value={r.passed ? "passed" : "failed"} /> {r.failures?.join("; ")}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </>
      )}
      <p className="hint">
        {p.model} · {p.rounds} round{p.rounds === 1 ? "" : "s"} · {tokens.toLocaleString()} tokens · connectors considered: {p.connectors.join(", ") || "none"}
      </p>
      {children}
    </>
  );
}
