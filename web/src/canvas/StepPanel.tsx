// Edits one top-level step. Nested steps (branch paths, foreach bodies,
// on_error) are edited as JSON inside their parent.
import { useState } from "react";
import type { Step } from "@sdk/wd";
import type { ConnectorInfo } from "../api";
import { SchemaForm } from "../forms/SchemaForm";
import { merge } from "../lib/schema";
import { Field, JsonInput } from "../ui";

const ID = /^[a-z][a-z0-9_]{0,62}$/;

interface Props {
  step: Step;
  others: string[];
  connectors: ConnectorInfo[];
  onChange: (s: Step) => void;
  onRename: (id: string) => boolean;
  onDelete: () => void;
}

type Any = Record<string, unknown>;


const JS_TEMPLATE = "export default (input: Record<string, unknown>) => {\n  return { ok: true };\n};\n";
const PYTHON_TEMPLATE = "def main(input, host):\n    return {\"ok\": True}\n";

export function StepPanel({ step, others, connectors, onChange, onRename, onDelete }: Props) {
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
                {o}
              </label>
            ))}
          </div>
        </Field>
      )}
      <Field label="Run only when" hint="An expression, e.g. =steps.approve.output.decision == 'approved'">
        <input className="mono" value={step.when ?? ""} placeholder="=" onChange={(e) => set({ when: e.target.value })} />
      </Field>

      <h3>Configuration</h3>
      {step.type === "connector" && <ConnectorConfig step={step} connectors={connectors} set={set} />}
      {step.type === "http" && (
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
      {step.type === "code" && (
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
      {step.type === "transform" && (
        <Field label="Output (JSON; values may be expressions)">
          <JsonInput value={cfg.output} onChange={(v) => setCfg({ output: v === undefined ? null : v })} rows={8} />
        </Field>
      )}
      {step.type === "wait" && (
        <div className="row">
          <Field label="Duration" hint="e.g. 30m, 2h, 1d">
            <input value={String(cfg.duration ?? "")} onChange={(e) => set({ config: e.target.value ? { duration: e.target.value } : { until: cfg.until ?? "=" } })} />
          </Field>
          <Field label="Or until" hint="An expression giving an RFC 3339 time">
            <input className="mono" value={String(cfg.until ?? "")} onChange={(e) => set({ config: e.target.value ? { until: e.target.value } : { duration: "1h" } })} />
          </Field>
        </div>
      )}
      {step.type === "signal" && (
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
            <Field label="Role" hint="Members with this role may decide">
              <input value={String(cfg.role ?? "")} onChange={(e) => setCfg({ role: e.target.value })} />
            </Field>
            <Field label="Approvals needed">
              <input type="number" min={1} value={Number(cfg.count ?? 1)} onChange={(e) => setCfg({ count: Number(e.target.value) > 1 ? Number(e.target.value) : undefined })} />
            </Field>
          </div>
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
      )}
      {(step.type === "branch" || step.type === "foreach" || step.type === "parallel") && (
        <Field label="Configuration (JSON)" hint="Nested steps are edited here.">
          <JsonInput value={cfg} onChange={(v) => v && set({ config: v })} rows={14} />
        </Field>
      )}

      {(step.type === "connector" || step.type === "http" || step.type === "code") && (
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
      <Field label="Step timeout">
        <input value={step.timeout ?? ""} placeholder="e.g. 30s" onChange={(e) => set({ timeout: e.target.value })} />
      </Field>
      <details>
        <summary>Edit this step as JSON</summary>
        <JsonInput value={step} onChange={(v) => v && typeof v === "object" && onChange(v as Step)} rows={14} />
      </details>
    </div>
  );
}

function ConnectorConfig({ step, connectors, set }: { step: Extract<Step, { type: "connector" }>; connectors: ConnectorInfo[]; set: (p: Any) => void }) {
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
                {c.name} ({c.ref})
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
      {action && <div className="hint" style={{ marginBottom: 8 }}>Class: {action.class.replaceAll("_", " ")}</div>}
      <Field label="Connection" hint="Name of the connection to use; empty uses the only one">
        <input value={step.connection ?? ""} onChange={(e) => set({ connection: e.target.value })} />
      </Field>
      <h3>Input</h3>
      <SchemaForm schema={action?.input} value={step.input} onChange={(v) => set({ input: v })} />
      {action && action.class !== "read" && (
        <Field label="Idempotency seed" hint="Optional expression replacing the run id in the idempotency key, e.g. =trigger.body.payroll_id + ':' + item.employee_id">
          <input className="mono" value={step.effect?.idempotency_seed ?? ""} onChange={(e) => set({ effect: e.target.value ? { idempotency_seed: e.target.value } : undefined })} />
        </Field>
      )}
    </>
  );
}
