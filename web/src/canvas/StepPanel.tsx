// Edits one top-level step. Nested steps (branch paths, foreach bodies,
// on_error) are edited as JSON inside their parent.
import { useState } from "react";
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { get, type CapabilityInfo, type ConnectorInfo, type Problem, type WorkflowSummary } from "../api";
import { FormStepEditor } from "./FormEditor";
import { RolePicker } from "../forms/RolePicker";
import { ConnectionPicker, type ConnectionName } from "../forms/ConnectionPicker";
import { capableConnections } from "../lib/capabilities";
import type { FormStepConfig } from "../lib/forms";
import { SchemaForm } from "../forms/SchemaForm";
import { merge } from "../lib/schema";
import { Field, JsonInput, useLoad } from "../ui";
import { useAdvanced } from "../auth";
import { isExpression } from "../lib/schema";
import type { Source } from "../lib/plaincanvas";
import { DurationPicker, RulesBuilder, ValuePicker } from "./PlainPickers";
import { ParamField } from "../pages/Templates";
import { effectWords, toParams, type CallableWorkflow, type Template } from "../lib/templates";

const ID = /^[a-z][a-z0-9_]{0,62}$/;

interface Props {
  step: Step;
  others: string[];
  connectors: ConnectorInfo[];
  onChange: (s: Step) => void;
  onRename: (id: string) => boolean;
  onDelete: () => void;
  /** The validator's problems with this step, at paths relative to it (/config/...). */
  problems?: Problem[];
  /** The values this step can use from what came before (no-technical-steps N8). */
  sources?: Source[];
  /** Step names by id, for "Runs after". */
  names?: Record<string, string>;
}

/** What an owner reads where a step's technical settings would be. */
function ByHand() {
  return <p className="hint">This part was set up by hand. Turn on Advanced in Settings to change it.</p>;
}

/** A flat object of fixed values (a transform's output, an approval's subject), editable without JSON. */
function flatValues(v: unknown): Record<string, string | number | boolean> | null {
  if (v === undefined || v === null) return {};
  if (typeof v !== "object" || Array.isArray(v)) return null;
  const out: Record<string, string | number | boolean> = {};
  for (const [k, x] of Object.entries(v as Any)) {
    if (typeof x === "string" || typeof x === "number" || typeof x === "boolean") out[k] = x;
    else return null;
  }
  return out;
}

type Any = Record<string, unknown>;


const JS_TEMPLATE = "export default (input: Record<string, unknown>) => {\n  return { ok: true };\n};\n";
const PYTHON_TEMPLATE = "def main(input, host):\n    return {\"ok\": True}\n";

