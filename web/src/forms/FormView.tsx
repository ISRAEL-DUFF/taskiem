// A form as the respondent sees it, with the form host's widgets: pages
// with Next and Back, show_if over the answers so far, and plain-words
// checks before sending. Used by the editor's preview, members forms and
// form steps for a role.
import { useId, useMemo, useState, type ReactNode } from "react";
import {
  answerProblem,
  answersToSend,
  coerceAnswer,
  coerceAnswers,
  formatNaira,
  labelOf,
  pagesOf,
  parseNaira,
  shownPages,
  widgetOf,
  type FieldSchema,
  type FormPage,
  type FormSchema,
} from "../lib/forms";

type Raw = Record<string, unknown>;

export interface FormViewProps {
  title?: string;
  description?: string;
  schema: FormSchema | undefined;
  pages?: FormPage[] | null;
  /** Starting answers (prefill), as the schema has them. */
  initial?: Raw;
  /** Fields that are shown filled and cannot be changed. */
  locked?: string[];
  /** Files cannot be sent here: file fields show this note and are left out. */
  filesNote?: string;
  /** The editor's preview: nothing is sent. */
  preview?: boolean;
  submitLabel?: string;
  busy?: boolean;
  onSubmit?: (answers: Raw) => void;
  /** Below the buttons (an error from sending). */
  footer?: ReactNode;
  /** The title's heading level: 1 on a page of its own. */
  level?: 1 | 2;
}

/** Starting raw values: what inputs hold for each answer. */
function rawOf(schema: FormSchema | undefined, initial: Raw | undefined): Raw {
  const out: Raw = {};
  const props = schema?.properties ?? {};
  for (const [k, v] of Object.entries(initial ?? {})) {
    const p = props[k];
    if (!p || v === undefined || v === null) continue;
    const w = widgetOf(p);
    if (w === "money" && typeof v === "number") out[k] = formatNaira(v, false);
    else if ((w === "number" || w === "text" || w === "textarea" || w === "tel" || w === "email" || w === "url") && typeof v !== "string") out[k] = typeof v === "object" ? JSON.stringify(v) : String(v as number);
    else if (w === "datetime-local" && typeof v === "string") out[k] = v.slice(0, 16);
    else out[k] = v;
  }
  return out;
}

export function FormView({ title, description, schema, pages, initial, locked = [], filesNote, preview, submitLabel = "Send", busy, onSubmit, footer, level = 2 }: FormViewProps) {
  const all = useMemo(() => pagesOf(pages, schema), [pages, schema]);
  const [raw, setRaw] = useState<Raw>(() => rawOf(schema, initial));
  const [at, setAt] = useState(0);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [sent, setSent] = useState(false);
  const answers = coerceAnswers(schema, raw);
  const shown = shownPages(all, answers);
  const page = shown.includes(at) ? at : (shown.filter((i) => i < at).at(-1) ?? shown[0] ?? 0);
  const pos = shown.indexOf(page);
  const last = pos === shown.length - 1;
  const props = schema?.properties ?? {};
  const required = new Set(schema?.required ?? []);
  const current = all[page];

  const asked = (name: string) => {
    const p = props[name];
    return !!p && widgetOf(p) !== "hidden" && !(filesNote && widgetOf(p) === "file");
  };
  const check = (names: string[]) => {
    const e: Record<string, string> = {};
    for (const n of names) {
      const p = props[n];
      if (!p || !asked(n) || locked.includes(n)) continue;
      const m = answerProblem(p, coerceAnswer(p, raw[n]), required.has(n));
      if (m) e[n] = m;
    }
    setErrors(e);
    return Object.keys(e).length === 0;
  };
  const next = () => {
    if (!check(current?.fields ?? [])) return;
    setAt(shown[pos + 1] ?? page);
  };
  const submit = () => {
    if (!check(current?.fields ?? [])) return;
    if (preview) {
      setSent(true);
      return;
    }
    // Files are not sent from the app.
    const out = Object.fromEntries(Object.entries(answersToSend(schema, all, raw)).filter(([k]) => !(filesNote && props[k] && widgetOf(props[k]) === "file")));
    onSubmit?.(out);
  };

  return (
    <form
      className="form-view"
      noValidate
      onSubmit={(e) => {
        e.preventDefault();
        if (last) submit();
        else next();
      }}
    >
      {title && (level === 1 ? <h1 className="form-title">{title}</h1> : <h2 className="form-title">{title}</h2>)}
      {description && <p className="hint">{description}</p>}
      {shown.length > 1 && (
        <p className="hint" aria-live="polite">
          Page {pos + 1} of {shown.length}
          {current?.title ? `: ${current.title}` : ""}
        </p>
      )}
      {shown.length <= 1 && current?.title && <h3>{current.title}</h3>}
      {(current?.fields ?? []).map((name) => {
        const p = props[name];
        if (!p || widgetOf(p) === "hidden") return null;
        return (
          <FormField
            key={name}
            name={name}
            schema={p}
            required={required.has(name)}
            value={raw[name]}
            locked={locked.includes(name)}
            error={errors[name]}
            filesNote={filesNote}
            onChange={(v) => {
              setSent(false);
              setRaw((r) => ({ ...r, [name]: v }));
              if (errors[name]) setErrors((e) => ({ ...e, [name]: "" }));
            }}
          />
        );
      })}
      <div className="toolbar form-buttons">
        {pos > 0 && (
          <button type="button" onClick={() => setAt(shown[pos - 1] ?? 0)}>
            Back
          </button>
        )}
        {last ? (
          <button type="submit" className="primary" disabled={busy}>
            {submitLabel}
          </button>
        ) : (
          <button type="submit" className="primary">
            Next
          </button>
        )}
      </div>
      {preview && sent && (
        <p className="hint" role="status">
          This is a preview: nothing was sent.
        </p>
      )}
      {footer}
    </form>
  );
}

