// Form fields generated from a JSON Schema (connector action inputs and
// workflow inputs). Every field also accepts an "=..." expression.
import { useState } from "react";
import type { JSONSchema } from "../api";
import { sourceOf, type Source } from "../lib/plaincanvas";
import { ValuePicker } from "../canvas/PlainPickers";
import { coerce, isExpression, kindOf, setKey } from "../lib/schema";
import { JsonInput } from "../ui";
import { RecipientField } from "./RecipientField";
import { FieldText } from "./FieldText";
import { SavedFill } from "../pages/SavedFill";
import { placeAddressFit } from "../lib/saved";
import { toolSavedKind } from "../lib/toolsaved";
import { deviceKindOf, type FieldSchema } from "../lib/forms";
import { DeviceField } from "./DeviceField";

interface Props {
  schema: JSONSchema | undefined;
  value: Record<string, unknown> | undefined;
  onChange: (v: Record<string, unknown> | undefined) => void;
  expressions?: boolean; // offer the expression toggle (workflow steps)
  /** Values from earlier steps a field can take instead of a fixed one (no-technical-steps N8). */
  sources?: Source[];
  /** A tool's form in its sheet (made tools M1): a text input a saved
   * kind fits (x-saved, its format, its words) has a Saved button. */
  saved?: boolean;
}

export function SchemaForm({ schema, value, onChange, expressions = true, sources, saved = false }: Props) {
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
      {Object.entries(props).map(([k, s]) => {
        // A tool's location, photo, signature or code (build-plan-gaps
        // H3): asked of the browser at the tap, as a form step's are.
        const device = saved && !expressions ? deviceKindOf(s as FieldSchema) : undefined;
        return device ? (
          <DeviceField key={k} name={k} kind={device} schema={s as FieldSchema} required={required.has(k)} value={value?.[k]} onChange={(v) => update(k, v)} />
        ) : (
          <SchemaField key={k} name={k} schema={s} required={required.has(k)} value={value?.[k]} onChange={(v) => update(k, v)} expressions={expressions} sources={sources} saved={saved} />
        );
      })}
    </div>
  );
}

function SchemaField({ name, schema, required, value, onChange, expressions, sources, saved }: {
  name: string;
  schema: JSONSchema;
  required: boolean;
  value: unknown;
  onChange: (v: unknown) => void;
  expressions: boolean;
  sources?: Source[];
  saved: boolean;
}) {
  const kind = kindOf(schema);
  const expr = isExpression(value);
  // Without expressions, a field takes a value from an earlier step by its words.
  // Text takes fields among its words, as chips (the builder's message boxes).
  const textWithFields = !expressions && !!sources?.length && kind === "string" && !schema.format;
  const fromSource = !expressions && !!sources?.length && kind !== "object" && !textWithFields;
  const [picking, setPicking] = useState(() => fromSource && !!sourceOf(sources ?? [], value));
  // What a Saved pick could not fill, or left out to fit.
  const [savedNote, setSavedNote] = useState<string>();
  const savedKind = saved && !expressions ? toolSavedKind(name, schema as Record<string, unknown>) : undefined;
  // Without expressions (plain words), a field with no title reads as words.
  const title = schema.title ?? (expressions ? name : (name.charAt(0).toUpperCase() + name.slice(1)).replaceAll("_", " "));
  const label = (
    <span>
      {title}
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
  if (textWithFields) {
    input = <FieldText label={String(title)} value={value} onChange={(v) => onChange(v)} sources={sources ?? []} multiline={/message|body|text|note|description|content/i.test(name)} />;
  } else if (fromSource && (picking || sourceOf(sources ?? [], value))) {
    input = (
      <span className="toolbar">
        <ValuePicker label={String(title)} sources={sources ?? []} value={typeof value === "string" ? value : ""} onChange={(v) => onChange(v || undefined)} />
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
    input = <RecipientField label={title} value={typeof value === "string" ? value : ""} onChange={(v) => onChange(v || undefined)} />;
  } else if (kind === "boolean") {
    input = <input type="checkbox" aria-label={String(title)} checked={value === true} onChange={(e) => onChange(e.target.checked)} />;
  } else if (kind === "enum") {
    input = (
      <select aria-label={String(title)} value={value === undefined ? "" : JSON.stringify(value)} onChange={(e) => onChange(e.target.value === "" ? undefined : JSON.parse(e.target.value))}>
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
        aria-label={String(title)}
        value={value === undefined ? "" : String(value)}
        onChange={(e) => onChange(coerce(kind, e.target.value))}
      />
    );
    if (savedKind && kind === "string") {
      const limit = (schema as { maxLength?: unknown }).maxLength;
      const max = typeof limit === "number" ? limit : 300;
      input = (
        <>
          {input}
          <SavedFill
            kind={savedKind}
            label={String(title).toLowerCase()}
            inSheet
            onPick={(x, v, problem) => {
              let fill = v;
              let note = problem;
              if (savedKind === "person") fill = x.title;
              else if (savedKind === "place") {
                const fit = placeAddressFit(x.details, max);
                fill = fit.address;
                note = fit.hint || undefined;
              }
              setSavedNote(note);
              if (fill) onChange(fill.slice(0, max));
            }}
          />
          {savedNote && (
            <p className="hint saved-fill-note" role="status" data-testid="saved-fill-note">
              {savedNote}
            </p>
          )}
        </>
      );
    }
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
