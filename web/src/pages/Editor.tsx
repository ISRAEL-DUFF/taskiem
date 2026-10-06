import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router-dom";
import { Background, Controls, ReactFlow, useReactFlow, type Connection, type EdgeChange, type NodeChange } from "@xyflow/react";
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { ApiError, get, post, put, type ConnectorInfo, type MergeConflict, type Problem, type TriggerInfo, type VersionInfo, type WorkflowSummary } from "../api";
import { useAuth } from "../auth";
import { StepNode, type StepFlowNode } from "../canvas/StepNode";
import { StepPanel } from "../canvas/StepPanel";
import { connect, disconnect, freshId, newStep, removeStep, renameStep, toGraph, type Layout } from "../lib/graph";
import { merge } from "../lib/schema";
import { Badge, ErrorBox, Field, JsonInput, Modal, fmtTime, useAction, useLoad } from "../ui";

const nodeTypes = { step: StepNode };
const PALETTE: Step["type"][] = ["connector", "http", "code", "transform", "approval", "signal", "wait", "branch", "parallel", "foreach"];

interface Loaded {
  workflow: WorkflowSummary;
  versions: VersionInfo[];
}
interface VersionDoc {
  version: VersionInfo;
  definition: WorkflowDefinition;
  layout?: Layout;
  problems: Problem[];
}

/** Maps validator paths (/steps/2/..., /steps/<id>) to top-level step ids. */
function problemSteps(def: WorkflowDefinition, problems: Problem[]): Set<string> {
  const out = new Set<string>();
  for (const p of problems) {
    const seg = p.path.split("/")[2];
    if (seg === undefined) continue;
    const idx = Number(seg);
    const id = Number.isInteger(idx) ? def.steps[idx]?.id : seg;
    if (id) out.add(id);
  }
  return out;
}