export function StepPanel({ step, others, connectors, onChange, onRename, onDelete, problems = [], sources = [], names = {} }: Props) {
  const advanced = useAdvanced();
  const [id, setId] = useState(step.id);
  const [idError, setIdError] = useState("");
  const set = (patch: Any) => onChange(merge(step, patch));
  const cfg = ((step as { config?: Any }).config ?? {}) as Any;
  const setCfg = (patch: Any) => set({ config: merge(cfg, patch) });
  return (
    <div key={step.id}>
      <div className="toolbar">
        <h2 className="grow" style={{ margin: 0 }}>
          {step.type} step
        </h2>
        <button className="danger" onClick={onDelete}>
          Delete
        </button>
      </div>
      {advanced && (
      <Field label="ID" hint={idError || "Lower-case letters, digits and _. Expressions refer to steps.<id>."}>
        <input
          className="mono"
          value={id}
          onChange={(e) => setId(e.target.value)}
          onBlur={() => {
            if (id === step.id) return;
            if (!ID.test(id)) return setIdError("Invalid id.");
            if (!onRename(id)) return setIdError("That id is taken.");
            setIdError("");
          }}
        />
      </Field>
      )}
      <Field label="Name">
        <input value={step.name ?? ""} onChange={(e) => set({ name: e.target.value })} />
      </Field>
      {others.length > 0 && (
        <Field label="Runs after">
          <div>
            {others.map((o) => (
              <label key={o} className="inline">
                <input
                  type="checkbox"
                  checked={step.needs?.includes(o) ?? false}
                  onChange={(e) => {
                    const needs = e.target.checked ? [...(step.needs ?? []), o] : (step.needs ?? []).filter((n) => n !== o);
                    set({ needs: needs.length ? needs : undefined });
                  }}
                />
                {names[o] || o.replace(/_/g, " ")}
              </label>
            ))}
          </div>
        </Field>
      )}
      {advanced ? (
        <Field label="Run only when" hint="An expression, e.g. =steps.approve.output.decision == 'approved'">
          <input className="mono" value={step.when ?? ""} placeholder="=" onChange={(e) => set({ when: e.target.value })} />
        </Field>
      ) : (
        <Field label="Run only when">
          <RulesBuilder sources={sources} value={step.when ?? ""} onChange={(when) => set({ when: when || undefined })} />
        </Field>
      )}

      <h3>Configuration</h3>
      {step.type === "connector" && <ConnectorConfig step={step} connectors={connectors} set={set} advanced={advanced} sources={sources} />}
      {step.type === "capability" && <CapabilityConfig step={step} connectors={connectors} set={set} advanced={advanced} sources={sources} />}
      {step.type === "template" && <TemplateConfig step={step} set={set} advanced={advanced} sources={sources} />}
      {step.type === "subflow" && <SubflowConfig step={step} set={set} advanced={advanced} sources={sources} />}
      {step.type === "ai" && <AIConfig step={step} set={set} advanced={advanced} />}
      {!advanced && ["http", "code", "signal", "branch", "foreach", "parallel", "callback", "respond"].includes(step.type) && <ByHand />}
      {advanced && step.type === "http" && (
        <>
          <div className="row">
            <Field label="Method">
              <select value={String(cfg.method)} onChange={(e) => setCfg({ method: e.target.value })}>
                {["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"].map((m) => (
                  <option key={m}>{m}</option>
                ))}
              </select>
            </Field>
            <Field label="Action class">
              <select value={String(cfg.class ?? "")} onChange={(e) => setCfg({ class: e.target.value })}>
                <option value="">read (GET/HEAD only)</option>
                <option value="idempotent_write">idempotent write</option>
                <option value="reconcilable_write">reconcilable write</option>
                <option value="unsafe_write">unsafe write</option>
              </select>
            </Field>
          </div>
          <Field label="URL" hint="A literal, an expression, or =secrets.name">
            <input className="mono" value={String(cfg.url ?? "")} onChange={(e) => setCfg({ url: e.target.value })} />
          </Field>
          <Field label="Idempotency header" hint="Header that carries the derived idempotency key on writes">
            <input value={String(cfg.idempotency_header ?? "")} onChange={(e) => setCfg({ idempotency_header: e.target.value })} />
          </Field>
          <Field label="Headers (JSON)">
            <JsonInput value={cfg.headers} onChange={(v) => setCfg({ headers: v })} rows={3} />
          </Field>
          <Field label="Body (JSON)">
            <JsonInput value={cfg.body} onChange={(v) => setCfg({ body: v })} rows={4} />
          </Field>
        </>
      )}
      {advanced && step.type === "code" && (
        <>
          <Field label="Language">
            <select
              value={String(cfg.language)}
              onChange={(e) => {
                const language = e.target.value;
                // Switching between JavaScript and Python starts from that language's template.
                const swap = (language === "python") !== (cfg.language === "python");
                setCfg(swap ? { language, source: language === "python" ? PYTHON_TEMPLATE : JS_TEMPLATE } : { language });
              }}
            >
              <option value="typescript">TypeScript</option>
              <option value="javascript">JavaScript</option>
              <option value="python">Python</option>
            </select>
          </Field>
          <Field
            label="Source"
            hint={
              cfg.language === "python"
                ? "Define main(input, host). print() goes to the logs; host.secret, host.now and host.fetch are available. Standard library only."
                : "Export a default function of the step input. host.log, host.secret, host.now and host.fetch are available."
            }
          >
            <textarea className="mono" rows={12} spellCheck={false} value={String(cfg.source ?? "")} onChange={(e) => setCfg({ source: e.target.value })} />
          </Field>
          <Field label="Input (JSON)">
            <JsonInput value={(step as { input?: unknown }).input} onChange={(v) => set({ input: v })} rows={4} />
          </Field>
        </>
      )}
      {step.type === "transform" && advanced && (
        <Field label="Output (JSON; values may be expressions)">
          <JsonInput value={cfg.output} onChange={(v) => setCfg({ output: v === undefined ? null : v })} rows={8} />
        </Field>
      )}
      {step.type === "transform" && !advanced && <FixedValues value={cfg.output} onChange={(output) => setCfg({ output })} />}
      {step.type === "wait" && !advanced && (
        <Field label="Wait for">
          {cfg.until !== undefined ? <ByHand /> : <DurationPicker label="Wait for" value={String(cfg.duration ?? "1h")} onChange={(duration) => set({ config: { duration } })} />}
        </Field>
      )}
      {step.type === "wait" && advanced && (
        <div className="row">
          <Field label="Duration" hint="e.g. 30m, 2h, 1d">
            <input value={String(cfg.duration ?? "")} onChange={(e) => set({ config: e.target.value ? { duration: e.target.value } : { until: cfg.until ?? "=" } })} />
          </Field>
          <Field label="Or until" hint="An expression giving an RFC 3339 time">
            <input className="mono" value={String(cfg.until ?? "")} onChange={(e) => set({ config: e.target.value ? { until: e.target.value } : { duration: "1h" } })} />
          </Field>
        </div>
      )}
      {advanced && step.type === "signal" && (
        <>
          <Field label="Event" hint="Connector events are <connector>:<trigger>, e.g. paystack@1:transfer_event">
            <input className="mono" value={String(cfg.event ?? "")} onChange={(e) => setCfg({ event: e.target.value })} />
          </Field>
          <Field label="Correlation" hint="Expression matched against the event's correlation, e.g. =steps.pay.output.reference">
            <input className="mono" value={String(cfg.correlation ?? "")} onChange={(e) => setCfg({ correlation: e.target.value })} />
          </Field>
          <Field label="Timeout">
            <input value={String(cfg.timeout ?? "")} onChange={(e) => setCfg({ timeout: e.target.value })} />
          </Field>
        </>
      )}
      {step.type === "approval" && (
        <>
          <div className="row">
            <Field label="Who decides" hint="Members with this role, and the business's owners">
              <RolePicker label="Who decides" value={String(cfg.role ?? "")} allowNone onChange={(role) => setCfg({ role: role || undefined })} />
            </Field>
            <Field label="Approvals needed">
              <input type="number" min={1} value={Number(cfg.count ?? 1)} onChange={(e) => setCfg({ count: Number(e.target.value) > 1 ? Number(e.target.value) : undefined })} />
            </Field>
          </div>
          {advanced ? (
            <>
              <div className="row">
                <Field label="Timeout">
                  <input value={String(cfg.timeout ?? "")} onChange={(e) => setCfg({ timeout: e.target.value })} />
                </Field>
                <Field label="On timeout">
                  <input value={String(cfg.on_timeout ?? "")} placeholder="reject | fail | escalate:<role>" onChange={(e) => setCfg({ on_timeout: e.target.value })} />
                </Field>
              </div>
              <Field label="Subject shown to approvers (JSON; values may be expressions)">
                <JsonInput value={cfg.subject} onChange={(v) => setCfg({ subject: v })} rows={5} />
              </Field>
            </>
          ) : (
            <ApprovalPlain cfg={cfg} setCfg={setCfg} sources={sources} />
          )}
        </>
      )}
      {step.type === "form" && (
        <FormStepEditor
          advanced={advanced}
          sources={sources}
          cfg={cfg as FormStepConfig}
          onChange={(c) => set({ config: c })}
          problems={problems.filter((p) => p.path.startsWith("/config")).map((p) => ({ ...p, path: p.path.slice("/config".length) }))}
        />
      )}
      {advanced && step.type === "callback" && (
        <p className="hint">
          The run pauses here until another system calls <code>run.callbacks.{step.id}</code>: send that address with an earlier step. The call's body and query are this step's output.
        </p>
      )}
      {advanced && step.type === "respond" && <p className="hint">The caller of the webhook that started this run gets this status, headers and body. The trigger needs respond: true.</p>}
      {advanced && (step.type === "branch" || step.type === "foreach" || step.type === "parallel" || step.type === "callback" || step.type === "respond") && (
        <Field label="Configuration (JSON)" hint="Nested steps are edited here.">
          <JsonInput value={cfg} onChange={(v) => v && set({ config: v })} rows={14} />
        </Field>
      )}

      {advanced && (step.type === "connector" || step.type === "http" || step.type === "code") && (
        <>
          <h3>Retries</h3>
          <div className="row">
            <Field label="Max attempts after the first">
              <input
                type="number"
                min={0}
                value={step.retry?.max ?? ""}
                onChange={(e) => set({ retry: e.target.value === "" ? undefined : { ...(step.retry ?? {}), max: Number(e.target.value) } })}
              />
            </Field>
            <Field label="First delay">
              <input value={step.retry?.initial ?? ""} placeholder="2s" onChange={(e) => step.retry && set({ retry: { ...step.retry, initial: e.target.value || undefined } })} />
            </Field>
          </div>
        </>
      )}
      {advanced && (
        <>
          <Field label="Step timeout">
            <input value={step.timeout ?? ""} placeholder="e.g. 30s" onChange={(e) => set({ timeout: e.target.value })} />
          </Field>
          <details>
            <summary>Edit this step as JSON</summary>
            <JsonInput value={step} onChange={(v) => v && typeof v === "object" && onChange(v as Step)} rows={14} />
          </details>
        </>
      )}
    </div>
  );
}

