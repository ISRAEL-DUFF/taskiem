// The embedded builder and run view (docs/embedding.md): the console's
// canvas, step panel and run canvas, talking only to the embed API with
// the end user's token. It renders inside a shadow root (the
// <taskiem-builder> and <taskiem-runs> elements) or the frame page.
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Background, Controls, ReactFlow, type Connection, type EdgeChange, type NodeChange } from "@xyflow/react";
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { ApiError, type ConnectorInfo, type Problem, type RunEvent, type RunSummary, type VersionInfo, type WorkflowSummary } from "../api";
import { RunCanvas } from "../canvas/RunCanvas";
import { StepNode, type StepFlowNode } from "../canvas/StepNode";
import { StepPanel } from "../canvas/StepPanel";
import { connect, disconnect, freshId, newStep, removeStep, renameStep, toGraph, type Layout } from "../lib/graph";
import { timeline } from "../lib/timeline";
import { Badge, ErrorBox, Field, JsonInput, fmtTime, useAction, useLoad } from "../ui";
import type { EmbedClient, EmbedMe } from "./client";
import { addFontStylesheet, parseBranding, themeVariables } from "./theme";

const nodeTypes = { step: StepNode };
/** Step types an app's end users may add; http and code only when the app allows them. */
const PALETTE: Step["type"][] = ["connector", "http", "code", "transform", "wait", "branch", "parallel", "foreach"];
const GATED = new Set<string>(["http", "code", "container", "ai"]);

/** Events the builder reports to its host (element events, or messages to the parent page). */
export type Emit = (name: string, detail: Record<string, unknown>) => void;

export interface EmbedProps {
  client: EmbedClient;
  view: "builder" | "runs";
  emit: Emit;
}