export function Editor() {
  const { id = "" } = useParams();
  const [params, setParams] = useSearchParams();
  const nav = useNavigate();
  const { can } = useAuth();
  const wf = useLoad(() => get<Loaded>(`/v1/workflows/${id}`), [id]);
  const connectors = useLoad(() => get<{ connectors: ConnectorInfo[] }>("/v1/connectors"), []);
  const version = Number(params.get("v")) || wf.data?.workflow.latest_version || 0;
  const doc = useLoad(() => (version ? get<VersionDoc>(`/v1/workflows/${id}/versions/${version}`) : Promise.resolve(undefined)), [id, version]);

  const [def, setDef] = useState<WorkflowDefinition>();
  const [layout, setLayout] = useState<Layout>({});
  const [problems, setProblems] = useState<Problem[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [tab, setTab] = useState<"canvas" | "settings" | "code" | "json" | "triggers">("canvas");
  const [notice, setNotice] = useState("");
  const [starting, setStarting] = useState(false);
  const [merge, setMerge] = useState<{ conflicts: MergeConflict[]; latest: number }>();
  const act = useAction();

  useEffect(() => {
    if (!doc.data) return;
    setDef(doc.data.definition);
    setLayout(doc.data.layout ?? {});
    setProblems(doc.data.problems);
    setDirty(false);
  }, [doc.data]);

  const edit = useCallback((next: WorkflowDefinition) => {
    setDef(next);
    setDirty(true);
  }, []);
  const editSteps = (steps: Step[]) => def && edit({ ...def, steps });

  // Layout changes are saved on the current version without a new version.
  const layoutTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const saveLayout = (l: Layout) => {
    clearTimeout(layoutTimer.current);
    layoutTimer.current = setTimeout(() => {
      if (version && can("workflow.edit")) void put(`/v1/workflows/${id}/versions/${version}/layout`, l).catch(() => undefined);
    }, 600);
  };

  const invalid = useMemo(() => (def ? problemSteps(def, problems) : new Set<string>()), [def, problems]);
  const graph = useMemo(() => (def ? toGraph(def, layout) : { nodes: [], edges: [] }), [def, layout]);
  const nodes: StepFlowNode[] = graph.nodes.map((n) => ({
    id: n.id,
    type: "step",
    position: n.position,
    selected: n.id === selected,
    data: { step: n.step, invalid: invalid.has(n.id) },
  }));

  const onNodesChange = (changes: NodeChange<StepFlowNode>[]) => {
    let l = layout;
    let steps = def?.steps;
    for (const c of changes) {
      if (c.type === "position" && c.position) {
        l = { ...l, [c.id]: c.position };
        if (!c.dragging) saveLayout(l);
      } else if (c.type === "select") {
        if (c.selected) setSelected(c.id);
        else setSelected((s) => (s === c.id ? null : s));
      } else if (c.type === "remove" && steps) {
        steps = removeStep(steps, c.id);
      }
    }
    if (l !== layout) setLayout(l);
    if (steps && steps !== def?.steps) editSteps(steps);
  };
  const onEdgesChange = (changes: EdgeChange[]) => {
    let steps = def?.steps;
    for (const c of changes) {
      if (c.type === "remove" && steps) {
        const [source, target] = c.id.split("->");
        if (source && target) steps = disconnect(steps, source, target);
      }
    }
    if (steps && steps !== def?.steps) editSteps(steps);
  };
  const onConnect = (c: Connection) => {
    if (!def || !c.source || !c.target) return;
    const next = connect(def.steps, c.source, c.target);
    if (!next) {
      setNotice("That connection would make a cycle.");
      return;
    }
    editSteps(next);
  };

  const addStep = (type: Step["type"]) => {
    if (!def) return;
    const sid = freshId(def.steps, type === "connector" ? "call" : type);
    const last = selected ?? def.steps[def.steps.length - 1]?.id;
    const s = newStep(type, sid);
    if (last) s.needs = [last];
    const pos = last && layout[last] ? { x: (layout[last]?.x ?? 0) + 280, y: layout[last]?.y ?? 0 } : undefined;
    if (pos) setLayout({ ...layout, [sid]: pos });
    edit({ ...def, steps: [...def.steps, s] });
    setSelected(sid);
  };

  // A save names the version it started from; if someone saved since, the
  // server merges the two edits, or reports conflicts to resolve.
  const save = async (resolutions?: Record<string, "ours" | "theirs">): Promise<number> => {
    if (!def) return version;
    if (!dirty) return version;
    let r: { version: number; problems: Problem[]; merged: boolean };
    try {
      r = await post(`/v1/workflows/${id}/versions`, { definition: def, layout, parent_digest: doc.data?.version.digest, resolutions });
    } catch (e) {
      if (e instanceof ApiError && e.status === 409 && Array.isArray(e.body.conflicts)) {
        setMerge({ conflicts: e.body.conflicts as MergeConflict[], latest: Number(e.body.latest_version) });
        throw new Error(`Version ${String(e.body.latest_version)} was saved while you were editing, and changes the same parts. Choose which to keep.`, { cause: e });
      }
      throw e;
    }
    setMerge(undefined);
    if (r.merged) setNotice(`Saved as version ${r.version}, merged with changes saved since you started.`);
    setProblems(r.problems);
    setDirty(false);
    setParams({ v: String(r.version) }, { replace: true });
    wf.reload();
    return r.version;
  };

  const validate = () =>
    act.run(async () => {
      const r = await post<{ valid: boolean; problems: Problem[] }>("/v1/validate", { definition: def });
      setProblems(r.problems);
      setNotice(r.valid ? "Valid: this version can be published." : "");
    });

  const publish = () =>
    act.run(async () => {
      const v = await save();
      try {
        await post(`/v1/workflows/${id}/versions/${v}/publish`);
      } catch (e) {
        if (e instanceof ApiError && e.status === 422) setProblems(e.problems as Problem[]);
        throw e;
      }
      setNotice(`Version ${v} is published; new runs use it.`);
      wf.reload();
      doc.reload();
    });

  if (wf.error || doc.error) return <ErrorBox error={wf.error ?? doc.error} />;
  if (!wf.data || !def) return <div className="empty">Loading…</div>;
  const current = wf.data.versions.find((v) => v.version === version);
  const sel = def.steps.find((s) => s.id === selected);

  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          {wf.data.workflow.name}
        </h1>
        <select
          aria-label="Version"
          value={version}
          style={{ width: "auto" }}
          onChange={(e) => {
            if (dirty && !confirm("Discard unsaved changes?")) return;
            setParams({ v: e.target.value });
          }}
        >
          {wf.data.versions.map((v) => (
            <option key={v.version} value={v.version}>
              v{v.version} · {v.state}
            </option>
          ))}
        </select>
        {current && <Badge value={dirty ? "draft" : current.state} />}
        {dirty && <span className="hint">unsaved</span>}
        {can("workflow.edit") && (
          <>
            <button onClick={() => void validate()} disabled={act.busy}>
              Validate
            </button>
            <button onClick={() => void act.run(() => save())} disabled={act.busy || !dirty}>
              Save draft
            </button>
          </>
        )}
        {can("workflow.publish") && (
          <button className="primary" onClick={() => void publish()} disabled={act.busy || (!dirty && current?.state === "published")}>
            Publish
          </button>
        )}
        {can("run.start") && wf.data.workflow.active_version && (
          <button onClick={() => setStarting(true)} disabled={act.busy}>
            Start run
          </button>
        )}
      </div>
      <ErrorBox error={act.error} />
      {notice && (
        <div className="notice" onClick={() => setNotice("")}>
          {notice}
        </div>
      )}
      {problems.length > 0 && (
        <div className="error">
          {problems.length} problem{problems.length > 1 ? "s" : ""}:
          <ul>
            {problems.map((p, i) => (
              <li key={i}>
                <code>{p.path}</code> {p.message}
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="tabs" role="tablist">
        {(["canvas", "settings", "code", "json", "triggers"] as const).map((t) => (
          <button key={t} role="tab" className={tab === t ? "active" : ""} onClick={() => setTab(t)}>
            {{ canvas: "Canvas", settings: "Trigger & settings", code: "Code", json: "JSON", triggers: "Endpoints" }[t]}
          </button>
        ))}
      </div>
      {tab === "canvas" && (
        <div className="editor">
          <div className="canvas">
            <ReactFlow
              nodes={nodes}
              edges={graph.edges}
              nodeTypes={nodeTypes}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              onPaneClick={() => setSelected(null)}
              fitView
              fitViewOptions={{ maxZoom: 1 }}
              deleteKeyCode={["Delete", "Backspace"]}
              proOptions={{ hideAttribution: true }}
            >
              <Background />
              <Controls />
              <FitOnChange count={def.steps.length} />
            </ReactFlow>
          </div>
          <div className="side">
            {can("workflow.edit") && (
              <>
                <div className="hint">Add a step{selected ? ` after ${selected}` : ""}:</div>
                <div className="palette">
                  {PALETTE.map((t) => (
                    <button key={t} onClick={() => addStep(t)}>
                      + {t}
                    </button>
                  ))}
                </div>
              </>
            )}
            {sel ? (
              <StepPanel
                key={sel.id}
                step={sel}
                others={def.steps.filter((s) => s.id !== sel.id).map((s) => s.id)}
                connectors={connectors.data?.connectors ?? []}
                onChange={(s) => editSteps(def.steps.map((x) => (x.id === sel.id ? s : x)))}
                onRename={(to) => {
                  if (def.steps.some((s) => s.id === to)) return false;
                  editSteps(renameStep(def.steps, sel.id, to));
                  if (layout[sel.id]) setLayout({ ...layout, [to]: layout[sel.id] as { x: number; y: number } });
                  setSelected(to);
                  return true;
                }}
                onDelete={() => {
                  editSteps(removeStep(def.steps, sel.id));
                  setSelected(null);
                }}
              />
            ) : (
              <p className="hint">Select a step to edit it. Drag from a step's right handle to another step to make it run after; select an arrow and press Delete to remove it.</p>
            )}
          </div>
        </div>
      )}
      {tab === "settings" && <SettingsTab def={def} onChange={edit} connectors={connectors.data?.connectors ?? []} />}
      {tab === "code" && (
        <CodeTab
          def={def}
          onApply={(next, probs) => {
            edit(next);
            setProblems(probs);
          }}
        />
      )}
      {tab === "json" && (
        <div className="card">
          <p className="hint">The whole definition (wd/v1). Edits apply as you type, once the JSON is valid.</p>
          <JsonInput value={def} onChange={(v) => v && typeof v === "object" && edit(v as WorkflowDefinition)} rows={30} />
        </div>
      )}
      {tab === "triggers" && <Endpoints workflow={id} />}
      {current && (
        <p className="hint">
          v{current.version} created {fmtTime(current.created_at)}
          {current.published_at && `, published ${fmtTime(current.published_at)}`} · digest <code>{current.digest.slice(0, 12)}</code>
        </p>
      )}
      {starting && <StartRun workflow={id} onClose={() => setStarting(false)} onStarted={(run) => nav(`/runs/${run}`)} />}
      {merge && (
        <MergeDialog
          conflicts={merge.conflicts}
          latest={merge.latest}
          busy={act.busy}
          onClose={() => setMerge(undefined)}
          onResolve={(res) => void act.run(() => save(res))}
        />
      )}
    </>
  );
}

/** Refits the view when steps are added or removed, so new steps are visible. */
function FitOnChange({ count }: { count: number }) {
  const rf = useReactFlow();
  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    const t = setTimeout(() => void rf.fitView({ maxZoom: 1, duration: 200 }), 30);
    return () => clearTimeout(t);
  }, [count, rf]);
  return null;
}

function SettingsTab({ def, onChange, connectors }: { def: WorkflowDefinition; onChange: (d: WorkflowDefinition) => void; connectors: ConnectorInfo[] }) {
  const t = def.trigger;
  const cfg = (t.config ?? {}) as Record<string, unknown>;
  const setCfg = (patch: Record<string, unknown>) => onChange({ ...def, trigger: { ...t, config: merge(cfg, patch) } });
  const settings = def.settings ?? {};
  const setSettings = (patch: Record<string, unknown>) => {
    const next = merge(settings, patch);
    onChange({ ...def, settings: Object.keys(next).length ? next : undefined } as WorkflowDefinition);
  };
  const conn = connectors.find((c) => c.ref === cfg.connector);
  return (
    <div className="card" style={{ maxWidth: 760 }}>
      <Field label="Name">
        <input value={def.name} onChange={(e) => onChange({ ...def, name: e.target.value })} />
      </Field>
      <Field label="Description">
        <textarea rows={2} value={def.description ?? ""} onChange={(e) => onChange({ ...def, description: e.target.value || undefined } as WorkflowDefinition)} />
      </Field>
      <h2>Trigger</h2>
      <Field label="Type">
        <select
          value={t.type}
          onChange={(e) => {
            const type = e.target.value as typeof t.type;
            const config: Record<string, unknown> | undefined =
              type === "webhook" ? { path: "/" + def.id.replace(/^wf_/, ""), auth: "hmac" } : type === "schedule" ? { cron: "0 9 * * 1-5" } : type === "connector_event" ? { connector: "paystack@1", trigger: "transfer_event" } : undefined;
            onChange({ ...def, trigger: config ? { type, config } : { type } });
          }}
        >
          <option value="manual">Manual / API</option>
          <option value="webhook">Webhook</option>
          <option value="schedule">Schedule</option>
          <option value="connector_event">Connector event</option>
        </select>
      </Field>
      {t.type === "webhook" && (
        <>
          <div className="row">
            <Field label="Path">
              <input className="mono" value={String(cfg.path ?? "")} onChange={(e) => setCfg({ path: e.target.value })} />
            </Field>
            <Field label="Authentication" hint={`Key: the environment secret webhook_${def.id}`}>
              <select value={String(cfg.auth ?? "hmac")} onChange={(e) => setCfg({ auth: e.target.value })}>
                <option value="hmac">HMAC-SHA256 signature</option>
                <option value="bearer">Bearer token</option>
                <option value="none">None</option>
              </select>
            </Field>
          </div>
          <Field label="Deduplicate on" hint="Expression; deliveries with the same value start one run. Default: the body's hash.">
            <input className="mono" value={String(cfg.dedup ?? "")} placeholder="=trigger.body.id" onChange={(e) => setCfg({ dedup: e.target.value })} />
          </Field>
        </>
      )}
      {t.type === "schedule" && (
        <div className="row">
          <Field label="Cron" hint="Five fields: minute hour day month weekday">
            <input className="mono" value={String(cfg.cron ?? "")} onChange={(e) => setCfg({ cron: e.target.value })} />
          </Field>
          <Field label="Time zone">
            <input value={String(cfg.timezone ?? "")} placeholder="Africa/Lagos" onChange={(e) => setCfg({ timezone: e.target.value })} />
          </Field>
        </div>
      )}
      {t.type === "connector_event" && (
        <>
          <div className="row">
            <Field label="Connector">
              <select value={String(cfg.connector ?? "")} onChange={(e) => setCfg({ connector: e.target.value, trigger: connectors.find((c) => c.ref === e.target.value)?.triggers[0] })}>
                {connectors
                  .filter((c) => c.triggers.length > 0)
                  .map((c) => (
                    <option key={c.ref} value={c.ref}>
                      {c.name}
                    </option>
                  ))}
              </select>
            </Field>
            <Field label="Trigger">
              <select value={String(cfg.trigger ?? "")} onChange={(e) => setCfg({ trigger: e.target.value })}>
                {conn?.triggers.map((x) => (
                  <option key={x}>{x}</option>
                ))}
              </select>
            </Field>
          </div>
          <Field label="Only these events" hint="Comma-separated, e.g. transfer.failed, transfer.reversed; empty for all">
            <input
              value={Array.isArray(cfg.events) ? (cfg.events as string[]).join(", ") : ""}
              onChange={(e) => {
                const events = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
                setCfg({ events: events.length ? events : undefined });
              }}
            />
          </Field>
        </>
      )}
      <h2>Inputs schema</h2>
      <p className="hint">JSON Schema for trigger.body; fields marked "x-pii" are encrypted per data subject.</p>
      <JsonInput value={def.inputs?.schema} onChange={(v) => onChange({ ...def, inputs: v === undefined ? undefined : { schema: v as object } } as WorkflowDefinition)} rows={8} />
      <h2>Run settings</h2>
      <div className="row">
        <Field label="Run timeout">
          <input value={settings.timeout ?? ""} placeholder="72h" onChange={(e) => setSettings({ timeout: e.target.value })} />
        </Field>
        <Field label="Retention after the run ends">
          <input value={settings.retention ?? ""} placeholder="90d" onChange={(e) => setSettings({ retention: e.target.value })} />
        </Field>
      </div>
      <div className="row">
        <Field label="Concurrency key" hint="Runs with the same key run one at a time (or up to the limit)">
          <input className="mono" value={settings.concurrency_key ?? ""} onChange={(e) => setSettings({ concurrency_key: e.target.value })} />
        </Field>
        <Field label="Max concurrent runs per key">
          <input type="number" min={1} value={settings.max_concurrency ?? ""} onChange={(e) => setSettings({ max_concurrency: e.target.value ? Number(e.target.value) : undefined })} />
        </Field>
      </div>
    </div>
  );
}

function Endpoints({ workflow }: { workflow: string }) {
  const { data, error } = useLoad(() => get<{ triggers: TriggerInfo[] }>(`/v1/workflows/${workflow}/triggers`), [workflow]);
  if (error) return <ErrorBox error={error} />;
  if (!data) return null;
  if (data.triggers.length === 0) return <div className="empty card">The published version is started manually or through the API: POST /v1/workflows/{workflow}/runs.</div>;
  return (
    <table>
      <thead>
        <tr>
          <th>Environment</th>
          <th>Type</th>
          <th>Endpoint or schedule</th>
          <th>Version</th>
        </tr>
      </thead>
      <tbody>
        {data.triggers.map((t) => (
          <tr key={t.id}>
            <td>{t.environment}</td>
            <td>{t.type.replaceAll("_", " ")}</td>
            <td>
              {t.url && <code>{window.location.origin + t.url}</code>}
              {t.secret_name && t.auth !== "none" && <div className="hint">Signed with the secret {t.secret_name} ({t.auth})</div>}
              {t.cron && (
                <>
                  <code>{t.cron}</code> ({t.timezone}) · next {fmtTime(t.next_fire_at)}
                </>
              )}
            </td>
            <td>v{t.version}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function StartRun({ workflow, onClose, onStarted }: { workflow: string; onClose: () => void; onStarted: (run: string) => void }) {
  const [input, setInput] = useState<unknown>({});
  const [env, setEnv] = useState("prod");
  const act = useAction();
  return (
    <Modal title="Start a run" onClose={onClose}>
      <Field label="Environment">
        <select value={env} onChange={(e) => setEnv(e.target.value)}>
          <option value="prod">prod</option>
          <option value="dev">dev</option>
        </select>
      </Field>
      <Field label="Input (trigger.body)">
        <JsonInput value={input} onChange={setInput} rows={8} />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          className="primary"
          disabled={act.busy}
          onClick={() =>
            void act.run(async () => {
              const r = await post<{ run_id: string }>(`/v1/workflows/${workflow}/runs`, { input: input ?? {}, environment: env });
              onStarted(r.run_id);
            })
          }
        >
          Start
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}

/**
 * The workflow as code (spec 10.2): generated from the definition, editable,
 * and applied back by compiling it on the server. Generated code always
 * builds to the definition it came from.
 */
function CodeTab({ def, onApply }: { def: WorkflowDefinition; onApply: (def: WorkflowDefinition, problems: Problem[]) => void }) {
  const [base, setBase] = useState<string>();
  const [code, setCode] = useState("");
  const gen = useAction();
  const apply = useAction();
  useEffect(() => {
    void gen.run(async () => {
      const r = await post<{ code: string }>("/v1/code/generate", { definition: def });
      setBase(r.code);
      setCode(r.code);
    });
  }, [def]);
  const changed = base !== undefined && code !== base;
  return (
    <div className="card">
      <p className="hint">
        This workflow as TypeScript with <code>@taskiem/sdk</code>, the same code <code>taskiem codegen</code> writes. Edit it and apply: the code is
        compiled to a definition, and the canvas shows the result. Expressions are arrow functions or CEL strings; logic belongs in code steps.
      </p>
      <ErrorBox error={gen.error ?? apply.error} />
      <textarea className="mono" rows={32} value={code} spellCheck={false} onChange={(e) => setCode(e.target.value)} aria-label="Workflow code" />
      <div className="row">
        <button
          className="primary"
          disabled={!changed || apply.busy}
          onClick={() =>
            void apply.run(async () => {
              const r = await post<{ definition: WorkflowDefinition; problems: Problem[] }>("/v1/code/compile", { source: code });
              onApply(r.definition, r.problems);
            })
          }
        >
          {apply.busy ? "Compiling…" : "Apply to workflow"}
        </button>
        <button disabled={!changed} onClick={() => base !== undefined && setCode(base)}>
          Discard changes
        </button>
      </div>
    </div>
  );
}

/** Lets an editor choose, for each conflict, their edit or the newer version's. */
function MergeDialog({
  conflicts,
  latest,
  busy,
  onClose,
  onResolve,
}: {
  conflicts: MergeConflict[];
  latest: number;
  busy: boolean;
  onClose: () => void;
  onResolve: (res: Record<string, "ours" | "theirs">) => void;
}) {
  const [choice, setChoice] = useState<Record<string, "ours" | "theirs">>({});
  const show = (v: unknown) => (v === undefined ? "(removed)" : JSON.stringify(v, null, 2));
  const label = (c: MergeConflict) => (c.path.startsWith("steps/") ? `Step ${c.path.slice(6)}` : `Workflow ${c.path}`);
  return (
    <Modal title="Resolve conflicting edits" onClose={onClose}>
      <p className="hint">
        Version {latest} changed these parts too. Everything else is merged. Choose which version of each to keep, then save.
      </p>
      {conflicts.map((c) => (
        <fieldset key={c.path} className="card" data-testid={`conflict-${c.path}`}>
          <legend>
            {label(c)} {c.kind === "changed_and_deleted" && "(changed on one side, removed on the other)"}
          </legend>
          <div className="row">
            {(["ours", "theirs"] as const).map((side) => (
              <label key={side} className="grow">
                <input type="radio" name={c.path} checked={choice[c.path] === side} onChange={() => setChoice({ ...choice, [c.path]: side })} />{" "}
                {side === "ours" ? "Yours" : `Version ${latest}`}
                <pre className="mono">{show(c[side])}</pre>
              </label>
            ))}
          </div>
        </fieldset>
      ))}
      <div className="row">
        <button className="primary" disabled={busy || conflicts.some((c) => !choice[c.path])} onClick={() => onResolve(choice)}>
          Save merged version
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
