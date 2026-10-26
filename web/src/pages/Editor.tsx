import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { ApiError, get, post, put, type CapabilityInfo, type ConnectorInfo, type JSONSchema, type MergeConflict, type Problem, type RunSummary, type TriggerInfo, type VersionInfo, type WorkflowSummary, type EmailEndpoint } from "../api";
import { useAdvanced, useAuth } from "../auth";
import { Icon } from "../icons";
import { SchedulePicker } from "../canvas/PlainPickers";
import { FormTriggerEditor } from "../canvas/FormEditor";
import { EmailAddressCard, EmailTriggerSettings } from "../canvas/EmailTrigger";
import { WebhookListener, WebhookTriggerSettings } from "../canvas/WebhookTrigger";
import { connectIn, disconnectIn, emptyPaths, forSaving, insertStep, placed, removeAnywhere, renameAnywhere, updateAnywhere, type InsertAt } from "../lib/flow";
import { merge } from "../lib/schema";
import { triggerWords } from "../lib/home";
import { runLook } from "../lib/lists";
import { EnvSelect, useEnvironments, type Deployment } from "../environments";
import { Badge, ErrorBox, Field, Item, Items, JsonInput, Modal, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { AIBuilder } from "./AIBuild";
import { resolveSchema } from "../lib/values";
import { SchemaForm } from "../forms/SchemaForm";
import { NeedsList } from "./Readiness";
import { ProblemLine } from "../forms/ProblemLine";
import type { ReadyNeed, Readiness } from "../lib/readiness";
import type { CallableWorkflow, Template } from "../lib/templates";
import { Canvas, type TryResult } from "../builder/Canvas";
import { Picker } from "../builder/Picker";
import { StepWindow } from "../builder/StepWindow";
import { PhoneSteps } from "../builder/PhoneSteps";
import { catalog, idFrom } from "../builder/catalog";
import { TRIGGER_CHOICES } from "../builder/triggers";
import { stepTitle } from "../builder/look";
import { sourcesAnywhere, withStarted } from "../builder/sources";

interface Loaded {
  workflow: WorkflowSummary;
  versions: VersionInfo[];
  deployments: Deployment[];
  /** Made from a template another now replaces. */
  upgrade?: { template: string; title: string };
  /** Templates its template steps use that the library has changed since the latest version was saved (decision 0047). */
  template_updates?: { template: string; title: string; changes: string[] }[];
}
interface VersionDoc {
  version: VersionInfo;
  definition: WorkflowDefinition;
  layout?: Record<string, unknown>;
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
  const capabilities = useLoad(() => get<{ capabilities: CapabilityInfo[] }>("/v1/capabilities").catch(() => ({ capabilities: [] as CapabilityInfo[] })), []);
  const callables = useLoad(() => get<{ templates: Template[] }>("/v1/templates?callable=true"), []);
  const ownCallables = useLoad(() => get<{ workflows: CallableWorkflow[] }>("/v1/workflows/callable"), []);
  const version = Number(params.get("v")) || wf.data?.workflow.latest_version || 0;
  const doc = useLoad(() => (version ? get<VersionDoc>(`/v1/workflows/${id}/versions/${version}`) : Promise.resolve(undefined)), [id, version]);

  const [def, setDef] = useState<WorkflowDefinition>();
  const [layout, setLayout] = useState<Record<string, unknown>>({});
  const [problems, setProblems] = useState<Problem[]>([]);
  // A publish refused for what the version still needs.
  const [needs, setNeeds] = useState<{ version: number; needs: ReadyNeed[] }>();
  const [selected, setSelected] = useState<string | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [picking, setPicking] = useState<{ at: InsertAt; where: string } | null>(null);
  const [dirty, setDirty] = useState(false);
  // ?tab=settings opens on when it runs (a new form opens on its questions).
  const [tab, setTab] = useState<"build" | "runs" | "settings" | "code" | "json">(() => (params.get("tab") === "settings" ? "settings" : "build"));
  const [notice, setNotice] = useState("");
  const [starting, setStarting] = useState(false);
  const [merge, setMerge] = useState<{ conflicts: MergeConflict[]; latest: number }>();
  const [ai, setAI] = useState(false);
  const [fitKey, setFitKey] = useState(0);
  // The last test with sample data, and the outputs kept for the next ones.
  const [result, setResult] = useState<TryResult>();
  const [kept, setKept] = useState<Record<string, unknown>>({});
  const testing = useAction();
  const act = useAction();
  // Undo and redo: the definition and the layout as they were before each change.
  const past = useRef<{ def: WorkflowDefinition; layout: Record<string, unknown> }[]>([]);
  const future = useRef<{ def: WorkflowDefinition; layout: Record<string, unknown> }[]>([]);
  const [, setTick] = useState(0);

  useEffect(() => {
    if (!doc.data) return;
    setDef(doc.data.definition);
    setLayout((doc.data.layout ?? {}) as Record<string, unknown>);
    setProblems(doc.data.problems);
    setDirty(false);
    past.current = [];
    future.current = [];
  }, [doc.data]);

  const remember = () => {
    if (!def) return;
    past.current = [...past.current.slice(-49), { def, layout }];
    future.current = [];
  };
  const edit = useCallback((next: WorkflowDefinition) => {
    setDef(next);
    setDirty(true);
  }, []);
  const change = (next: WorkflowDefinition) => {
    remember();
    edit(next);
  };
  const editSteps = (steps: Step[]) => def && change({ ...def, steps });
  const undo = () => {
    const prev = past.current.pop();
    if (!prev || !def) return;
    future.current.push({ def, layout });
    setDef(prev.def);
    setLayout(prev.layout);
    setDirty(true);
    setTick((t) => t + 1);
  };
  const redo = () => {
    const next = future.current.pop();
    if (!next || !def) return;
    past.current.push({ def, layout });
    setDef(next.def);
    setLayout(next.layout);
    setDirty(true);
    setTick((t) => t + 1);
  };
  // Ctrl or Cmd with Z undoes, with Shift (or Y) redoes; not while typing.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (!(e.metaKey || e.ctrlKey) || t?.closest("input, textarea, select, [contenteditable=true]")) return;
      const k = e.key.toLowerCase();
      if (k === "z" && !e.shiftKey) {
        e.preventDefault();
        undo();
      } else if ((k === "z" && e.shiftKey) || k === "y") {
        e.preventDefault();
        redo();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  // Layout changes are saved on the current version without a new version.
  const layoutTimer = useRef<ReturnType<typeof setTimeout>>(undefined);
  const saveLayout = (l: Record<string, unknown>) => {
    clearTimeout(layoutTimer.current);
    layoutTimer.current = setTimeout(() => {
      if (version && can("workflow.edit") && !dirty) void put(`/v1/workflows/${id}/versions/${version}/layout`, l).catch(() => undefined);
    }, 600);
  };

  // An email trigger's card shows the address mail starts it at.
  const emailTrigger = doc.data?.definition.trigger.type === "email";
  const emailEndpoints = useLoad(
    () => (emailTrigger ? get<{ endpoints: EmailEndpoint[] }>(`/v1/workflows/${id}/email`) : Promise.resolve({ endpoints: [] })),
    [id, emailTrigger],
  );
  const emailAddress = emailEndpoints.data?.endpoints.find((e) => e.environment === "prod" && e.status === "live")?.address || undefined;
  const invalid = useMemo(() => (def ? problemSteps(def, problems) : new Set<string>()), [def, problems]);
  const conns = connectors.data?.connectors ?? [];
  const picks = useMemo(() => catalog(conns, capabilities.data?.capabilities ?? []), [conns, capabilities.data]);

  // A save names the version it started from; if someone saved since, the
  // server merges the two edits, or reports conflicts to resolve.
  const save = async (resolutions?: Record<string, "ours" | "theirs">): Promise<number> => {
    if (!def) return version;
    if (!dirty) return version;
    let r: { version: number; problems: Problem[]; merged: boolean };
    try {
      r = await post(`/v1/workflows/${id}/versions`, { definition: forSaving(def), layout, parent_digest: doc.data?.version.digest, resolutions });
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
      const r = await post<{ valid: boolean; problems: Problem[] }>("/v1/validate", { definition: def && forSaving(def) });
      setProblems(r.problems);
      setNotice(r.valid ? "Valid: this version can be published." : "");
    }, false);

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
    }, "Published");

  // Test with sample data: the draft as it is now, nothing sent.
  const test = () =>
    testing.run(async () => {
      if (!def) return;
      if (def.steps.length === 0) throw new Error("Add a step first: then test it with sample data.");
      const gaps = emptyPaths(def.steps, (s) => stepTitle(s, conns));
      if (gaps.length) throw new Error(`Add a step to ${gaps.join(" and ")} first: a path can't be empty. Or delete what you don't need.`);
      setResult(await post<TryResult>(`/v1/workflows/${id}/try`, { definition: forSaving(def), kept }));
    }, false);

  if (wf.error || doc.error) return <ErrorBox error={wf.error ?? doc.error} />;
  if (!wf.data || !def) return <Skeleton />;
  const current = wf.data.versions.find((v) => v.version === version);
  const managed = wf.data.workflow.git_path;
  const readOnly = !can("workflow.edit") || !!managed;
  const all = placed(def.steps);
  const taken = new Set(all.map((p) => p.step.id));
  const title = (sid: string) => {
    const s = all.find((p) => p.step.id === sid)?.step;
    return s ? stepTitle(s, conns) : sid;
  };
  const whereWords = (at: InsertAt) =>
    at.after && at.before ? `Between ${title(at.after)} and ${title(at.before)}` : at.after ? `After ${title(at.after)}` : at.before ? `Before ${title(at.before)}` : at.list ? "The first step of this path" : "The first step";
  const add = (at: InsertAt) => setPicking({ at, where: whereWords(at) });
  const addAtEnd = () => {
    const followed = new Set(def.steps.flatMap((s) => s.needs ?? []));
    const last = [...def.steps].reverse().find((s) => !followed.has(s.id));
    add(last ? { list: "", after: last.id } : { list: "" });
  };
  const openIdx = open ? all.findIndex((p) => p.step.id === open) : -1;
  const openStep = openIdx >= 0 ? all[openIdx]?.step : undefined;
  const neighbour = (i: number) => {
    const s = all[i]?.step;
    return s && { id: s.id, title: stepTitle(s, conns) };
  };
  const status = dirty ? <span className="badge warn">Unsaved changes</span> : current?.state === "published" ? <span className="badge published">Live</span> : current && <Badge value={current.state} />;
  const tabs = (advanced ? (["build", "runs", "settings", "code", "json"] as const) : (["build", "runs", "settings"] as const)).map((t) => [t, { build: "Build", runs: "Runs", settings: "Settings", code: "Code", json: "JSON" }[t]] as const);

  return (
    <div className="builder" data-testid="builder">
      <header className="builder-bar">
        <Link to="/workflows" className="icon-round" aria-label="Back to Workflows">
          <Icon name="back" />
        </Link>
        <div className="builder-name">
          <h1>{wf.data.workflow.name}</h1>
          {status}
        </div>
        <div className="builder-tabs" role="tablist" aria-label="Workflow">
          {tabs.map(([t, words]) => (
            <button key={t} type="button" role="tab" aria-selected={tab === t} className={tab === t ? "active" : ""} onClick={() => setTab(t)}>
              {words}
            </button>
          ))}
        </div>
        <span className="grow" />
        {!readOnly && (
          <>
            <button type="button" className="icon-square" aria-label="Undo" title="Undo (Ctrl+Z)" disabled={past.current.length === 0} onClick={undo}>
              <Icon name="undo" />
            </button>
            <button type="button" className="icon-square" aria-label="Redo" title="Redo (Ctrl+Shift+Z)" disabled={future.current.length === 0} onClick={redo}>
              <Icon name="redo" />
            </button>
            {dirty ? (
              <button type="button" onClick={() => void act.run(() => save(), "Saved")} disabled={act.busy}>
                Save draft
              </button>
            ) : (
              <span className="hint builder-saved">Saved</span>
            )}
          </>
        )}
        {(advanced || wf.data.versions.length > 1) && (
          <select
            aria-label="Version"
            className="builder-version"
            value={version}
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
        {!readOnly && (
          <button type="button" className="pill" onClick={() => void test()} disabled={testing.busy}>
            <Icon name="play" />
            {testing.busy ? "Testing…" : "Test workflow"}
          </button>
        )}
        {can("run.start") && wf.data.workflow.active_version && (
          <button type="button" onClick={() => setStarting(true)} disabled={act.busy}>
            Start run
          </button>
        )}
        {can("workflow.publish") && !managed && (
          <button type="button" className="primary" onClick={() => void publish()} disabled={act.busy || (!dirty && current?.state === "published")}>
            Publish
          </button>
        )}
      </header>

      <div className="builder-notes">
        <ErrorBox error={act.error ?? testing.error} />
        {needs && (
          <div className="notice">
            <p>Version {needs.version} is saved, but not published yet: fix these, then publish again.</p>
            <NeedsList
              needs={needs.needs}
              onChanged={() =>
                void get<Readiness>(`/v1/workflows/${id}/versions/${needs.version}/readiness`).then((r) => setNeeds(r.ready ? undefined : { version: needs.version, needs: r.needs }))
              }
            />
          </div>
        )}
        {managed && (
          <div className="notice" role="status">
            This workflow is managed in Git (<code>{managed}</code>). Change it in the repository: a push to the connected branch deploys it once its tests pass.
          </div>
        )}
        {notice && (
          <div className="notice" role="status" onClick={() => setNotice("")}>
            {notice}
          </div>
        )}
        {/* What the last save found; an edit since may have fixed it. */}
        {problems.length > 0 && def.steps.length > 0 && !dirty && (
          <details className="error builder-problems">
            <summary>
              {problems.length} {problems.length > 1 ? "things need" : "thing needs"} fixing before it can be published
            </summary>
            <ul>
              {problems.map((p, i) => (
                <li key={i}>
                  <ProblemLine p={p} onStep={(sid) => (setTab("build"), setOpen(sid))} />
                </li>
              ))}
            </ul>
          </details>
        )}
      </div>

      {tab === "build" && (
        <div className="builder-canvas">
          {result && (
            <div className="test-banner" role="status">
              <span className={`test-mark ${result.status === "completed" ? "ok" : "bad"}`} aria-hidden="true">
                <Icon name={result.status === "completed" ? "check" : "close"} />
              </span>
              <span>
                <strong>{result.status === "completed" ? "Tested with sample data" : "The test stopped"}</strong>{" "}
                <span className="hint">· {testWords(result)}</span>
              </span>
              <button type="button" className="pill small" onClick={() => setResult(undefined)}>
                Clear
              </button>
            </div>
          )}
          {!readOnly && (
            <div className="builder-actions">
              <button type="button" className="pill" onClick={() => setAI(true)} disabled={dirty} title={dirty ? "Save your changes first" : undefined}>
                <Icon name="sparkle" />
                Change it with AI
              </button>
              <button type="button" className="pill primary" onClick={addAtEnd}>
                <Icon name="plus" />
                Add a step
              </button>
            </div>
          )}
          <Canvas
            def={def}
            layout={layout}
            connectors={conns}
            triggerWords={triggerWords(def.trigger)}
            triggerDetail={emailAddress}
            readOnly={readOnly}
            invalid={invalid}
            result={result}
            selected={selected}
            onSelect={setSelected}
            onOpen={(sid) => (setSelected(sid), setOpen(sid))}
            onOpenTrigger={() => setTab("settings")}
            onAdd={add}
            onRemove={(sid) => {
              editSteps(removeAnywhere(def.steps, sid));
              if (open === sid) setOpen(null);
            }}
            onConnect={(source, target) => {
              const next = connectIn(def.steps, source, target);
              if (!next) setNotice("Those two can't be joined: join steps in the same path, without going round in a circle.");
              else editSteps(next);
            }}
            onDisconnect={(source, target) => editSteps(disconnectIn(def.steps, source, target))}
            onLayout={(l, done) => {
              if (done) remember();
              setLayout(l);
              if (done) saveLayout(l);
            }}
            fitKey={fitKey}
          />
          <PhoneSteps def={def} connectors={conns} triggerWords={triggerWords(def.trigger)} result={result} readOnly={readOnly} onOpen={setOpen} onOpenTrigger={() => setTab("settings")} onAdd={add} onTest={() => void test()} testing={testing.busy} />
          {picking && (
            <Picker
              items={picks}
              where={picking.where}
              onClose={() => setPicking(null)}
              onDescribe={dirty ? undefined : () => (setPicking(null), setAI(true))}
              onPick={(p) => {
                const sid = idFrom(p.base, taken);
                editSteps(insertStep(def.steps, picking.at, p.make(sid)));
                setPicking(null);
                setFitKey((k) => k + 1);
                setSelected(sid);
                setOpen(sid);
              }}
            />
          )}
        </div>
      )}
      {tab === "runs" && <RunsTab workflow={id} published={!!wf.data.workflow.active_version} canStart={can("run.start")} onStart={() => setStarting(true)} />}
      {tab === "settings" && (
        <div className="builder-page">
          <Deployments workflow={id} deployments={wf.data.deployments} canPromote={can("workflow.publish")} onChange={wf.reload} />
          {wf.data.upgrade && (
            <div className="notice" role="status">
              This was made from a template a newer one replaces: <strong>{wf.data.upgrade.title}</strong>.{" "}
              <Link to={`/start/guide?template=${encodeURIComponent(wf.data.upgrade.template)}`}>Set it up</Link>, then turn this one off. This one keeps working until you do.
            </div>
          )}
          {(wf.data.template_updates ?? []).map((u) => (
            <div className="notice" role="status" key={u.template}>
              <strong>{u.title}</strong> has an update. This workflow keeps the version it was saved with; save it again to take the update, then publish.
              <details>
                <summary>What changed ({u.changes.length})</summary>
                <ul className="plain-list mono">
                  {u.changes.map((c) => (
                    <li key={c}>{c}</li>
                  ))}
                </ul>
              </details>
            </div>
          ))}
          {advanced && (
            <p className="toolbar">
              <button onClick={() => void validate()} disabled={act.busy}>
                Validate
              </button>
            </p>
          )}
          <SettingsTab workflow={id} def={def} onChange={change} connectors={conns} problems={problems} />
          <Endpoints workflow={id} />
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
        </div>
      )}
      {advanced && tab === "code" && (
        <div className="builder-page">
          <CodeTab
            def={def}
            onApply={(next, probs) => {
              change(next);
              setProblems(probs);
            }}
          />
        </div>
      )}
      {advanced && tab === "json" && (
        <div className="builder-page">
          <div className="card">
            <p className="hint">The whole definition (wd/v1). Edits apply as you type, once the JSON is valid.</p>
            <JsonInput value={def} onChange={(v) => v && typeof v === "object" && change(v as WorkflowDefinition)} rows={30} />
          </div>
        </div>
      )}
      {openStep && (
        <StepWindow
          key={openStep.id}
          step={openStep}
          connectors={conns}
          sources={withStarted(sourcesAnywhere(def, openStep.id, conns, [...(callables.data?.templates ?? []), ...(ownCallables.data?.workflows ?? [])] as Parameters<typeof sourcesAnywhere>[3]), def.trigger, result)}
          problems={stepProblemsOf(def, problems, openStep.id)}
          result={result}
          testing={testing.busy}
          kept={openStep.id in kept}
          readOnly={readOnly}
          place={{
            index: openIdx + 1,
            count: all.length,
            prev: neighbour(openIdx - 1),
            next: neighbour(openIdx + 1),
          }}
          onChange={(s) => editSteps(updateAnywhere(def.steps, openStep.id, () => s))}
          onRename={(to) => {
            if (taken.has(to)) return false;
            editSteps(renameAnywhere(def.steps, openStep.id, to));
            if (layout[openStep.id]) setLayout({ ...layout, [to]: layout[openStep.id] });
            setOpen(to);
            setSelected(to);
            return true;
          }}
          onDelete={() => {
            editSteps(removeAnywhere(def.steps, openStep.id));
            setOpen(null);
          }}
          onGo={setOpen}
          onTest={() => void test()}
          onKeep={(keep) => {
            const rest = Object.fromEntries(Object.entries(kept).filter(([k]) => k !== openStep.id));
            setKept(keep && result ? { ...rest, [openStep.id]: result.outputs[openStep.id] } : rest);
          }}
          onClose={() => setOpen(null)}
        />
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
          onResolve={(res) => void act.run(() => save(res), "Saved")}
        />
      )}
    </div>
  );
}

/** A test's outcome in a few words: how many steps ran, and which stopped. */
function testWords(r: TryResult): string {
  const states = Object.values(r.steps);
  const ran = states.filter((s) => s === "completed").length;
  const failed = Object.keys(r.errors);
  if (r.status !== "completed" && failed.length) return `${failed.length === 1 ? "a step" : `${failed.length} steps`} stopped. Open it to see why. Nothing was sent.`;
  if (r.status === "waiting" || r.status === "running") return `${ran} ${ran === 1 ? "step" : "steps"} ran, then it waits. Nothing was sent.`;
  return `${ran} ${ran === 1 ? "step" : "steps"} ran with a sample. Nothing was sent.`;
}

/** This workflow's runs, newest first, and starting one. */
function RunsTab({ workflow, published, canStart, onStart }: { workflow: string; published: boolean; canStart: boolean; onStart: () => void }) {
  const runs = useLoad(() => get<{ runs: RunSummary[] }>(`/v1/runs?workflow=${workflow}&limit=50`), [workflow]);
  return (
    <div className="builder-page">
      <div className="toolbar">
        <h2 className="grow" style={{ margin: 0 }}>
          Its runs
        </h2>
        {canStart && published && (
          <button type="button" className="primary" onClick={onStart}>
            Start a run
          </button>
        )}
      </div>
      {!published && <p className="hint">It runs once it is published. Until then, test it with sample data on Build.</p>}
      <ErrorBox error={runs.error} />
      {!runs.data && !runs.error && <Skeleton rows={3} />}
      {runs.data && runs.data.runs.length === 0 && published && <p className="hint">No runs yet.</p>}
      {runs.data && runs.data.runs.length > 0 && (
        <Items label="Its runs">
          {runs.data.runs.map((r) => {
            const look = runLook(r.status);
            return <Item key={r.id} to={`/runs/${r.id}`} icon={look.icon} tone={look.tone} title={fmtTime(r.started_at)} side={<Badge value={r.status} />} sub={`Version ${r.version}`} testid="workflow-run" />;
          })}
        </Items>
      )}
    </div>
  );
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

function SettingsTab({ workflow, def, onChange, connectors, problems }: { workflow: string; def: WorkflowDefinition; onChange: (d: WorkflowDefinition) => void; connectors: ConnectorInfo[]; problems: Problem[] }) {
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
            const choice = TRIGGER_CHOICES.find((c) => c.type === type);
            if (choice) return onChange(choice.apply(def));
            if (type === "connector_event") onChange({ ...def, trigger: { type, config: { connector: "paystack@1", trigger: "transfer_event" } } });
            else if (type === "invoice") onChange({ ...def, trigger: { type, config: { event: "paid" } } });
          }}
        >
          {TRIGGER_CHOICES.map((c) => (
            <option key={c.type} value={c.type}>
              {advanced ? `${c.title} (${c.type})` : c.title}
            </option>
          ))}
          <option value="invoice">{advanced ? "An invoice is paid (invoice)" : "An invoice is paid"}</option>
          <option value="connector_event">{advanced ? "Something happens in a service (connector_event)" : "Something happens in a service"}</option>
          {![...TRIGGER_CHOICES.map((c) => c.type), "invoice", "connector_event"].includes(t.type) && <option value={t.type}>{triggerWords(t)}</option>}
        </select>
      </Field>
      {t.type === "webhook" && !advanced && <p className="hint">Another system starts it at its web address (under Web addresses). Turn on Advanced in Settings to change how.</p>}
      {t.type === "webhook" && advanced && (
        <>
          <WebhookTriggerSettings wdId={def.id} config={cfg} onChange={setCfg} />
          <Field label="Deduplicate on" hint="Expression; deliveries with the same value start one run. Default: the body's hash (for a GET, its query).">
            <input className="mono" value={String(cfg.dedup ?? "")} placeholder="=trigger.body.id" onChange={(e) => setCfg({ dedup: e.target.value })} />
          </Field>
          <WebhookListener workflow={workflow} config={cfg} onUseSchema={(schema) => onChange({ ...def, inputs: { ...(def.inputs ?? {}), schema } } as WorkflowDefinition)} />
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
      {t.type === "email" && <EmailTriggerSettings config={cfg} onChange={setCfg} />}
      {t.type !== "form" && t.type !== "email" && advanced && (
        <>
          <h2>Inputs schema</h2>
          <p className="hint">JSON Schema for trigger.body; fields marked "x-pii" are encrypted per data subject.</p>
          <JsonInput value={def.inputs?.schema} onChange={(v) => onChange({ ...def, inputs: v === undefined ? undefined : { schema: v as object } } as WorkflowDefinition)} rows={8} label="Inputs schema" />
          <h2>What it gives back</h2>
          <p className="hint">
            Outputs make this workflow usable as a step of your other workflows once it is published: a list of {"{name, type, title, description, value}"}, each value an expression over this workflow's steps (steps.&lt;id&gt;.output). It needs an inputs schema too.
          </p>
          <JsonInput value={def.outputs} onChange={(v) => onChange({ ...def, outputs: v === undefined ? undefined : (v as WorkflowDefinition["outputs"]) } as WorkflowDefinition)} rows={6} label="What it gives back" />
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
    {t.type === "email" && <EmailAddressCard workflow={workflow} />}
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
  const advanced = useAdvanced();
  if (error) return <ErrorBox error={error} />;
  if (!data) return null;
  if (data.triggers.length === 0)
    return advanced ? <p className="hint">The published version is started manually or through the API: POST /v1/workflows/{workflow}/runs.</p> : null;
  const newSecret = (env: string) =>
    confirm(`Make a new signing secret for ${env}? It replaces the current one at once: whoever sends to this webhook must switch to the new one.`) &&
    void act.run(async () => setMade(await post<SigningSecret>(`/v1/workflows/${workflow}/signing-secret`, { environment: env })), false);
  return (
    <>
      <h2>Where it starts</h2>
      {made && (
        <div className="card" role="status">
          <p>
            The new {{ bearer: "token", basic: "user name and password (user:password)", jwt: "key to sign tokens with (HS256)" }[made.auth] ?? "signing secret"} for {made.environment}. Copy it now and give it to the sender: it is not shown again.
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
                {t.email && (
                  <>
                    <code>{t.email}</code>
                    <div className="hint">Email sent or forwarded to this address starts the workflow (see When it runs).</div>
                  </>
                )}
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
                  }, "Promoted")
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
            }, "Run started")
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
    }, false);
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
            }, false)
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
