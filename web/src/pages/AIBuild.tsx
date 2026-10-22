import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Background, ReactFlow } from "@xyflow/react";
import type { WorkflowDefinition } from "@sdk/wd";
import { get, post, type Problem } from "../api";
import { StepNode, type StepFlowNode } from "../canvas/StepNode";
import { toGraph } from "../lib/graph";
import { Badge, ErrorBox, Field, Modal, useAction } from "../ui";
import { useAuth } from "../auth";
import {
  COLUMN_TYPES,
  tableProblem,
  tablesToSave,
  tableTroubles,
  type ProposedTable,
  type ReviewedTable,
  type TableResult,
} from "../lib/aitables";
import type { ReadyNeed, Readiness } from "../lib/readiness";
import { NeedsList } from "./Readiness";
import { ProblemLine } from "../forms/ProblemLine";

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
  /** Starting templates the prompt offered, and the one the draft started from. */
  templates_offered?: string[];
  template?: {
    id: string;
    title: string;
    instantiated: boolean;
    params: Record<string, unknown>;
    missing: string[];
  };
  usage: {
    input_tokens: number;
    output_tokens: number;
    cache_creation_tokens: number;
    cache_read_tokens: number;
  };
  model: string;
  /** New data tables the draft uses, made when it is saved. */
  tables?: ProposedTable[];
  /** What the draft needs before it can run (connections, settings, roles). */
  needs?: ReadyNeed[];
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

const stageText = (s: string) =>
  STAGES[s] ??
  (s.startsWith("correct")
    ? `Fixing problems (round ${s.split(" ")[1] ?? ""} of 3)`
    : s);

const nodeTypes = { step: StepNode };

/** The AI builder: for a new workflow, or to modify `workflow`. */
export function AIBuilder({
  workflow,
  goal: typed = "",
  onClose,
  onSaved,
}: {
  workflow?: string;
  /** What the person already typed (the Workflows page's describe bar). */
  goal?: string;
  onClose: () => void;
  onSaved: (id: string, version: number) => void;
}) {
  const [status, setStatus] = useState<AIStatus>();
  const [goal, setGoal] = useState(typed);
  const [name, setName] = useState("");
  const [build, setBuild] = useState<Build>();
  const [tables, setTables] = useState<ReviewedTable[]>([]);
  // Saved, with tables that could not be made: shown before moving on.
  const [saved, setSaved] = useState<{
    id: string;
    version: number;
    troubles: TableResult[];
  }>();
  const { can } = useAuth();
  const act = useAction();

  // The proposal's tables, to review: all ticked to be made.
  const proposal = build?.status === "proposed" ? build.proposal : null;
  useEffect(() => {
    setTables((proposal?.tables ?? []).map((t) => ({ ...t, create: true })));
  }, [proposal]);

  useEffect(() => {
    void get<AIStatus>("/v1/ai/status").then(setStatus, () =>
      setStatus({ enabled: false }),
    );
  }, []);

  // Poll a running build.
  useEffect(() => {
    if (build?.status !== "running") return;
    const t = setTimeout(() => {
      void get<Build>(`/v1/ai/builds/${build.id}`).then(
        setBuild,
        () => undefined,
      );
    }, 1500);
    return () => clearTimeout(t);
  }, [build]);

  const start = () =>
    act.run(async () => {
      const r = await post<{ id: string }>(
        "/v1/ai/build",
        workflow ? { goal, workflow } : { goal },
      );
      setBuild({
        id: r.id,
        status: "running",
        stage: "queued",
        error: null,
        proposal: null,
        saved_workflow_id: null,
        saved_version: null,
      });
    }, false);

  const save = () =>
    act.run(async () => {
      if (!build) return;
      const toMake = tablesToSave(tables);
      for (const t of toMake) {
        const problem = tableProblem(t);
        if (problem) throw new Error(problem);
      }
      const body: Record<string, unknown> = workflow
        ? {}
        : { name: name || undefined };
      if (toMake.length > 0) body.tables = toMake;
      const r = await post<{
        id: string;
        version: number;
        tables?: TableResult[];
      }>(`/v1/ai/builds/${build.id}/save`, body);
      const troubles = tableTroubles(r.tables);
      if (troubles.length > 0)
        setSaved({ id: r.id, version: r.version, troubles });
      else onSaved(r.id, r.version);
    }, "Saved as a draft");

  const p = build?.proposal;
  const budget = status?.budget;
  return (
    <Modal
      title={workflow ? "Change with AI" : "Build with AI"}
      onClose={onClose}
    >
      <div className="ai-builder">
        {status && !status.enabled && (
          <div className="notice">
            AI building is not set up on this installation. Build the workflow
            on the canvas instead.
          </div>
        )}
        {status?.enabled && !build && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void start();
            }}
          >
            <Field
              label={
                workflow
                  ? "What should change?"
                  : "What should the workflow do?"
              }
              hint="Say what starts it, what it should do, and who approves anything that moves money. Personal data is masked before it reaches the model; secrets never do."
            >
              <textarea
                rows={5}
                value={goal}
                maxLength={4000}
                onChange={(e) => setGoal(e.target.value)}
                required
                autoFocus
              />
            </Field>
            {budget && budget.monthly_tokens > 0 && (
              <p className="hint">
                This month's AI use:{" "}
                {Math.min(
                  100,
                  Math.round(
                    (budget.used_tokens / budget.monthly_tokens) * 100,
                  ),
                )}
                % of your plan's.
              </p>
            )}
            <ErrorBox error={act.error} />
            <div className="toolbar">
              <button
                type="submit"
                className="primary"
                disabled={act.busy || !goal.trim()}
              >
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
        {saved && (
          <>
            <div className="notice" role="status">
              The draft is saved, but not every table it needs could be made:
              <ul>
                {saved.troubles.map((t) => (
                  <li key={t.name}>
                    <strong>{t.name}</strong>: {t.error ?? t.status}
                  </li>
                ))}
              </ul>
              Create them on the <a href="/tables">Tables</a> page before
              publishing the workflow.
            </div>
            <div className="toolbar">
              <button
                className="primary"
                onClick={() => onSaved(saved.id, saved.version)}
              >
                Open the workflow
              </button>
            </div>
          </>
        )}
        {!saved && p && build && build.status !== "running" && (
          <Review proposal={p}>
            {build.status === "proposed" && p.definition && (
              <>
                <DraftNeeds proposal={p} />
                {tables.length > 0 && (
                  <TablesReview
                    tables={tables}
                    onChange={setTables}
                    canMake={can("table.edit")}
                  />
                )}
                {!workflow && (
                  <Field label="Name">
                    <input
                      value={name}
                      placeholder={p.definition.name}
                      onChange={(e) => setName(e.target.value)}
                    />
                  </Field>
                )}
                <ErrorBox error={act.error} />
                <div className="toolbar">
                  <button
                    className="primary"
                    onClick={() => void save()}
                    disabled={act.busy}
                  >
                    Save as draft
                  </button>
                  <button
                    onClick={() => setBuild(undefined)}
                    disabled={act.busy}
                  >
                    Start over
                  </button>
                </div>
                <p className="hint">
                  Saving creates a draft version with you as its author and the
                  AI as co-author. Publishing it goes through the usual checks
                  and approvals.
                </p>
              </>
            )}
          </Review>
        )}
      </div>
    </Modal>
  );
}

