// Form fields generated from a JSON Schema (connector action inputs and
// workflow inputs). Every field also accepts an "=..." expression.
import type { JSONSchema } from "../api";
import { coerce, isExpression, kindOf, setKey } from "../lib/schema";
import { JsonInput } from "../ui";

interface Props {
  schema: JSONSchema | undefined;
  value: Record<string, unknown> | undefined;
  onChange: (v: Record<string, unknown> | undefined) => void;
  expressions?: boolean; // offer the expression toggle (workflow steps)
}

export function SchemaForm({ schema, value, onChange, expressions = true }: Props) {
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
        <SchemaField key={k} name={k} schema={s} required={required.has(k)} value={value?.[k]} onChange={(v) => update(k, v)} expressions={expressions} />
      ))}
    </div>
  );
}

function SchemaField({ name, schema, required, value, onChange, expressions }: {
  name: string;
  schema: JSONSchema;
  required: boolean;
  value: unknown;
  onChange: (v: unknown) => void;
  expressions: boolean;
}) {
  const kind = kindOf(schema);
  const expr = isExpression(value);
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
  if (expr || (expressions && value === "=")) {
    input = <input className="mono" value={String(value)} placeholder="=trigger.body.field" onChange={(e) => onChange(e.target.value || undefined)} />;
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
        <SchemaForm schema={schema} value={value as Record<string, unknown> | undefined} onChange={onChange} expressions={expressions} />
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
      {schema.description && <div className="hint">{schema.description}</div>}
    </div>
  );
}