/** Named fixed values, each typed (a transform's settings). */
function FixedValues({ value, onChange }: { value: unknown; onChange: (v: Any) => void }) {
  const flat = flatValues(value);
  if (!flat || Object.values(flat).some((v) => isExpression(v))) return <ByHand />;
  return (
    <>
      {Object.entries(flat).map(([k, v]) => (
        <Field key={k} label={k.replace(/_/g, " ")}>
          <input
            value={String(v)}
            onChange={(e) => onChange({ ...flat, [k]: typeof v === "number" && e.target.value.trim() !== "" && !Number.isNaN(Number(e.target.value)) ? Number(e.target.value) : e.target.value })}
          />
        </Field>
      ))}
    </>
  );
}

const ON_TIMEOUT: [string, string][] = [
  ["reject", "Count it as rejected"],
  ["fail", "Stop the workflow"],
  ["escalate", "Ask someone else"],
];

/** An approval's time limit, what happens then, and what approvers see, without JSON. */
function ApprovalPlain({ cfg, setCfg, sources }: { cfg: Any; setCfg: (p: Any) => void; sources: Source[] }) {
  const onTimeout = String(cfg.on_timeout ?? "");
  const escalate = onTimeout.startsWith("escalate:");
  const subject = (cfg.subject ?? {}) as Any;
  const plainSubject = typeof subject === "object" && !Array.isArray(subject) && Object.values(subject).every((v) => typeof v !== "object" || v === null);
  return (
    <>
      <div className="row">
        <Field label="Time to decide">
          <DurationPicker label="Time to decide" allowNone value={String(cfg.timeout ?? "")} onChange={(timeout) => setCfg({ timeout: timeout || undefined })} />
        </Field>
        <Field label="If nobody decides in time">
          <select value={escalate ? "escalate" : onTimeout || "reject"} onChange={(e) => setCfg({ on_timeout: e.target.value === "escalate" ? "escalate:owner" : e.target.value === "reject" ? undefined : e.target.value })}>
            {ON_TIMEOUT.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
        </Field>
        {escalate && (
          <Field label="Ask">
            <RolePicker label="Ask" value={onTimeout.slice("escalate:".length)} onChange={(role) => setCfg({ on_timeout: `escalate:${role}` })} />
          </Field>
        )}
      </div>
      <Field label="What the approver sees">
        {plainSubject ? (
          <div>
            {Object.entries(subject).map(([k, v]) => (
              <span key={k} className="toolbar" style={{ marginBottom: 4 }}>
                <span>{k.replace(/_/g, " ")}:</span>
                {isExpression(v) ? (
                  <ValuePicker label={k} sources={sources} value={String(v)} onChange={(x) => setCfg({ subject: { ...subject, [k]: x } })} />
                ) : (
                  <input aria-label={k} value={String(v ?? "")} onChange={(e) => setCfg({ subject: { ...subject, [k]: e.target.value } })} />
                )}
                <button
                  type="button"
                  className="link"
                  onClick={() => {
                    const next = Object.fromEntries(Object.entries(subject).filter(([x]) => x !== k));
                    setCfg({ subject: Object.keys(next).length ? next : undefined });
                  }}
                >
                  Remove
                </button>
              </span>
            ))}
            {sources.length > 0 && (
              <ValuePicker
                label="Show another detail"
                sources={sources.filter((s) => !Object.values(subject).includes(s.expr))}
                value=""
                onChange={(x) => {
                  const src = sources.find((s) => s.expr === x);
                  if (!src) return;
                  const key = (src.label.split(": ").pop() ?? "detail").toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_|_$/g, "") || "detail";
                  setCfg({ subject: { ...subject, [key]: x } });
                }}
              />
            )}
          </div>
        ) : (
          <ByHand />
        )}
      </Field>
    </>
  );
}