/**
 * What the draft needs, checked again after each fix: the proposal's
 * needs first, then the server's answer for the same definition.
 */
function DraftNeeds({ proposal: p }: { proposal: Proposal }) {
  const [tick, setTick] = useState(0);
  const [live, setLive] = useState<ReadyNeed[]>();
  useEffect(() => {
    if (tick === 0 || !p.definition) return;
    let on = true;
    void post<Readiness>("/v1/readiness", { definition: p.definition }).then(
      (r) => on && setLive(r.needs),
    );
    return () => {
      on = false;
    };
  }, [tick, p.definition]);
  return (
    <NeedsList
      needs={live ?? p.needs ?? []}
      onChanged={() => setTick((n) => n + 1)}
      tablesHere
    />
  );
}

function Review({
  proposal: p,
  children,
}: {
  proposal: Proposal;
  children: ReactNode;
}) {
  const graph = useMemo(
    () => (p.definition ? toGraph(p.definition) : { nodes: [], edges: [] }),
    [p.definition],
  );
  const nodes: StepFlowNode[] = graph.nodes.map((n) => ({
    id: n.id,
    type: "step",
    position: n.position,
    data: { step: n.step, invalid: false },
  }));
  const tokens =
    p.usage.input_tokens +
    p.usage.output_tokens +
    p.usage.cache_creation_tokens +
    p.usage.cache_read_tokens;
  return (
    <>
      {p.definition && (
        <div
          className="canvas ai-preview"
          role="group"
          aria-label="Proposed workflow"
        >
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
      {p.template && (
        <div className="notice" aria-label="Template">
          Started from the template{" "}
          <a
            href={`/templates?id=${encodeURIComponent(p.template.id)}`}
            target="_blank"
            rel="noreferrer"
          >
            {p.template.title}
          </a>
          {p.template.instantiated
            ? ", filled in from your goal."
            : ", adapted to your goal."}
          {p.template.missing.length > 0 && (
            <>
              {" "}
              Your goal did not say{" "}
              <strong>{p.template.missing.join(", ")}</strong>: the draft holds
              example values for them. Set them before publishing, or create the
              workflow from the template with the details filled in.
            </>
          )}
        </div>
      )}
      {p.summary && <p>{p.summary}</p>}
      {p.assumptions.length > 0 && (
        <>
          <h3>What the AI assumed</h3>
          <ul>
            {p.assumptions.map((a, i) => (
              <li key={i}>{a}</li>
            ))}
          </ul>
        </>
      )}
      {p.problems.length > 0 && (
        <div className="error">
          It would not publish yet ({p.problems.length} problem
          {p.problems.length > 1 ? "s" : ""}):
          <ul>
            {p.problems.map((x, i) => (
              <li key={i}>
                <ProblemLine p={x} />
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
                {plainWarning(w)}
                {plainWarning(w) !== w.message && (
                  <details className="hint">
                    <summary>Details</summary>
                    {w.message}
                  </details>
                )}
              </li>
            ))}
          </ul>
        </div>
      )}
      {p.results.length > 0 && (
        <>
          <p>
            {p.results.every((r) => r.passed)
              ? "Taskiem tried the draft with pretend data, and it worked."
              : "Taskiem tried the draft with pretend data, and something did not go as expected: check the steps before you publish."}
          </p>
          <details className="hint">
            <summary>The practice runs</summary>
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
                      {r.name}{" "}
                      {r.source === "generated" && (
                        <span className="hint">(generated)</span>
                      )}
                    </td>
                    <td>
                      <Badge value={r.passed ? "passed" : "failed"} />{" "}
                      {r.failures?.join("; ")}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </details>
        </>
      )}
      <details className="hint">
        <summary>Details</summary>
        {p.model} · {p.rounds} round{p.rounds === 1 ? "" : "s"} ·{" "}
        {tokens.toLocaleString()} tokens · connectors considered:{" "}
        {p.connectors.join(", ") || "none"}
      </details>
      {children}
    </>
  );
}

/** The new tables a draft needs: the person may rename, retype, add or drop
 * columns, or leave a table out, before they are made with the draft. */
function TablesReview({
  tables,
  onChange,
  canMake,
}: {
  tables: ReviewedTable[];
  onChange: (t: ReviewedTable[]) => void;
  canMake: boolean;
}) {
  const set = (i: number, t: ReviewedTable) =>
    onChange(tables.map((x, j) => (j === i ? t : x)));
  return (
    <section aria-label="New tables">
      <h3>New tables</h3>
      <p className="hint">
        The workflow keeps its records in{" "}
        {tables.length === 1
          ? "a table that does not exist yet"
          : "tables that do not exist yet"}
        . Check the columns: they are made when you save the draft.
        {!canMake &&
          " You cannot make tables yourself: an owner or admin can make them on the Tables page."}
      </p>
      {tables.map((t, i) => {
        const problem = t.create ? tableProblem(t) : "";
        return (
          <fieldset key={i} className="ai-table">
            <legend>
              <label className="inline">
                <input
                  type="checkbox"
                  checked={t.create}
                  disabled={!canMake}
                  onChange={(e) => set(i, { ...t, create: e.target.checked })}
                />{" "}
                Make the table <code>{t.name}</code>
              </label>
            </legend>
            <table>
              <thead>
                <tr>
                  <th scope="col">Column</th>
                  <th scope="col">Type</th>
                  <th scope="col">Required</th>
                  <th scope="col">
                    <span className="sr-only">Remove</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {t.columns.map((c, k) => {
                  const setCol = (v: Partial<typeof c>) =>
                    set(i, {
                      ...t,
                      columns: t.columns.map((x, m) =>
                        m === k ? { ...x, ...v } : x,
                      ),
                    });
                  return (
                    <tr key={k}>
                      <td>
                        <input
                          aria-label={`Column ${k + 1} name`}
                          value={c.name}
                          disabled={!t.create}
                          onChange={(e) =>
                            setCol({
                              name: e.target.value,
                              title:
                                !c.title || c.title === c.name
                                  ? e.target.value
                                  : c.title,
                            })
                          }
                        />
                      </td>
                      <td>
                        <select
                          aria-label={`Column ${k + 1} type`}
                          value={c.type}
                          disabled={!t.create}
                          onChange={(e) => setCol({ type: e.target.value })}
                        >
                          {COLUMN_TYPES.map((ty) => (
                            <option key={ty} value={ty}>
                              {ty}
                            </option>
                          ))}
                        </select>
                      </td>
                      <td>
                        <input
                          type="checkbox"
                          aria-label={`Column ${k + 1} required`}
                          checked={c.required}
                          disabled={!t.create}
                          onChange={(e) =>
                            setCol({ required: e.target.checked })
                          }
                        />
                      </td>
                      <td>
                        <button
                          type="button"
                          disabled={!t.create}
                          onClick={() =>
                            set(i, {
                              ...t,
                              columns: t.columns.filter((_, m) => m !== k),
                            })
                          }
                        >
                          Remove
                          <span className="sr-only"> column {c.name}</span>
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            <div className="toolbar">
              <button
                type="button"
                disabled={!t.create}
                onClick={() =>
                  set(i, {
                    ...t,
                    columns: [
                      ...t.columns,
                      { name: "", title: "", type: "text", required: false },
                    ],
                  })
                }
              >
                Add a column
              </button>
            </div>
            {problem && <p className="error-text">{problem}</p>}
          </fieldset>
        );
      })}
    </section>
  );
}

/** A review warning in plain words (no-technical-steps N5). */
export function plainWarning(w: Warning): string {
  switch (w.kind) {
    case "test":
      return "A practice run of the draft did not go as expected. Check the steps before you publish it.";
    case "policy":
      return `The draft does not follow one of your organisation's rules: ${w.message}`;
  }
  return w.message;
}