/** A new workflow's first draft: a manual trigger and one step. */
function starter(name: string): WorkflowDefinition {
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

export function EmbedApp({ client, view, emit }: EmbedProps) {
  const [me, setMe] = useState<EmbedMe>();
  const [error, setError] = useState<unknown>();
  const root = useRef<HTMLDivElement>(null);
  useEffect(() => {
    let live = true;
    let loaded = false;
    const load = () =>
      client.me().then(
        (m) => {
          if (!live) return;
          setMe(m);
          setError(undefined);
          if (!loaded) emit("loaded", { app: m.app_id, end_user: m.end_user.external_id, expires_at: m.expires_at });
          loaded = true;
        },
        (e: unknown) => live && setError(e),
      );
    void load();
    // A new token (setToken) re-reads who the end user is.
    const onToken = () => void load();
    client.addEventListener("token-set", onToken);
    return () => {
      live = false;
      client.removeEventListener("token-set", onToken);
    };
  }, [client, emit]);

  // Branding tokens become custom properties on the root, nothing else.
  const vars = useMemo(() => themeVariables(me?.branding), [me]);
  useLayoutEffect(() => {
    const el = root.current;
    if (!el) return;
    for (const [k, v] of Object.entries(vars)) el.style.setProperty(k, v);
    return () => {
      for (const k of Object.keys(vars)) el.style.removeProperty(k);
    };
  }, [vars]);
  const branding = parseBranding(me?.branding);
  // Fonts declared inside a shadow root are ignored by browsers: the font's
  // stylesheet goes in the document (an https URL the server checked).
  useEffect(() => {
    if (branding.font_url) addFontStylesheet(branding.font_url);
  }, [branding.font_url]);

  return (
    <div className="tk-root" ref={root} data-testid="taskiem-root">
      <header className="tk-header">
        {branding.logo_url && <img className="tk-logo" src={branding.logo_url} alt="" referrerPolicy="no-referrer" />}
        <span className="grow" />
        {me && <span className="hint">{me.end_user.external_id}</span>}
      </header>
      <ErrorBox error={error} />
      {me && (view === "runs" ? <Runs client={client} emit={emit} /> : <Builder client={client} me={me} emit={emit} />)}
      {!me && !error && <div className="empty">Loading…</div>}
      {me && !me.white_label && (
        <footer className="tk-footer" data-testid="taskiem-powered-by">
          Powered by Taskiem
        </footer>
      )}
    </div>
  );
}

function Builder({ client, me, emit }: { client: EmbedClient; me: EmbedMe; emit: Emit }) {
  const can = (p: string) => me.permissions.includes(p);
  const list = useLoad(() => client.get<{ workflows: WorkflowSummary[] }>("/workflows"), [client]);
  const [open, setOpen] = useState<string>();
  const [name, setName] = useState("");
  const act = useAction();
  if (open) return <Editor client={client} id={open} can={can} emit={emit} onBack={() => (setOpen(undefined), list.reload())} />;
  return (
    <div className="tk-body">
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Automations
        </h1>
        {can("workflow.edit") && (
          <>
            <input aria-label="New workflow name" placeholder="New workflow name" value={name} onChange={(e) => setName(e.target.value)} style={{ width: 220 }} />
            <button
              className="primary"
              disabled={!name.trim() || act.busy}
              onClick={() =>
                void act.run(async () => {
                  const r = await client.post<{ id: string }>("/workflows", { name: name.trim(), definition: starter(name.trim()) });
                  setName("");
                  setOpen(r.id);
                })
              }
            >
              Create
            </button>
          </>
        )}
      </div>
      <ErrorBox error={act.error ?? list.error} />
      {list.data && list.data.workflows.length === 0 && <div className="empty">No automations yet.</div>}
      {list.data && list.data.workflows.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Published</th>
              <th>Created</th>
            </tr>
          </thead>
          <tbody>
            {list.data.workflows.map((w) => (
              <tr key={w.id} className="clickable" onClick={() => setOpen(w.id)}>
                <td>{w.name}</td>
                <td>{w.active_version ? `v${w.active_version}` : "—"}</td>
                <td>{fmtTime(w.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}

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

function Editor({ client, id, can, emit, onBack }: { client: EmbedClient; id: string; can: (p: string) => boolean; emit: Emit; onBack: () => void }) {
  const wf = useLoad(() => client.get<Loaded>(`/workflows/${id}`), [client, id]);
  const catalogue = useLoad(() => client.get<{ connectors: ConnectorInfo[]; step_types: string[] }>("/connectors"), [client]);
  const [version, setVersion] = useState(0);
  const v = version || wf.data?.workflow.latest_version || 0;
  const doc = useLoad(() => (v ? client.get<VersionDoc>(`/workflows/${id}/versions/${v}`) : Promise.resolve(undefined)), [client, id, v]);
  const [def, setDef] = useState<WorkflowDefinition>();
  const [layout, setLayout] = useState<Layout>({});
  const [problems, setProblems] = useState<Problem[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [dirty, setDirty] = useState(false);
  const [notice, setNotice] = useState("");
  const [input, setInput] = useState<unknown>({});
  const [starting, setStarting] = useState(false);
  const [run, setRun] = useState<string>();
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
  const allowed = new Set(catalogue.data?.step_types ?? []);
  const palette = PALETTE.filter((t) => (t === "connector" ? (catalogue.data?.connectors.length ?? 0) > 0 : !GATED.has(t) || allowed.has(t)));

  const graph = useMemo(() => (def ? toGraph(def, layout) : { nodes: [], edges: [] }), [def, layout]);
  const nodes: StepFlowNode[] = graph.nodes.map((n) => ({ id: n.id, type: "step", position: n.position, selected: n.id === selected, data: { step: n.step, invalid: false } }));
  const onNodesChange = (changes: NodeChange<StepFlowNode>[]) => {
    let l = layout;
    let steps = def?.steps;
    for (const c of changes) {
      if (c.type === "position" && c.position) l = { ...l, [c.id]: c.position };
      else if (c.type === "select") setSelected((s) => (c.selected ? c.id : s === c.id ? null : s));
      else if (c.type === "remove" && steps) steps = removeStep(steps, c.id);
    }
    if (l !== layout) setLayout(l);
    if (steps && steps !== def?.steps) editSteps(steps);
  };
  const onEdgesChange = (changes: EdgeChange[]) => {
    let steps = def?.steps;
    for (const c of changes) {
      if (c.type !== "remove" || !steps) continue;
      const [source, target] = c.id.split("->");
      if (source && target) steps = disconnect(steps, source, target);
    }
    if (steps && steps !== def?.steps) editSteps(steps);
  };
  const onConnect = (c: Connection) => {
    if (!def || !c.source || !c.target) return;
    const next = connect(def.steps, c.source, c.target);
    if (next) editSteps(next);
    else setNotice("That connection would make a cycle.");
  };
  const addStep = (type: Step["type"]) => {
    if (!def) return;
    const sid = freshId(def.steps, type === "connector" ? "call" : type);
    const last = selected ?? def.steps[def.steps.length - 1]?.id;
    const s = newStep(type, sid);
    if (last) s.needs = [last];
    edit({ ...def, steps: [...def.steps, s] });
    setSelected(sid);
  };

  const save = async (): Promise<number> => {
    if (!def || !dirty) return v;
    const r = await client.post<{ version: number; problems: Problem[] }>(`/workflows/${id}/versions`, { definition: def, layout, parent_digest: doc.data?.version.digest });
    setProblems(r.problems);
    setDirty(false);
    setVersion(r.version);
    wf.reload();
    return r.version;
  };
  const validate = () =>
    act.run(async () => {
      const r = await client.post<{ valid: boolean; problems: Problem[] }>("/validate", { definition: def });
      setProblems(r.problems);
      setNotice(r.valid ? "Valid: this version can be published." : "");
    });
  const publish = () =>
    act.run(async () => {
      const n = await save();
      let out: { state?: string } | undefined;
      try {
        out = await client.post<{ state?: string }>(`/workflows/${id}/versions/${n}/publish`);
      } catch (e) {
        if (e instanceof ApiError && e.status === 422) setProblems(e.problems as Problem[]);
        throw e;
      }
      if (out?.state === "pending_approval") {
        // Four-eyes publishing: someone else approves it.
        setNotice(`Version ${n} is waiting for approval before it is published.`);
        emit("publish-requested", { workflow: id, version: n });
      } else {
        setNotice(`Version ${n} is published.`);
        emit("published", { workflow: id, version: n });
      }
      wf.reload();
      doc.reload();
    });
  const startRun = () =>
    act.run(async () => {
      const r = await client.post<{ run_id: string }>(`/workflows/${id}/runs`, { input: input ?? {} });
      setStarting(false);
      setRun(r.run_id);
      emit("run-started", { run: r.run_id, workflow: id });
    });

  if (wf.error || doc.error) return <ErrorBox error={wf.error ?? doc.error} />;
  if (!wf.data || !def) return <div className="empty">Loading…</div>;
  const current = wf.data.versions.find((x) => x.version === v);
  const sel = def.steps.find((s) => s.id === selected);
  return (
    <div className="tk-body">
      <div className="toolbar">
        <button onClick={onBack}>← All</button>
        <h1 className="grow" style={{ margin: 0 }}>
          {wf.data.workflow.name}
        </h1>
        {current && <Badge value={dirty ? "draft" : current.state} />}
        {can("workflow.edit") && (
          <>
            <button onClick={() => void validate()} disabled={act.busy}>
              Validate
            </button>
            <button onClick={() => void act.run(save)} disabled={act.busy || !dirty}>
              Save
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
            Run
          </button>
        )}
      </div>
      <ErrorBox error={act.error} />
      {notice && (
        <div className="notice" role="status" onClick={() => setNotice("")}>
          {notice}
        </div>
      )}
      {problems.length > 0 && (
        <div className="error">
          <ul>
            {problems.map((p, i) => (
              <li key={i}>
                <code>{p.path}</code> {p.message}
              </li>
            ))}
          </ul>
        </div>
      )}
      {starting && (
        <div className="card" role="dialog" aria-label="Start a run">
          <Field label="Input (JSON)">
            <JsonInput value={input} onChange={setInput} rows={4} />
          </Field>
          <div className="toolbar">
            <button className="primary" onClick={() => void startRun()} disabled={act.busy}>
              Start run
            </button>
            <button onClick={() => setStarting(false)}>Cancel</button>
          </div>
        </div>
      )}
      {run && <RunView client={client} run={run} emit={emit} onClose={() => setRun(undefined)} />}
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
          </ReactFlow>
        </div>
        <div className="side">
          {can("workflow.edit") && (
            <>
              <div className="hint">Add a step{selected ? ` after ${selected}` : ""}:</div>
              <div className="palette">
                {palette.map((t) => (
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
              connectors={catalogue.data?.connectors ?? []}
              onChange={(s) => editSteps(def.steps.map((x) => (x.id === sel.id ? s : x)))}
              onRename={(to) => {
                if (def.steps.some((s) => s.id === to)) return false;
                editSteps(renameStep(def.steps, sel.id, to));
                setSelected(to);
                return true;
              }}
              onDelete={() => {
                editSteps(removeStep(def.steps, sel.id));
                setSelected(null);
              }}
            />
          ) : (
            <div className="hint">Select a step to edit it.</div>
          )}
        </div>
      </div>
    </div>
  );
}

const ENDED = new Set(["completed", "failed", "cancelled"]);

/** A run, live: its events streamed with fetch until it ends. */
export function RunView({ client, run, emit, onClose }: { client: EmbedClient; run: string; emit: Emit; onClose?: () => void }) {
  const [summary, setSummary] = useState<RunSummary>();
  const [events, setEvents] = useState<RunEvent[]>([]);
  const [error, setError] = useState<unknown>();
  useEffect(() => {
    const ctl = new AbortController();
    let last = 0;
    const add = (e: RunEvent) => {
      if (e.seq <= last) return;
      last = e.seq;
      setEvents((xs) => [...xs, e]);
    };
    const refresh = () => client.get<{ run: RunSummary; events: RunEvent[] }>(`/runs/${run}`);
    void (async () => {
      try {
        let doc = await refresh();
        setSummary(doc.run);
        doc.events.forEach(add);
        if (!ENDED.has(doc.run.status)) {
          await client.streamRun(run, add, ctl.signal);
          if (ctl.signal.aborted) return;
          doc = await refresh();
          doc.events.forEach(add);
          setSummary(doc.run);
        }
        emit(doc.run.status === "completed" ? "run-completed" : "run-ended", { run, status: doc.run.status });
      } catch (e) {
        if (!ctl.signal.aborted) setError(e);
      }
    })();
    return () => ctl.abort();
  }, [client, run, emit]);
  const rows = useMemo(() => timeline(events), [events]);
  const ended = !!summary && ENDED.has(summary.status);
  return (
    <div className="card" data-testid="taskiem-run">
      <div className="toolbar">
        <h2 className="grow" style={{ margin: 0 }}>
          Run {summary ? <Badge value={summary.status} /> : null} {!ended && <span className="live-dot hint">live</span>}
        </h2>
        {onClose && <button onClick={onClose}>Close</button>}
      </div>
      <ErrorBox error={error} />
      {summary && (
        <RunCanvas
          workflow={summary.workflow_id}
          version={summary.version}
          rows={rows}
          ended={ended}
          load={(wf, v) => client.get(`/workflows/${wf}/versions/${v}`)}
        />
      )}
      <div className="timeline">
        {rows.map((r) => (
          <div key={r.id} className={`step ${r.status}`}>
            <strong>{r.id}</strong> <Badge value={r.status} /> <span className="hint">{fmtTime(r.lastAt)}</span>
            {r.error?.message && <div className="hint">{r.error.message}</div>}
          </div>
        ))}
      </div>
    </div>
  );
}

function Runs({ client, emit }: { client: EmbedClient; emit: Emit }) {
  const list = useLoad(() => client.get<{ runs: RunSummary[] }>("/runs"), [client]);
  const [open, setOpen] = useState<string>();
  return (
    <div className="tk-body">
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Runs
        </h1>
        <button onClick={list.reload}>Refresh</button>
      </div>
      <ErrorBox error={list.error} />
      {open && <RunView client={client} run={open} emit={emit} onClose={() => setOpen(undefined)} />}
      {list.data && list.data.runs.length === 0 && <div className="empty">No runs yet.</div>}
      {list.data && list.data.runs.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Automation</th>
              <th>Status</th>
              <th>Started</th>
            </tr>
          </thead>
          <tbody>
            {list.data.runs.map((r) => (
              <tr key={r.id} className="clickable" onClick={() => setOpen(r.id)}>
                <td>{r.workflow}</td>
                <td>
                  <Badge value={r.status} />
                </td>
                <td>{fmtTime(r.started_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