/** A capability step: what to do, on which rail, with what (wd-v1 capability steps). */
function CapabilityConfig({ step, connectors, set, advanced, sources }: { step: Extract<Step, { type: "capability" }>; connectors: ConnectorInfo[]; set: (p: Any) => void; advanced: boolean; sources: Source[] }) {
  const caps = useLoad(() => get<{ capabilities: CapabilityInfo[] }>("/v1/capabilities"), []);
  const names = useLoad(() => get<{ connections: ConnectionName[] }>("/v1/connection-names"), []);
  const cap = caps.data?.capabilities.find((c) => c.name === step.uses);
  const options = capableConnections(names.data?.connections ?? [], cap?.rails ?? [], connectors);
  return (
    <>
      <Field label="What to do">
        <select value={step.uses} onChange={(e) => set({ uses: e.target.value, input: undefined })}>
          {!cap && <option value={step.uses}>{step.uses}</option>}
          {(caps.data?.capabilities ?? []).filter((c) => c.kind !== "event").map((c) => (
            <option key={c.name} value={c.name}>
              {c.title}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Pay through" hint="Any of your connections whose service can do it. Change it and redeploy to move to another service; runs already going finish where they started.">
        <select value={step.connection ?? ""} onChange={(e) => set({ connection: e.target.value || undefined })}>
          <option value="">{options.length === 1 ? `${options[0]?.label} (the only one that can)` : options.length === 0 ? "None of your connections can do this yet" : "Choose a connection…"}</option>
          {step.connection && !options.some((o) => o.name === step.connection) && <option value={step.connection}>{step.connection} (cannot do this)</option>}
          {options.map((o) => (
            <option key={o.name} value={o.name}>
              {o.label}
            </option>
          ))}
        </select>
      </Field>
      {cap && cap.rails.length > 0 && options.length === 0 && (
        <p className="hint">Services that can: {[...new Set(cap.rails.map((r) => connectors.find((c) => c.ref === r)?.name ?? r))].join(", ")}. Connect one under Connections.</p>
      )}
      <h3>Input</h3>
      <SchemaForm schema={cap?.input as never} value={step.input} onChange={(v) => set({ input: v })} expressions={advanced} sources={sources} />
      {advanced && cap && !step.uses.startsWith("await_") && !["balance", "resolve_account", "list_banks", "get_payout", "get_payment"].includes(step.uses) && (
        <Field label="Idempotency seed" hint="Optional expression that makes this payment once per item, e.g. =trigger.body.payroll_id + ':' + item.employee_id">
          <input className="mono" value={step.effect?.idempotency_seed ?? ""} onChange={(e) => set({ effect: e.target.value ? { idempotency_seed: e.target.value } : undefined })} />
        </Field>
      )}
    </>
  );
}

/**
 * A template step: which callable template, its parameters, and its run
 * inputs from this workflow (decision 0047). Its steps run in this run; the
 * panel shows them, what the step gives back, and what the template does.
 */
function TemplateConfig({ step, set, advanced, sources }: { step: Extract<Step, { type: "template" }>; set: (p: Any) => void; advanced: boolean; sources: Source[] }) {
  const list = useLoad(() => get<{ templates: Template[] }>("/v1/templates?callable=true"), []);
  const own = useLoad(() => get<{ workflows: CallableWorkflow[] }>("/v1/workflows/callable"), []);
  // The step uses a library template, or one of the business's own
  // workflows (decision 0048), which has no settings.
  const wf = step.workflow !== undefined ? own.data?.workflows.find((w) => w.id === step.workflow) : undefined;
  const tpl = step.template !== undefined ? list.data?.templates.find((t) => t.id === step.template) : undefined;
  const iface = tpl?.callable ?? wf;
  const params = (tpl?.params ?? []).filter((p) => tpl?.callable?.params.includes(p.name));
  const given = (step.params ?? {}) as Record<string, unknown>;
  const values = Object.fromEntries(params.map((p) => [p.name, given[p.name] === undefined ? "" : String(given[p.name])]));
  const chosen = step.workflow !== undefined ? `workflow:${step.workflow}` : `template:${step.template ?? ""}`;
  const choose = (v: string) => {
    if (v.startsWith("workflow:")) set({ workflow: v.slice("workflow:".length), template: undefined, params: undefined, input: {} });
    else set({ template: v.slice("template:".length), workflow: undefined, params: undefined, input: {} });
  };
  return (
    <>
      <Field label="Template" hint="A ready workflow from the library, or one of your own workflows that gives something back, used as one step: its approvals, payments and waits run in this workflow's run.">
        <select value={chosen} onChange={(e) => choose(e.target.value)}>
          {!tpl && !wf && <option value={chosen}>{step.workflow ?? step.template}</option>}
          <optgroup label="From the library">
            {(list.data?.templates ?? []).map((t) => (
              <option key={t.id} value={`template:${t.id}`}>
                {t.title}
              </option>
            ))}
          </optgroup>
          {(own.data?.workflows ?? []).length > 0 && (
            <optgroup label="Your workflows">
              {(own.data?.workflows ?? []).map((w) => (
                <option key={w.id} value={`workflow:${w.id}`}>
                  {w.name}
                </option>
              ))}
            </optgroup>
          )}
        </select>
      </Field>
      {iface && <p className="hint">{effectWords(iface.effect)}</p>}
      {wf && <p className="hint">Version {wf.version}, the published one. Publishing a new version of it changes this step when this workflow is next saved.</p>}
      {params.length > 0 && <h3>Settings</h3>}
      {params.map((p) => (
        <ParamField
          key={p.name}
          param={p}
          value={values[p.name] ?? ""}
          params={params}
          values={values}
          onChange={(v) => set({ params: nonEmpty({ ...given, ...toParams([p], { [p.name]: v }), ...(v.trim() === "" ? { [p.name]: undefined } : {}) }) })}
          onOther={(name, v) => set({ params: nonEmpty({ ...given, ...toParams(params.filter((x) => x.name === name), { [name]: v }) }) })}
        />
      ))}
      <h3>What it starts with</h3>
      <SchemaForm schema={iface?.inputs as never} value={step.input} onChange={(v) => set({ input: v })} expressions={advanced} sources={sources} />
      {iface && iface.outputs.length > 0 && (
        <>
          <h3>What it gives back</h3>
          <ul className="plain-list">
            {iface.outputs.map((o) => (
              <li key={o.name}>
                <strong>{o.title}</strong>: {o.description} {advanced && <code>steps.{step.id}.output.{o.name}</code>}
              </li>
            ))}
          </ul>
        </>
      )}
      {tpl && (
        <details>
          <summary>Its steps ({tpl.steps.length})</summary>
          <ol className="plain-list">
            {tpl.steps.map((l) => (
              <li key={l.number} style={{ marginLeft: l.depth * 16 }}>
                {l.text}
              </li>
            ))}
          </ol>
        </details>
      )}
    </>
  );
}

interface AIStatus {
  enabled: boolean;
  model?: string;
  step_models?: string[];
  retention?: string;
  steps_allowed: boolean;
  steps_plan: boolean;
}

function AIConfig({ step, set, advanced }: { step: Extract<Step, { type: "ai" }>; set: (p: Any) => void; advanced: boolean }) {
  const status = useLoad(() => get<AIStatus>("/v1/ai/status"), []);
  const cfg = step.config;
  const input = (step.input ?? {}) as Record<string, unknown>;
  const through = cfg.personal_data ?? [];
  const setCfg = (patch: Partial<typeof cfg>) => set({ config: { ...cfg, ...patch } });
  const st = status.data;
  return (
    <>
      {st && !st.enabled && <p className="notice">This deployment has no AI model, so this step cannot run.</p>}
      {st && st.enabled && !st.steps_allowed && <p className="notice">Your business has turned AI steps off (Secrets & settings, AI steps).</p>}
      {st && st.enabled && !st.steps_plan && <p className="notice">Your plan does not include AI.</p>}
      <Field label="What to ask" hint="Say what you want in plain words: sort the message, pull out the address, write a short reply.">
        <textarea rows={5} value={cfg.prompt} onChange={(e) => setCfg({ prompt: e.target.value })} />
      </Field>
      {!advanced && (
        <>
          <p className="hint">It reads: {Object.keys(input).join(", ") || "nothing yet"}.</p>
          <p className="hint">It answers with: {answerFields(cfg.output_schema).join(", ") || "nothing yet"}.</p>
        </>
      )}
      {advanced && (
        <div data-technical>
          <Field label="What it reads (JSON)" hint="The data the model reads, by name; values may be expressions such as =trigger.body.message.">
            <JsonInput value={input} onChange={(v) => set({ input: (v as object | undefined) ?? {} })} rows={6} />
          </Field>
          <Field label="The answer's shape (JSON Schema)" hint={'An object schema, e.g. {"type": "object", "properties": {"urgent": {"type": "boolean"}}, "required": ["urgent"]}. The step\'s output is the answer.'}>
            <JsonInput value={cfg.output_schema} onChange={(v) => v !== undefined && setCfg({ output_schema: v as object })} rows={8} />
          </Field>
        </div>
      )}
      <h3>Personal data</h3>
      <p className="hint">
        Names, phone numbers, emails, BVNs and other personal data reach the model as placeholders such as [[name_1]], and are put back in its answer. Tick a field only when the model must read it as it
        is; publishing such a step is recorded in the audit log.
      </p>
      {Object.keys(input).length === 0 && <p className="hint">The step reads no fields yet.</p>}
      {Object.keys(input).map((k) => (
        <label key={k} style={{ display: "block" }}>
          <input
            type="checkbox"
            checked={through.includes(k)}
            onChange={(e) => {
              const next = e.target.checked ? [...through, k] : through.filter((x) => x !== k);
              setCfg({ personal_data: next.length > 0 ? next : undefined });
            }}
          />{" "}
          Let the model see <code>{k}</code> as it is
        </label>
      ))}
      {advanced && st?.enabled && (
        <Field label="Model" hint={st.retention ? `What the provider keeps: ${st.retention}` : "The deployment's default unless you pick another it offers."}>
          <select value={cfg.model ?? ""} onChange={(e) => setCfg({ model: e.target.value || undefined })}>
            <option value="">Default ({st.model})</option>
            {(st.step_models ?? []).filter((m) => m !== st.model).map((m) => (
              <option key={m} value={m}>
                {m}
              </option>
            ))}
          </select>
        </Field>
      )}
    </>
  );
}

/** The names of the fields an answer's shape gives. */
function answerFields(shape: unknown): string[] {
  const props = (shape as { properties?: Record<string, unknown> } | undefined)?.properties;
  return props ? Object.keys(props) : [];
}

function SubflowConfig({ step, set, advanced, sources }: { step: Extract<Step, { type: "subflow" }>; set: (p: Any) => void; advanced: boolean; sources: Source[] }) {
  const list = useLoad(() => get<{ workflows: WorkflowSummary[] }>("/v1/workflows"), []);
  // The business's published workflows that start on demand (decision 0049).
  const startable = (list.data?.workflows ?? []).filter((w) => w.active_version !== null && (w.trigger?.type === "manual" || w.trigger?.type === "subflow"));
  const chosen = startable.find((w) => w.id === step.config.workflow);
  const version = step.config.version ?? chosen?.active_version ?? null;
  const target = useLoad(
    () => (chosen && version ? get<{ definition: WorkflowDefinition }>(`/v1/workflows/${chosen.id}/versions/${version}`) : Promise.resolve(null)),
    [chosen?.id, version],
  );
  const inputs = target.data?.definition.inputs?.schema;
  const outputs = target.data?.definition.outputs ?? [];
  const waits = step.config.wait !== false;
  return (
    <>
      <Field label="Workflow" hint="One of your published workflows, run as its own run with its own history. Pick a workflow that starts by hand or when another workflow calls it.">
        <select value={step.config.workflow} onChange={(e) => set({ config: { ...step.config, workflow: e.target.value, version: undefined }, input: {} })}>
          {!chosen && <option value={step.config.workflow}>{step.config.workflow || "Choose a workflow"}</option>}
          {startable.map((w) => (
            <option key={w.id} value={w.id}>
              {w.name}
            </option>
          ))}
        </select>
      </Field>
      <Field label="Then" hint={waits ? "This workflow waits for that run to end; the step's output is what it gave back." : "This workflow carries on as soon as that run has started."}>
        <select value={waits ? "wait" : "continue"} onChange={(e) => set({ config: { ...step.config, wait: e.target.value === "wait" ? undefined : false } })}>
          <option value="wait">Wait for it to finish</option>
          <option value="continue">Carry on without waiting</option>
        </select>
      </Field>
      {advanced && (
        <Field label="Version" hint="Leave blank to run the version published when the step starts.">
          <input
            type="number"
            min={1}
            value={step.config.version ?? ""}
            onChange={(e) => set({ config: { ...step.config, version: e.target.value === "" ? undefined : Number(e.target.value) } })}
          />
        </Field>
      )}
      <h3>What it starts with</h3>
      <SchemaForm schema={inputs as never} value={step.input} onChange={(v) => set({ input: v })} expressions={advanced} sources={sources} />
      {waits && outputs.length > 0 && (
        <>
          <h3>What it gives back</h3>
          <ul className="plain-list">
            {outputs.map((o) => (
              <li key={o.name}>
                <strong>{o.title}</strong>: {o.description} {advanced && <code>steps.{step.id}.output.outputs.{o.name}</code>}
              </li>
            ))}
          </ul>
        </>
      )}
      {waits && outputs.length === 0 && chosen && <p className="hint">It gives nothing back: add outputs to it to use its results here. The step's output has the run's id and status.</p>}
    </>
  );
}

/** params without the ones left blank. */
function nonEmpty(o: Record<string, unknown>): Record<string, unknown> | undefined {
  const out = Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined && v !== ""));
  return Object.keys(out).length > 0 ? out : undefined;
}

function ConnectorConfig({ step, connectors, set, advanced, sources }: { step: Extract<Step, { type: "connector" }>; connectors: ConnectorInfo[]; set: (p: Any) => void; advanced: boolean; sources: Source[] }) {
  const conn = connectors.find((c) => c.ref === step.connector);
  const action = conn?.actions[step.action];
  return (
    <>
      <div className="row">
        <Field label="Connector">
          <select value={step.connector} onChange={(e) => set({ connector: e.target.value, action: Object.keys(connectors.find((c) => c.ref === e.target.value)?.actions ?? {})[0] ?? "", input: undefined })}>
            {!conn && <option value={step.connector}>{step.connector} (not available)</option>}
            {connectors.map((c) => (
              <option key={c.ref} value={c.ref}>
                {advanced ? `${c.name} (${c.ref})` : c.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Action">
          <select value={step.action} onChange={(e) => set({ action: e.target.value, input: undefined })}>
            {conn &&
              Object.entries(conn.actions).map(([k, a]) => (
                <option key={k} value={k}>
                  {a.title || k}
                </option>
              ))}
          </select>
        </Field>
      </div>
      {advanced && action && <div className="hint" style={{ marginBottom: 8 }}>Class: {action.class.replaceAll("_", " ")}</div>}
      <Field label="Connection" hint="Which of your connections to this service it uses">
        <ConnectionPicker connector={step.connector ?? ""} value={step.connection ?? ""} onChange={(connection) => set({ connection: connection || undefined })} />
      </Field>
      <h3>Input</h3>
      <SchemaForm schema={action?.input} value={step.input} onChange={(v) => set({ input: v })} expressions={advanced} sources={sources} />
      {advanced && action && action.class !== "read" && (
        <Field label="Idempotency seed" hint="Optional expression replacing the run id in the idempotency key, e.g. =trigger.body.payroll_id + ':' + item.employee_id">
          <input className="mono" value={step.effect?.idempotency_seed ?? ""} onChange={(e) => set({ effect: e.target.value ? { idempotency_seed: e.target.value } : undefined })} />
        </Field>
      )}
    </>
  );
}
