import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { Background, Controls, ReactFlow, useReactFlow, type Connection, type EdgeChange, type NodeChange } from "@xyflow/react";
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { ApiError, get, post, put, type ConnectorInfo, type JSONSchema, type MergeConflict, type Problem, type TriggerInfo, type VersionInfo, type WorkflowSummary } from "../api";
import { useAdvanced, useAuth } from "../auth";
import { SchedulePicker } from "../canvas/PlainPickers";
import { sourcesFor } from "../lib/plaincanvas";
import { StepNode, TRIGGER_NODE, TriggerNode, triggerCard, type StepFlowNode, type TriggerFlowNode } from "../canvas/StepNode";
import { StepPanel } from "../canvas/StepPanel";
import { FormTriggerEditor, startFormTrigger } from "../canvas/FormEditor";
import { connect, disconnect, freshId, newStep, removeStep, renameStep, toGraph, type Layout } from "../lib/graph";
import { merge } from "../lib/schema";
import { EnvSelect, useEnvironments, type Deployment } from "../environments";
import { Badge, ErrorBox, Field, JsonInput, Modal, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { AIBuilder } from "./AIBuild";
import { resolveSchema } from "../lib/values";
import { SchemaForm } from "../forms/SchemaForm";
import { NeedsList } from "./Readiness";
import { ProblemLine } from "../forms/ProblemLine";
import type { ReadyNeed, Readiness } from "../lib/readiness";

const nodeTypes = { step: StepNode, trigger: TriggerNode };
const PALETTE: Step["type"][] = ["connector", "http", "code", "transform", "approval", "form", "signal", "wait", "branch", "parallel", "foreach"];

/** The steps an owner adds without Advanced, in their words. */
const PLAIN_PALETTE: [Step["type"], string][] = [
  ["connector", "Use a service"],
  ["approval", "Ask for approval"],
  ["form", "Ask someone to fill in a form"],
  ["wait", "Wait"],
];

interface Loaded {
  workflow: WorkflowSummary;
  versions: VersionInfo[];
  deployments: Deployment[];
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
  const advanced = useAdvanced();
  const wf = useLoad(() => get<Loaded>(`/v1/workflows/${id}`), [id]);
  const connectors = useLoad(() => get<{ connectors: ConnectorInfo[] }>("/v1/connectors"), []);
  const version = Number(params.get("v")) || wf.data?.workflow.latest_version || 0;
  const doc = useLoad(() => (version ? get<VersionDoc>(`/v1/workflows/${id}/versions/${version}`) : Promise.resolve(undefined)), [id, version]);

  const [def, setDef] = useState<WorkflowDefinition>();
  const [layout, setLayout] = useState<Layout>({});
  const [problems, setProblems] = useState<Problem[]>([]);
  // A publish refused for what the version still needs.
  const [needs, setNeeds] = useState<{ version: number; needs: ReadyNeed[] }>();
  const [selected, setSelected] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  // ?tab=settings opens on the trigger (a new form opens on its questions).
  const [tab, setTab] = useState<"canvas" | "settings" | "code" | "json" | "triggers">(() => (params.get("tab") === "settings" ? "settings" : "canvas"));
  const [notice, setNotice] = useState("");
  const [starting, setStarting] = useState(false);
  const [merge, setMerge] = useState<{ conflicts: MergeConflict[]; latest: number }>();
  const [ai, setAI] = useState(false);
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

  const [sizes, setSizes] = useState<Record<string, { width: number; height: number }>>({});
  const invalid = useMemo(() => (def ? problemSteps(def, problems) : new Set<string>()), [def, problems]);
  const graph = useMemo(() => (def ? toGraph(def, layout) : { nodes: [], edges: [] }), [def, layout]);
  const stepNodes: StepFlowNode[] = graph.nodes.map((n) => ({
    id: n.id,
    type: "step",
    position: n.position,
    selected: n.id === selected,
    measured: sizes[n.id],
    data: { step: n.step, invalid: invalid.has(n.id) },
  }));
  // What starts it, left of the steps that need nothing, joined to them.
  const trigger = def ? triggerCard(graph.nodes, def.trigger, { measured: sizes[TRIGGER_NODE] }) : null;
  const nodes: (StepFlowNode | TriggerFlowNode)[] = trigger ? [trigger.node, ...stepNodes] : stepNodes;
  const edges = trigger ? [...graph.edges, ...trigger.edges] : graph.edges;

  const onNodesChange = (changes: NodeChange<StepFlowNode | TriggerFlowNode>[]) => {
    let l = layout;
    let steps = def?.steps;
    // The canvas measures each card and reports it here; a card it has no
    // size for stays hidden, so the sizes outlive reloads of the definition.
    const measured = changes.filter((c) => c.type === "dimensions" && c.dimensions);
    if (measured.length > 0)
      setSizes((was) => {
        const next = { ...was };
        for (const c of measured) if (c.type === "dimensions" && c.dimensions) next[c.id] = c.dimensions;
        return next;
      });
    for (const c of changes) {
      if ("id" in c && c.id === TRIGGER_NODE) continue;
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
        if (source && target && source !== TRIGGER_NODE) steps = disconnect(steps, source, target);
      }
    }
    if (steps && steps !== def?.steps) editSteps(steps);
  };
  const onConnect = (c: Connection) => {
    if (!def || !c.source || !c.target || c.source === TRIGGER_NODE) return;
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
      let r: { state: string; git?: Record<string, { url?: string; error?: string }> };
      try {
        r = await post(`/v1/workflows/${id}/versions/${v}/publish`);
      } catch (e) {
        // Not ready: what it needs, each with its fix, instead of an error.
        if (e instanceof ApiError && e.status === 422 && e.body.code === "not_ready") {
          setNeeds({ version: v, needs: (e.body.needs as ReadyNeed[] | undefined) ?? [] });
          return;
        }
        if (e instanceof ApiError && e.status === 422) setProblems(e.problems as Problem[]);
        throw e;
      }
      setNeeds(undefined);
      if (r.state === "pending_approval") {
        setNotice(`Version ${v} is waiting for a second person to publish it (Approvals → Publishing to review).`);
        wf.reload();
        return;
      }
      const git = Object.entries(r.git ?? {})
        .map(([env, g]) => (g.url ? `Pull request for ${env}: ${g.url}` : `The pull request for ${env} could not be opened: ${g.error ?? "unknown error"}`))
        .join(" ");
      setNotice(`Version ${v} is published; new runs use it.${git ? " " + git : ""}`);
      wf.reload();
      doc.reload();
    });

  if (wf.error || doc.error) return <ErrorBox error={wf.error ?? doc.error} />;
  if (!wf.data || !def) return <Skeleton />;
  const current = wf.data.versions.find((v) => v.version === version);
  const sel = def.steps.find((s) => s.id === selected);
  const managed = wf.data.workflow.git_path;

  return (
    <>
      <p className="crumbs">
        <Link to="/workflows" aria-label="Back to Workflows">
          Workflows
        </Link>
      </p>
      <div className="editor-head">
        <div className="editor-title">
          <h1>{wf.data.workflow.name}</h1>
          {dirty ? (
            <span className="badge warn">Unsaved changes</span>
          ) : current?.state === "published" ? (
            <span className="badge published">Live</span>
          ) : (
            current && <Badge value={current.state} />
          )}
        </div>
        <div className="page-actions">
          {(advanced || wf.data.versions.length > 1) && (
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
                  Version {v.version} · {v.state}
                </option>
              ))}
            </select>
          )}
          {can("workflow.edit") && !managed && (
            <>
              {advanced && (
                <button onClick={() => void validate()} disabled={act.busy}>
                  Validate
                </button>
              )}
              <button onClick={() => setAI(true)} disabled={act.busy || dirty} title={dirty ? "Save your changes first" : undefined}>
                Change with AI
              </button>
              <button onClick={() => void act.run(() => save())} disabled={act.busy || !dirty}>
                Save draft
              </button>
            </>
          )}
          {can("run.start") && wf.data.workflow.active_version && (
            <button onClick={() => setStarting(true)} disabled={act.busy}>
              Start run
            </button>
          )}
          {can("workflow.publish") && !managed && (
            <button className="primary" onClick={() => void publish()} disabled={act.busy || (!dirty && current?.state === "published")}>
              Publish
            </button>
          )}
        </div>
      </div>
      <ErrorBox error={act.error} />
      {needs && (
        <div>
          <p>Version {needs.version} is saved, but not published yet: fix these, then publish again.</p>
          <NeedsList
            needs={needs.needs}
            onChanged={() =>
              void get<Readiness>(`/v1/workflows/${id}/versions/${needs.version}/readiness`).then((r) =>
                setNeeds(r.ready ? undefined : { version: needs.version, needs: r.needs }),
              )
            }
          />
        </div>
      )}
      <Deployments workflow={id} deployments={wf.data.deployments} canPromote={can("workflow.publish")} onChange={wf.reload} />
      {managed && (
        <div className="notice" role="status">
          This workflow is managed in Git (<code>{managed}</code>). Change it in the repository: a push to the connected branch deploys it once its
          tests pass.
        </div>
      )}
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
                <ProblemLine p={p} onStep={setSelected} />
              </li>
            ))}
          </ul>
        </div>
      )}
      <div className="tabs" role="tablist">
        {(advanced ? (["canvas", "settings", "code", "json", "triggers"] as const) : (["canvas", "settings", "triggers"] as const)).map((t) => (
          <button key={t} role="tab" aria-selected={tab === t} className={tab === t ? "active" : ""} onClick={() => setTab(t)}>
            {(advanced ? { canvas: "Canvas", settings: "Trigger & settings", code: "Code", json: "JSON", triggers: "Endpoints" } : { canvas: "Steps", settings: "When it runs", code: "", json: "", triggers: "Web addresses" })[t]}
          </button>
        ))}
      </div>
      {tab === "canvas" && (
        <div className="editor">
          <div className="canvas">
            <ReactFlow
              nodes={nodes}
              edges={edges}
              nodeTypes={nodeTypes}
              onNodeClick={(_, n) => n.id === TRIGGER_NODE && setTab("settings")}
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
                  {advanced
                    ? PALETTE.map((t) => (
                        <button key={t} onClick={() => addStep(t)}>
                          + {t}
                        </button>
                      ))
                    : PLAIN_PALETTE.map(([t, words]) => (
                        <button key={t} onClick={() => addStep(t)}>
                          + {words}
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
                names={Object.fromEntries(def.steps.map((s) => [s.id, s.name ?? ""]))}
                sources={sourcesFor(def as Parameters<typeof sourcesFor>[0], sel.id, connectors.data?.connectors ?? [])}
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
                problems={stepProblemsOf(def, problems, sel.id)}
              />
            ) : (
              <p className="hint">Select a step to edit it. Drag from a step's right handle to another step to make it run after; select an arrow and press Delete to remove it.</p>
            )}
          </div>
        </div>
      )}
      {tab === "settings" && <SettingsTab def={def} onChange={edit} connectors={connectors.data?.connectors ?? []} problems={problems} />}
      {advanced && tab === "code" && (
        <CodeTab
          def={def}
          onApply={(next, probs) => {
            edit(next);
            setProblems(probs);
          }}
        />
      )}
      {advanced && tab === "json" && (
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
          {current.ai_build && " · drafted with AI"}
          {current.git_commit && (
            <>
              {" "}
              · from commit <code>{current.git_commit.slice(0, 10)}</code>
            </>
          )}
          {current.git_request && (
            <>
              {" "}
              ·{" "}
              <a href={current.git_request} target="_blank" rel="noreferrer">
                pull request
              </a>
            </>
          )}
        </p>
      )}
      {starting && <StartRun workflow={id} def={def} onClose={() => setStarting(false)} onStarted={(run) => nav(`/runs/${run}`)} />}
      {ai && (
        <AIBuilder
          workflow={id}
          onClose={() => setAI(false)}
          onSaved={(_, v) => {
            setAI(false);
            setNotice(`Saved the AI's proposal as draft version ${v}. Review it here, then publish as usual.`);
            setParams({ v: String(v) });
            wf.reload();
          }}
        />
      )}
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

/** The validator's problems with one top-level step, at paths relative to it. */
function stepProblemsOf(def: WorkflowDefinition, problems: Problem[], id: string): Problem[] {
  const idx = def.steps.findIndex((s) => s.id === id);
  const out: Problem[] = [];
  for (const p of problems) {
    for (const pre of [`/steps/${idx}`, `/steps/${id}`]) {
      if (p.path === pre || p.path.startsWith(pre + "/")) {
        out.push({ ...p, path: p.path.slice(pre.length) });
        break;
      }
    }
  }
  return out;
}

function SettingsTab({ def, onChange, connectors, problems }: { def: WorkflowDefinition; onChange: (d: WorkflowDefinition) => void; connectors: ConnectorInfo[]; problems: Problem[] }) {
  const t = def.trigger;
  const cfg = (t.config ?? {}) as Record<string, unknown>;
  const setCfg = (patch: Record<string, unknown>) => onChange({ ...def, trigger: { ...t, config: merge(cfg, patch) } });
  const settings = def.settings ?? {};
  const setSettings = (patch: Record<string, unknown>) => {
    const next = merge(settings, patch);
    onChange({ ...def, settings: Object.keys(next).length ? next : undefined } as WorkflowDefinition);
  };
  const conn = connectors.find((c) => c.ref === cfg.connector);
  const advanced = useAdvanced();
  return (
    <>
    <div className="card" style={{ maxWidth: 760 }}>
      <Field label="Name">
        <input value={def.name} onChange={(e) => onChange({ ...def, name: e.target.value })} />
      </Field>
      <Field label="Description">
        <textarea rows={2} value={def.description ?? ""} onChange={(e) => onChange({ ...def, description: e.target.value || undefined } as WorkflowDefinition)} />
      </Field>
      <h2>{advanced ? "Trigger" : "When it runs"}</h2>
      <Field label={advanced ? "Type" : "It runs"}>
        <select
          value={t.type}
          onChange={(e) => {
            const type = e.target.value as typeof t.type;
            if (type === "form") return onChange(startFormTrigger(def));
            const config: Record<string, unknown> | undefined =
              type === "webhook" ? { path: "/" + def.id.replace(/^wf_/, ""), auth: "hmac" } : type === "schedule" ? { cron: "0 9 * * 1-5" } : type === "connector_event" ? { connector: "paystack@1", trigger: "transfer_event" } : undefined;
            onChange({ ...def, trigger: config ? { type, config } : { type } });
          }}
        >
          <option value="manual">{advanced ? "Manual / API" : "When someone starts it"}</option>
          {(advanced || t.type === "webhook") && <option value="webhook">{advanced ? "Webhook" : "When another system calls it"}</option>}
          <option value="schedule">{advanced ? "Schedule" : "On a schedule"}</option>
          <option value="connector_event">{advanced ? "Connector event" : "When something happens in a service"}</option>
          <option value="form">{advanced ? "Form" : "When someone fills in a form"}</option>
          {!["manual", "webhook", "schedule", "connector_event", "form"].includes(t.type) && <option value={t.type}>{t.type.replaceAll("_", " ")}</option>}
        </select>
      </Field>
      {t.type === "webhook" && !advanced && <p className="hint">Another system starts it at its web address (under Web addresses). Turn on Advanced in Settings to change how.</p>}
      {t.type === "webhook" && advanced && (
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
      {t.type === "schedule" && !advanced && <SchedulePicker cron={String(cfg.cron ?? "")} onChange={(cron) => setCfg({ cron, timezone: cfg.timezone ?? "Africa/Lagos" })} />}
      {t.type === "schedule" && advanced && (
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
            <Field label={advanced ? "Trigger" : "What happens"}>
              <select value={String(cfg.trigger ?? "")} onChange={(e) => setCfg({ trigger: e.target.value })}>
                {conn?.triggers.map((x) => (
                  <option key={x} value={x}>
                    {advanced ? x : x.replace(/_event$/, "").replace(/_/g, " ")}
                  </option>
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
      {t.type !== "form" && advanced && (
        <>
          <h2>Inputs schema</h2>
          <p className="hint">JSON Schema for trigger.body; fields marked "x-pii" are encrypted per data subject.</p>
          <JsonInput value={def.inputs?.schema} onChange={(v) => onChange({ ...def, inputs: v === undefined ? undefined : { schema: v as object } } as WorkflowDefinition)} rows={8} />
        </>
      )}
      {advanced && (
      <>
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
      </>
      )}
    </div>
    {t.type === "form" && <FormTriggerEditor def={def} onChange={onChange} problems={problems} />}
    </>
  );
}

interface SigningSecret {
  environment: string;
  secret_name: string;
  auth: string;
  secret: string;
}

function Endpoints({ workflow }: { workflow: string }) {
  const { can } = useAuth();
  const { data, error } = useLoad(() => get<{ triggers: TriggerInfo[] }>(`/v1/workflows/${workflow}/triggers`), [workflow]);
  const act = useAction();
  const [made, setMade] = useState<SigningSecret | null>(null);
  // Production only, unless asked: the other environments are rehearsals.
  const [allEnvs, setAllEnvs] = useState(false);
  if (error) return <ErrorBox error={error} />;
  if (!data) return null;
  if (data.triggers.length === 0) return <div className="empty card">The published version is started manually or through the API: POST /v1/workflows/{workflow}/runs.</div>;
  const newSecret = (env: string) =>
    confirm(`Make a new signing secret for ${env}? It replaces the current one at once: whoever sends to this webhook must switch to the new one.`) &&
    void act.run(async () => setMade(await post<SigningSecret>(`/v1/workflows/${workflow}/signing-secret`, { environment: env })));
  return (
    <>
      {made && (
        <div className="card" role="status">
          <p>
            The new {made.auth === "bearer" ? "token" : "signing secret"} for {made.environment}. Copy it now and give it to the sender: it is not shown again.
          </p>
          <pre className="secret-once">{made.secret}</pre>
          <p>
            <button onClick={() => void navigator.clipboard?.writeText(made.secret)}>Copy</button> <button onClick={() => setMade(null)}>Done</button>
          </p>
        </div>
      )}
      <ErrorBox error={act.error} />
      <table>
        <thead>
          <tr>
            <th>Environment</th>
            <th>Type</th>
            <th>Web address or schedule</th>
            <th>Version</th>
          </tr>
        </thead>
        <tbody>
          {data.triggers.filter((t) => allEnvs || t.environment === "prod" || !data.triggers.some((x) => x.environment === "prod")).map((t) => (
            <tr key={t.id}>
              <td>{t.environment}</td>
              <td>{t.type.replaceAll("_", " ")}</td>
              <td>
                {t.remote && (
                  <div>
                    <Badge value={t.remote.health} /> Registered with the provider by Taskiem
                    {(t.remote.status_reason || t.remote.last_error) && <div className="hint">{t.remote.status_reason ?? t.remote.last_error}</div>}
                    {t.remote.health !== "ok" && t.remote.health !== "pending" && <div className="hint">Publish again to repair it.</div>}
                  </div>
                )}
                {t.url && !t.remote && <code>{window.location.origin + t.url}</code>}
                {t.url && !t.remote && <div className="hint">{endpointPurpose(t)}</div>}
                {t.secret_name && t.auth !== "none" && (
                  <div className="hint">
                    Signed with the secret {t.secret_name} ({t.auth}){" "}
                    {t.type === "webhook" && can("secret.manage") && (
                      <button disabled={act.busy} onClick={() => newSecret(t.environment)}>
                        New signing secret
                      </button>
                    )}
                  </div>
                )}
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
      {data.triggers.some((t) => t.environment !== "prod") && (
        <button className="link" onClick={() => setAllEnvs(!allEnvs)}>
          {allEnvs ? "Show production only" : "Show the other environments"}
        </button>
      )}
    </>
  );
}

/** What a trigger's web address is for, in plain words. */
export function endpointPurpose(t: TriggerInfo): string {
  if (t.type === "webhook") return "Give this address to the system that sends you the data, such as your website or shop.";
  const service = t.service ?? t.connector?.split("@")[0] ?? "the service";
  if (t.polled) return `Taskiem checks ${service} for new events every few minutes. To get them at once, you can also add this address in ${service}'s dashboard; it is optional.`;
  return `Add this address in ${service}'s dashboard, so ${service} tells Taskiem when something happens.`;
}

/** Which version each environment runs, and promotion into gated ones. */
function Deployments({ workflow, deployments, canPromote, onChange }: { workflow: string; deployments: Deployment[]; canPromote: boolean; onChange: () => void }) {
  const envs = useEnvironments();
  const act = useAction();
  const advanced = useAdvanced();
  const list = envs.data?.environments ?? [];
  // One business, one place it runs: environments show with Advanced, or
  // once someone has set up promotion between them.
  if (list.length === 0 || (!advanced && !list.some((e) => e.promotion_from))) return null;
  const at = (env: string) => deployments.find((d) => d.environment === env);
  return (
    <div className="deployments hint" aria-label="Deployments">
      {list.map((e) => {
        const d = at(e.name);
        const from = e.promotion_from ? at(e.promotion_from) : undefined;
        const promotable = canPromote && !e.git && e.promotion_from && from && from.version !== d?.version;
        return (
          <span key={e.name} className="deployment">
            <strong>{e.name}</strong> {d ? `v${d.version}` : "not deployed"}
            {promotable && (
              <button
                className="small"
                disabled={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    await post(`/v1/workflows/${workflow}/promote`, { from: e.promotion_from, to: e.name });
                    onChange();
                    envs.reload();
                  })
                }
              >
                Promote v{from.version} from {e.promotion_from}
              </button>
            )}
          </span>
        );
      })}
      <ErrorBox error={act.error} />
    </div>
  );
}

function StartRun({ workflow, def, onClose, onStarted }: { workflow: string; def?: WorkflowDefinition; onClose: () => void; onStarted: (run: string) => void }) {
  const [input, setInput] = useState<unknown>({});
  const [env, setEnv] = useState("prod");
  const [advanced, setAdvanced] = useState(false);
  // Environments and raw input are for businesses with Advanced on.
  const technical = useAdvanced();
  const act = useAction();
  // A form from the workflow's inputs, as chat's run asks them (N5).
  const schema = resolveSchema(def?.inputs?.schema as JSONSchema | undefined, (def?.types ?? {}) as Record<string, JSONSchema>);
  const asks = !!schema?.properties && Object.keys(schema.properties).length > 0;
  return (
    <Modal title="Start a run" onClose={onClose}>
      {technical && (
        <Field label="Environment">
          <EnvSelect value={env} onChange={setEnv} />
        </Field>
      )}
      {asks && !advanced ? (
        <SchemaForm schema={schema as JSONSchema} value={input as Record<string, unknown> | undefined} onChange={setInput} expressions={false} />
      ) : technical || advanced ? (
        <Field label="Input (JSON)" data-technical>
          <JsonInput value={input} onChange={setInput} rows={8} />
        </Field>
      ) : (
        <p>{def?.name ?? "This workflow"} needs nothing from you to start. Start it now to see it work.</p>
      )}
      {asks && technical && (
        <label className="inline hint" data-technical>
          <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} /> Advanced: enter JSON
        </label>
      )}
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
    <div className="card" data-technical>
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