const ACCEPT_NAMES: Record<string, string> = { "application/pdf": "PDF", "image/*": "images", "image/jpeg": "JPEG", "image/png": "PNG" };

export function FormField({
  name,
  schema: p,
  required,
  value,
  locked,
  error,
  filesNote,
  onChange,
}: {
  name: string;
  schema: FieldSchema;
  required: boolean;
  value: unknown;
  locked?: boolean;
  error?: string;
  filesNote?: string;
  onChange: (v: unknown) => void;
}) {
  const id = useId();
  const w = widgetOf(p);
  const h = p["x-form"] ?? {};
  const label = labelOf(name, p);
  const help = h.help ?? p.description;
  const helpId = `${id}-help`;
  const errId = `${id}-err`;
  const describedBy = [help ? helpId : "", error ? errId : ""].filter(Boolean).join(" ") || undefined;
  const common = { id, name, "aria-describedby": describedBy, "aria-invalid": error ? true : undefined, readOnly: locked, required };
  const str = typeof value === "string" ? value : "";
  const choices = (w === "checkboxes" ? p.items?.enum : p.enum) ?? [];
  const mark = required ? <span aria-hidden="true"> *</span> : <span className="hint"> (optional)</span>;
  const below = (
    <>
      {help && (
        <div className="hint" id={helpId}>
          {help}
        </div>
      )}
      {locked && <div className="hint">From your account.</div>}
      {error && (
        <div className="field-error" id={errId}>
          {error}
        </div>
      )}
    </>
  );

  if (w === "radio" || w === "checkboxes") {
    const picked = Array.isArray(value) ? value : [];
    return (
      <fieldset className="form-field" aria-describedby={describedBy}>
        <legend>
          {label}
          {mark}
        </legend>
        {choices.map((c, i) => (
          <label key={i} className="choice">
            {w === "radio" ? (
              <input type="radio" name={`${id}-${name}`} checked={value === c} disabled={locked} onChange={() => onChange(c)} />
            ) : (
              <input
                type="checkbox"
                checked={picked.includes(c)}
                disabled={locked}
                onChange={(e) => onChange(e.target.checked ? [...picked, c] : picked.filter((x) => x !== c))}
              />
            )}
            {String(c)}
          </label>
        ))}
        {below}
      </fieldset>
    );
  }
  if (w === "checkbox" || w === "consent") {
    return (
      <div className="form-field">
        <label className="choice" htmlFor={id}>
          <input type="checkbox" {...common} checked={value === true} disabled={locked} onChange={(e) => onChange(e.target.checked)} />
          <span>
            {w === "consent" ? h.text || label : label}
            {w === "consent" && required ? <span aria-hidden="true"> *</span> : null}
          </span>
        </label>
        {below}
      </div>
    );
  }
  let input: ReactNode;
  switch (w) {
    case "textarea":
      input = <textarea {...common} rows={4} placeholder={h.placeholder} value={str} onChange={(e) => onChange(e.target.value)} />;
      break;
    case "select":
      input = (
        <select {...common} disabled={locked} value={String(choices.findIndex((c) => c === value))} onChange={(e) => onChange(e.target.value === "-1" ? undefined : choices[Number(e.target.value)])}>
          <option value="-1">Choose…</option>
          {choices.map((c, i) => (
            <option key={i} value={i}>
              {String(c)}
            </option>
          ))}
        </select>
      );
      break;
    case "money":
      input = (
        <div className="money-input">
          <span aria-hidden="true">₦</span>
          <input
            {...common}
            inputMode="decimal"
            placeholder={h.placeholder}
            value={str}
            onChange={(e) => onChange(e.target.value)}
            onBlur={() => {
              const n = parseNaira(str);
              if (n !== undefined && !Number.isNaN(n)) onChange(formatNaira(n, false));
            }}
          />
        </div>
      );
      break;
    case "file":
      input = filesNote ? (
        <div className="notice">{filesNote}</div>
      ) : (
        <input
          {...common}
          type="file"
          accept={(h.accept ?? []).join(",") || undefined}
          onChange={(e) => {
            const f = e.target.files?.[0];
            onChange(f ? { name: f.name, type: f.type, size: f.size } : undefined);
          }}
        />
      );
      break;
    default: {
      const type = { number: "number", email: "email", tel: "tel", url: "url", date: "date", time: "time", "datetime-local": "datetime-local" }[w as string] ?? "text";
      input = (
        <input
          {...common}
          type={type}
          inputMode={w === "number" ? "decimal" : w === "tel" ? "tel" : undefined}
          autoComplete={p["x-pii"] === "name" ? "name" : w === "email" ? "email" : w === "tel" ? "tel" : undefined}
          placeholder={h.placeholder}
          value={str}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    }
  }
  return (
    <div className="form-field">
      <label htmlFor={id}>
        {label}
        {mark}
      </label>
      {input}
      {w === "file" && !filesNote && (
        <div className="hint">
          {(h.accept ?? []).map((a) => ACCEPT_NAMES[a] ?? a).join(", ") || "Any file"}, up to {h.max_mb ?? 10} MB
        </div>
      )}
      {below}
    </div>
  );
}
