// Form fields generated from a JSON Schema (connector action inputs and
// workflow inputs). Every field also accepts an "=..." expression.
import { useState } from "react";
import type { JSONSchema } from "../api";
import { sourceOf, type Source } from "../lib/plaincanvas";
import { ValuePicker } from "../canvas/PlainPickers";
import { coerce, isExpression, kindOf, setKey } from "../lib/schema";
import { JsonInput } from "../ui";
import { RecipientField } from "./RecipientField";

interface Props {
  schema: JSONSchema | undefined;
  value: Record<string, unknown> | undefined;
  onChange: (v: Record<string, unknown> | undefined) => void;
  expressions?: boolean; // offer the expression toggle (workflow steps)
  /** Values from earlier steps a field can take instead of a fixed one (no-technical-steps N8). */
  sources?: Source[];
}

export function SchemaForm({ schema, value, onChange, expressions = true, sources }: Props) {
  const props = schema?.properties;
  if (!props) {
    return <JsonInput value={value} onChange={(v) => onChange(v as Record<string, unknown> | undefined)} />;
  }
  const required = new Set(schema.required ?? []);
  const update = (k: string, v: unknown) => {
    const next = setKey(value, k, v);
    onChange(Object.keys(next).length ? next : undefined);
  };
  return (
    <div>
      {Object.entries(props).map(([k, s]) => (
        <SchemaField key={k} name={k} schema={s} required={required.has(k)} value={value?.[k]} onChange={(v) => update(k, v)} expressions={expressions} sources={sources} />
      ))}
    </div>
  );
}

function SchemaField({ name, schema, required, value, onChange, expressions, sources }: {
  name: string;
  schema: JSONSchema;
  required: boolean;
  value: unknown;
  onChange: (v: unknown) => void;
  expressions: boolean;
  sources?: Source[];
}) {
  const kind = kindOf(schema);
  const expr = isExpression(value);
  // Without expressions, a field takes a value from an earlier step by its words.
  const fromSource = !expressions && !!sources?.length && kind !== "object";
  const [picking, setPicking] = useState(() => fromSource && !!sourceOf(sources ?? [], value));
  const label = (
    <span>
      {schema.title ?? name}
      {required && " *"}
      {expressions && (
        <button
          type="button"
          title={expr ? "Use a fixed value" : "Use an expression"}
          style={{ marginLeft: 6, padding: "0 6px", fontSize: 11 }}
          onClick={() => onChange(expr ? undefined : "=")}
        >
          {expr ? "fixed" : "ƒx"}
        </button>
      )}
    </span>
  );
  let input;
  if (fromSource && (picking || sourceOf(sources ?? [], value))) {
    input = (
      <span className="toolbar">
        <ValuePicker label={String(schema.title ?? name)} sources={sources ?? []} value={typeof value === "string" ? value : ""} onChange={(v) => onChange(v || undefined)} />
        <button type="button" className="link" onClick={() => (setPicking(false), onChange(undefined))}>
          Type it instead
        </button>
      </span>
    );
  } else if (!expressions && expr) {
    input = <p className="hint">Set up by hand. Turn on Advanced in Settings to change it.</p>;
  } else if (expr || (expressions && value === "=")) {
    input = <input className="mono" value={String(value)} placeholder="=trigger.body.field" onChange={(e) => onChange(e.target.value || undefined)} />;
  } else if (schema.format === "paystack-recipient") {
    // Someone to pay: their bank and account number, the name confirmed.
    input = <RecipientField label={schema.title ?? name} value={typeof value === "string" ? value : ""} onChange={(v) => onChange(v || undefined)} />;
  } else if (kind === "boolean") {
    input = <input type="checkbox" checked={value === true} onChange={(e) => onChange(e.target.checked)} />;
  } else if (kind === "enum") {
    input = (
      <select value={value === undefined ? "" : JSON.stringify(value)} onChange={(e) => onChange(e.target.value === "" ? undefined : JSON.parse(e.target.value))}>
        <option value="">—</option>
        {schema.enum?.map((o) => (
          <option key={JSON.stringify(o)} value={JSON.stringify(o)}>
            {String(o)}
          </option>
        ))}
      </select>
    );
  } else if (kind === "object") {
    input = (
      <div style={{ borderLeft: "2px solid var(--border)", paddingLeft: 10 }}>
        <SchemaForm schema={schema} value={value as Record<string, unknown> | undefined} onChange={onChange} expressions={expressions} sources={sources} />
      </div>
    );
  } else if (kind === "json") {
    input = <JsonInput value={value} onChange={onChange} rows={3} />;
  } else {
    input = (
      <input
        type={kind === "string" ? "text" : "number"}
        value={value === undefined ? "" : String(value)}
        onChange={(e) => onChange(coerce(kind, e.target.value))}
      />
    );
  }
  return (
    <div className="field" style={{ marginBottom: 10 }}>
      <div style={{ fontSize: 12, color: "var(--muted)", marginBottom: 3 }}>{label}</div>
      {input}
      {fromSource && !picking && !sourceOf(sources ?? [], value) && !expr && (
        <button type="button" className="link" onClick={() => setPicking(true)}>
          Use a value from an earlier step
        </button>
      )}
      {schema.description && <div className="hint">{schema.description}</div>}
    </div>
  );
}
