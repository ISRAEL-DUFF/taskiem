// The form editor (forms 5.1): a form trigger's settings, its fields (the
// workflow's inputs schema), pages and a live preview; and the same field
// editor for a form step's questions.
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link } from "react-router-dom";
import type { WorkflowDefinition } from "@sdk/wd";
import { get, type Problem } from "../api";
import { FormView } from "../forms/FormView";
import {
  FIELD_NAME,
  FIELD_TYPES,
  PII_CATEGORIES,
  earlierFields,
  formProblems,
  loadFields,
  mergeProblems,
  nameFromLabel,
  newField,
  saveFields,
  stepProblems,
  suggestPii,
  type FieldDraft,
  type FieldType,
  type FormSchema,
  type FormStepConfig,
  type FormTriggerConfig,
  type PageDraft,
} from "../lib/forms";
import { merge } from "../lib/schema";
import { Badge, Field, JsonInput } from "../ui";
import type { FormInfo } from "../pages/Forms";

const TEXTLIKE = new Set<FieldType>(["text", "long_text", "number", "money", "email", "phone", "url"]);
const ACCEPTS: [string, string][] = [
  ["application/pdf", "PDF"],
  ["image/*", "Any image"],
  ["image/jpeg", "JPEG"],
  ["image/png", "PNG"],
];

/** Problems listed plainly. */
function ProblemList({ problems, label }: { problems: Problem[]; label?: string }) {
  if (problems.length === 0) return null;
  return (
    <div className="error" role="alert" aria-label={label}>
      <ul>
        {problems.map((p, i) => (
          <li key={i}>{p.message}</li>
        ))}
      </ul>
    </div>
  );
}

/** A textarea of one value per line that keeps what is being typed. */
function LinesInput({ value, onChange, rows = 4, label }: { value: string[]; onChange: (v: string[]) => void; rows?: number; label: string }) {
  const parse = (t: string) => t.split("\n").map((s) => s.trim()).filter(Boolean);
  const [text, setText] = useState(value.join("\n"));
  useEffect(() => {
    if (parse(text).join("\n") !== value.join("\n")) setText(value.join("\n"));
  }, [value]);
  return (
    <textarea
      aria-label={label}
      rows={rows}
      value={text}
      onChange={(e) => {
        setText(e.target.value);
        onChange(parse(e.target.value));
      }}
    />
  );
}

/** A field's name, committed when it is valid and the field leaves focus. */
function NameInput({ name, taken, onRename }: { name: string; taken: string[]; onRename: (n: string) => void }) {
  const [v, setV] = useState(name);
  const [err, setErr] = useState("");
  useEffect(() => setV(name), [name]);
  return (
    <Field label="Name" hint={err || "The answer's key: trigger.body.<name>"}>
      <input
        className="mono"
        value={v}
        onChange={(e) => setV(e.target.value)}
        onBlur={() => {
          if (v === name) return setErr("");
          if (!FIELD_NAME.test(v)) return setErr("Letters, digits and _, starting with a letter.");
          if (taken.includes(v)) return setErr("Another question has that name.");
          setErr("");
          onRename(v);
        }}
      />
    </Field>
  );
}

export interface FieldsEditorProps {
  fields: FieldDraft[];
  onChange: (fields: FieldDraft[]) => void;
  /** Pages to put fields on (form triggers); undefined: one page. */
  pages?: PageDraft[];
  /** Offer "filled from the link" (form triggers). */
  linkPrefill?: boolean;
  /** Offer "from the member's account" (members forms). */
  members?: boolean;
  /** Problems at paths relative to the schema: /properties/<name>/... */
  problems: Problem[];
}

/** The questions: add, remove, reorder and edit each. */
export function FieldsEditor({ fields, onChange, pages, linkPrefill, members, problems }: FieldsEditorProps) {
  const [open, setOpen] = useState<number | null>(null);
  // Names made from labels follow the label until changed by hand.
  const auto = useRef(new Set<string>());
  const names = fields.map((f) => f.name);
  const set = (i: number, patch: Partial<FieldDraft>) => onChange(fields.map((f, j) => (j === i ? { ...f, ...patch } : f)));
  const move = (i: number, d: number) => {
    const j = i + d;
    if (j < 0 || j >= fields.length) return;
    const next = [...fields];
    [next[i], next[j]] = [next[j] as FieldDraft, next[i] as FieldDraft];
    onChange(next);
    if (open === i) setOpen(j);
  };
  const add = () => {
    const name = nameFromLabel("field", names);
    auto.current.add(name);
    const f = newField(name, "text");
    if (pages?.length) f.page = pages.length - 1;
    onChange([...fields, f]);
    setOpen(fields.length);
  };
  return (
    <div className="fe-fields">
      {fields.length === 0 && <p className="hint">No questions yet.</p>}
      {fields.map((f, i) => {
        const mine = problems.filter((p) => p.path === `/properties/${f.name}` || p.path.startsWith(`/properties/${f.name}/`));
        const typeName = FIELD_TYPES.find(([t]) => t === f.type)?.[1] ?? f.type;
        const isOpen = open === i;
        const suggested = suggestPii(f.name, f.type);
        const others = names.filter((n) => n !== f.name);
        return (
          <div key={i} className={`fe-field${mine.length ? " invalid" : ""}`} data-testid={`field-${f.name}`}>
            <div className="fe-head">
              <button type="button" className="fe-title" aria-expanded={isOpen} onClick={() => setOpen(isOpen ? null : i)}>
                <span aria-hidden="true" className="fe-caret">
                  {isOpen ? "▾" : "▸"}
                </span>
                {f.label || f.name}
              </button>
              <span className="hint">
                {typeName}
                {f.required && " · required"}
                {f.pii && ` · personal (${f.pii})`}
                {pages?.length ? (f.page === undefined ? (f.type === "hidden" ? "" : " · on no page") : ` · page ${f.page + 1}`) : ""}
              </span>
              <span className="grow" />
              <button type="button" className="small" aria-label={`Move ${f.label || f.name} up`} disabled={i === 0} onClick={() => move(i, -1)}>
                ↑
              </button>
              <button type="button" className="small" aria-label={`Move ${f.label || f.name} down`} disabled={i === fields.length - 1} onClick={() => move(i, 1)}>
                ↓
              </button>
              <button
                type="button"
                className="small danger"
                aria-label={`Remove ${f.label || f.name}`}
                onClick={() => {
                  onChange(fields.filter((_, j) => j !== i));
                  setOpen(null);
                }}
              >
                Remove
              </button>
            </div>
            {!isOpen && <ProblemList problems={mine} />}
            {isOpen && (
              <div className="fe-body">
                <div className="row">
                  <Field label="Question">
                    <input
                      value={f.label}
                      placeholder="What to ask"
                      onChange={(e) => {
                        const label = e.target.value;
                        if (!auto.current.has(f.name) || !label.trim()) return set(i, { label });
                        const name = nameFromLabel(label, others);
                        auto.current.delete(f.name);
                        auto.current.add(name);
                        const wasAuto = f.pii === "" || f.pii === suggestPii(f.name, f.type);
                        set(i, { label, name, pii: wasAuto ? suggestPii(name, f.type) : f.pii });
                      }}
                    />
                  </Field>
                  <NameInput
                    name={f.name}
                    taken={others}
                    onRename={(name) => {
                      auto.current.delete(f.name);
                      set(i, { name, pii: f.pii || suggestPii(name, f.type) });
                    }}
                  />
                </div>
                <div className="row">
                  <Field label="Type">
                    <select
                      value={f.type}
                      onChange={(e) => {
                        const type = e.target.value as FieldType;
                        const patch: Partial<FieldDraft> = { type };
                        if ((type === "choice" || type === "choices") && f.choices.length === 0) patch.choices = ["Option 1", "Option 2"];
                        if (type === "file" && f.accept.length === 0) patch.accept = ["application/pdf", "image/*"];
                        if (type === "money") patch.integer = true;
                        if (type === "hidden") patch.prefill = true;
                        if (!f.pii || f.pii === suggestPii(f.name, f.type)) patch.pii = suggestPii(f.name, type);
                        set(i, patch);
                      }}
                    >
                      {FIELD_TYPES.map(([t, l]) => (
                        <option key={t} value={t}>
                          {l}
                        </option>
                      ))}
                    </select>
                  </Field>
                  {f.type !== "hidden" && (
                    <label className="inline fe-check">
                      <input type="checkbox" checked={f.required} onChange={(e) => set(i, { required: e.target.checked })} /> Required
                    </label>
                  )}
                </div>
                {f.type !== "hidden" && (
                  <Field label="Help text" hint="Shown under the question">
                    <input value={f.help} onChange={(e) => set(i, { help: e.target.value })} />
                  </Field>
                )}
                {TEXTLIKE.has(f.type) && (
                  <Field label="Placeholder">
                    <input value={f.placeholder} onChange={(e) => set(i, { placeholder: e.target.value })} />
                  </Field>
                )}
                {(f.type === "number" || f.type === "money") && (
                  <label className="inline">
                    <input type="checkbox" checked={f.integer} onChange={(e) => set(i, { integer: e.target.checked })} /> {f.type === "money" ? "Whole naira only" : "Whole numbers only"}
                  </label>
                )}
                {(f.type === "choice" || f.type === "choices") && (
                  <Field label="Choices" hint="One per line">
                    <LinesInput
                      label="Choices"
                      value={f.choices.map(String)}
                      onChange={(lines) => set(i, { choices: lines.map((l) => f.choices.find((c) => String(c) === l) ?? l) })}
                    />
                  </Field>
                )}
                {f.type === "consent" && (
                  <Field label="What they agree to">
                    <textarea rows={3} value={f.consentText} onChange={(e) => set(i, { consentText: e.target.value })} />
                  </Field>
                )}
                {f.type === "file" && (
                  <>
                    <fieldset className="fe-accept">
                      <legend>Files taken</legend>
                      {ACCEPTS.map(([t, l]) => (
                        <label key={t} className="inline">
                          <input
                            type="checkbox"
                            checked={f.accept.includes(t)}
                            onChange={(e) => set(i, { accept: e.target.checked ? [...f.accept, t] : f.accept.filter((x) => x !== t) })}
                          />{" "}
                          {l}
                        </label>
                      ))}
                    </fieldset>
                    <Field label="Largest file (MB)" hint="Up to 25 MB; 10 MB when empty. Forms with files need a malware scanner on the deployment.">
                      <input
                        type="number"
                        min={1}
                        max={25}
                        value={f.maxMb ?? ""}
                        onChange={(e) => set(i, { maxMb: e.target.value === "" ? undefined : Number(e.target.value) })}
                      />
                    </Field>
                  </>
                )}
                {linkPrefill && (
                  <label className="inline">
                    <input type="checkbox" checked={f.prefill} onChange={(e) => set(i, { prefill: e.target.checked })} /> Can be filled in from the link (?{f.name}=…)
                  </label>
                )}
                {members && f.type !== "hidden" && (
                  <Field label="From the member's account" hint="Filled in and locked">
                    <select value={f.from} onChange={(e) => set(i, { from: e.target.value as FieldDraft["from"] })}>
                      <option value="">No</option>
                      <option value="member.name">Their name</option>
                      <option value="member.email">Their email</option>
                    </select>
                  </Field>
                )}
                <Field
                  label="Personal data"
                  hint={
                    suggested && suggested !== f.pii ? (
                      <>
                        Looks like {PII_CATEGORIES.find(([c]) => c === suggested)?.[1].toLowerCase() ?? suggested}.{" "}
                        <button type="button" className="small" onClick={() => set(i, { pii: suggested })}>
                          Mark it
                        </button>
                      </>
                    ) : (
                      "Personal answers are sealed when received and masked in the app."
                    )
                  }
                >
                  <select value={f.pii} onChange={(e) => set(i, { pii: e.target.value })}>
                    <option value="">Not personal</option>
                    {PII_CATEGORIES.map(([c, l]) => (
                      <option key={c} value={c}>
                        {l}
                      </option>
                    ))}
                    {f.pii && !PII_CATEGORIES.some(([c]) => c === f.pii) && <option value={f.pii}>{f.pii}</option>}
                  </select>
                </Field>
                {pages && pages.length > 0 && f.type !== "hidden" && (
                  <Field label="Page">
                    <select value={f.page ?? ""} onChange={(e) => set(i, { page: e.target.value === "" ? undefined : Number(e.target.value) })}>
                      <option value="">On no page</option>
                      {pages.map((p, j) => (
                        <option key={j} value={j}>
                          {j + 1}. {p.title || "Untitled"}
                        </option>
                      ))}
                    </select>
                  </Field>
                )}
                <ProblemList problems={mine} />
              </div>
            )}
          </div>
        );
      })}
      <button type="button" onClick={add}>
        + Add a question
      </button>
    </div>
  );
}

/** Pages: titles and conditions; fields choose their page. */
function PagesEditor({ pages, fields, onChange }: { pages: PageDraft[]; fields: FieldDraft[]; onChange: (pages: PageDraft[], fields: FieldDraft[]) => void }) {
  const asFormPages = pages.map((_, i) => ({ fields: fields.filter((f) => f.page === i).map((f) => f.name) }));
  if (pages.length === 0) {
    return (
      <p className="hint">
        One page with every question.{" "}
        <button type="button" onClick={() => onChange([{ title: "", show_if: "" }], fields.map((f) => (f.type === "hidden" ? f : { ...f, page: 0 })))}>
          Split into pages
        </button>
      </p>
    );
  }
  return (
    <div className="fe-pages">
      {pages.map((p, i) => {
        const earlier = earlierFields(asFormPages, i);
        return (
          <div key={i} className="fe-page" data-testid={`page-${i + 1}`}>
            <div className="row">
              <Field label={`Page ${i + 1} title`}>
                <input value={p.title} onChange={(e) => onChange(pages.map((x, j) => (j === i ? { ...x, title: e.target.value } : x)), fields)} />
              </Field>
              <button
                type="button"
                className="small danger"
                aria-label={`Remove page ${i + 1}`}
                onClick={() => {
                  // Its questions go to the page before (or the next one).
                  const to = i > 0 ? i - 1 : 0;
                  const next = pages.filter((_, j) => j !== i);
                  onChange(
                    next,
                    fields.map((f) => (f.page === undefined ? f : { ...f, page: next.length === 0 ? undefined : f.page === i ? to : f.page > i ? f.page - 1 : f.page })),
                  );
                }}
              >
                Remove
              </button>
            </div>
            {i > 0 && (
              <Field
                label="Show only when"
                hint={earlier.length ? <>An expression over earlier pages: {earlier.map((n) => <code key={n}>answers.{n}</code>).reduce<ReactNode[]>((a, c) => (a.length ? [...a, ", ", c] : [c]), [])}. For example answers.{earlier[0]} == true</> : "Earlier pages have no questions yet."}
              >
                <input className="mono" value={p.show_if} placeholder="Always shown" onChange={(e) => onChange(pages.map((x, j) => (j === i ? { ...x, show_if: e.target.value } : x)), fields)} />
              </Field>
            )}
            <div className="hint">Questions: {asFormPages[i]?.fields.join(", ") || "none yet (choose the page on a question)"}</div>
          </div>
        );
      })}
      <button type="button" onClick={() => onChange([...pages, { title: "", show_if: "" }], fields)}>
        + Add a page
      </button>
    </div>
  );
}

type Def = WorkflowDefinition;

/** The trigger panel for a form trigger. */
export function FormTriggerEditor({ def, onChange, problems }: { def: Def; onChange: (d: Def) => void; problems: Problem[] }) {
  const t = def.trigger;
  const cfg = (t.config ?? {}) as FormTriggerConfig;
  const schema = (typeof def.inputs?.schema === "object" ? def.inputs.schema : undefined) as FormSchema | undefined;
  const { fields, pages } = useMemo(() => loadFields(schema, cfg.pages), [schema, cfg.pages]);
  const access = cfg.access ?? "public";
  const after = cfg.after ?? {};
  const setCfg = (patch: Record<string, unknown>) => onChange({ ...def, trigger: { ...t, config: merge(cfg as Record<string, unknown>, patch) } });
  const setAfter = (patch: Record<string, unknown>) => {
    const a = merge(after as Record<string, unknown>, patch);
    setCfg({ after: Object.keys(a).length ? a : undefined });
  };
  const setFields = (fs: FieldDraft[], ps: PageDraft[] = pages) => {
    const out = saveFields(fs, schema, ps);
    onChange({ ...def, inputs: { ...def.inputs, schema: out.schema }, trigger: { ...t, config: merge(cfg as Record<string, unknown>, { pages: out.pages }) } });
  };
  const all = mergeProblems(
    formProblems(cfg, schema),
    problems.filter((p) => p.path.startsWith("/trigger") || p.path.startsWith("/inputs")),
  );
  const fieldProblems = all.filter((p) => p.path.startsWith("/inputs/schema/properties/")).map((p) => ({ ...p, path: p.path.slice("/inputs/schema".length) }));
  const formLevel = all.filter((p) => !p.path.startsWith("/inputs/schema/properties/"));
  const preview = saveFields(fields, schema, pages);
  const memberValues = Object.fromEntries(fields.filter((f) => f.from).map((f) => [f.name, f.from === "member.name" ? "Ada Obi (the member's name)" : "ada@example.com (the member's email)"]));
  return (
    <section className="form-editor" aria-label="Form">
      <div className="card">
        <h2>Form</h2>
        <p className="hint">
          People fill this in at a link, on your website or in the app; each answer starts a run with the answers as <code>trigger.body</code>.
        </p>
        <LiveForms workflow={def.id} />
        <ProblemList problems={formLevel} label="Form problems" />
        <Field label="Form title" hint="Shown at the top. Not the name of a bank, payment provider or government agency.">
          <input value={cfg.title ?? ""} onChange={(e) => setCfg({ title: e.target.value })} />
        </Field>
        <Field label="Form description" hint="What it is for; shown under the title">
          <textarea rows={2} value={cfg.description ?? ""} onChange={(e) => setCfg({ description: e.target.value })} />
        </Field>
        <fieldset className="fe-access">
          <legend>Who can fill it in</legend>
          {(
            [
              ["public", "Anyone with the link", "Also on your verified websites"],
              ["link", "Only people you send a one-time link", "Make links on the Forms page"],
              ["members", "Members of your organisation", "Signed in to Taskiem, in the app"],
            ] as const
          ).map(([v, l, h]) => (
            <label key={v} className="choice">
              <input type="radio" name="form-access" checked={access === v} onChange={() => setCfg({ access: v === "public" ? undefined : v, ...(v === "members" && { verify: undefined }), ...(v !== "members" && { roles: undefined }) })} />
              <span>
                {l} <span className="hint">{h}</span>
              </span>
            </label>
          ))}
        </fieldset>
        {access === "members" && (
          <Field label="Only these roles" hint="Comma-separated; empty for every member">
            <input
              value={(cfg.roles ?? []).join(", ")}
              onChange={(e) => {
                const roles = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
                setCfg({ roles: roles.length ? roles : undefined });
              }}
            />
          </Field>
        )}
        <div className="row">
          {access !== "members" && (
            <Field label="Check who they are" hint="A 6-digit code sent before they submit">
              <select value={cfg.verify ?? ""} onChange={(e) => setCfg({ verify: e.target.value || undefined })}>
                <option value="">No code</option>
                <option value="phone">A code to their phone</option>
                <option value="email">A code to their email</option>
              </select>
            </Field>
          )}
          <Field label="One answer per">
            <select value={cfg.once_per ?? ""} onChange={(e) => setCfg({ once_per: e.target.value || undefined })}>
              <option value="">No limit</option>
              <option value="phone">Phone number</option>
              <option value="email">Email address</option>
              <option value="member">Member</option>
            </select>
          </Field>
        </div>
        <h3>After they send it</h3>
        <Field label="They see">
          <select value={after.mode ?? "message"} onChange={(e) => setAfter({ mode: e.target.value === "message" ? undefined : e.target.value })}>
            <option value="message">A thank-you message</option>
            <option value="redirect">One of your web pages</option>
            <option value="result">The workflow's answer (up to 30 seconds)</option>
          </select>
        </Field>
        {after.mode === "redirect" ? (
          <Field label="Web page" hint="An https address on one of your verified websites">
            <input value={after.url ?? ""} placeholder="https://" onChange={(e) => setAfter({ url: e.target.value })} />
          </Field>
        ) : (
          <Field label="Thank-you message" hint={after.mode === "result" ? "Shown if the workflow has not answered in time" : "With the reference of their answers"}>
            <textarea rows={2} value={after.message ?? ""} placeholder="Thank you. We will be in touch." onChange={(e) => setAfter({ message: e.target.value })} />
          </Field>
        )}
        <label className="inline">
          <input type="checkbox" checked={after.status === true} onChange={(e) => setAfter({ status: e.target.checked || undefined })} /> Give them a status link
        </label>

        <h3>Questions</h3>
        <FieldsEditor fields={fields} onChange={(fs) => setFields(fs)} pages={pages} linkPrefill={access !== "members"} members={access === "members"} problems={fieldProblems} />
        <h3>Pages</h3>
        <PagesEditor pages={pages} fields={fields} onChange={(ps, fs) => setFields(fs, ps)} />
        <details>
          <summary>Edit the questions as JSON (the inputs schema)</summary>
          <JsonInput value={def.inputs?.schema} onChange={(v) => onChange({ ...def, inputs: v === undefined ? undefined : { schema: v as object } } as Def)} rows={12} />
        </details>
      </div>
      <div className="card form-preview" role="region" aria-label="Preview">
        <h2>Preview</h2>
        <p className="hint">As people see it{access === "members" ? " in the app" : ""}. Conditions on pages are tried here when they are simple; the server decides.</p>
        <FormView key={Object.keys(memberValues).join(",")} title={cfg.title} description={cfg.description} schema={preview.schema} pages={preview.pages} preview locked={Object.keys(memberValues)} initial={memberValues} />
      </div>
    </section>
  );
}

/** Where this workflow's form is live, per environment. */
function LiveForms({ workflow }: { workflow: string }) {
  const [forms, setForms] = useState<FormInfo[]>();
  useEffect(() => {
    let live = true;
    get<{ forms: FormInfo[] }>("/v1/forms").then(
      (r) => live && setForms(r.forms.filter((f) => f.workflow_id === workflow)),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [workflow]);
  if (!forms?.length) return <p className="hint">Publishing the workflow puts the form live; its link, embed code and answers are on the <Link to="/forms">Forms</Link> page.</p>;
  return (
    <ul className="fe-live">
      {forms.map((f) => (
        <li key={f.id}>
          <strong>{f.environment}</strong> <Badge value={f.status} /> v{f.version} · {f.submissions_this_week} this week ·{" "}
          {f.url ? (
            <a href={f.url} target="_blank" rel="noreferrer">
              open the form
            </a>
          ) : f.access === "members" ? (
            <Link to={`/forms/${f.id}`}>open the form</Link>
          ) : null}{" "}
          · <Link to="/forms">manage</Link>
        </li>
      ))}
    </ul>
  );
}

/** The configuration editor of a form step. Problems are relative to the step's config. */
export function FormStepEditor({ cfg, onChange, problems }: { cfg: FormStepConfig; onChange: (cfg: FormStepConfig) => void; problems: Problem[] }) {
  const schema = cfg.schema;
  const { fields } = useMemo(() => loadFields(schema), [schema]);
  const set = (patch: Record<string, unknown>) => onChange(merge(cfg as Record<string, unknown>, patch) as FormStepConfig);
  const to = cfg.to ?? {};
  const kind: "phone" | "email" | "role" = to.role !== undefined ? "role" : to.email !== undefined ? "email" : "phone";
  const toValue = to[kind] ?? "";
  const all = mergeProblems(stepProblems(cfg), problems);
  const fieldProblems = all.filter((p) => p.path.startsWith("/schema/properties/")).map((p) => ({ ...p, path: p.path.slice("/schema".length) }));
  const rest = all.filter((p) => !p.path.startsWith("/schema/properties/"));
  const via = cfg.via ?? [];
  const prefill = cfg.prefill ?? {};
  return (
    <div className="form-step-editor">
      <ProblemList problems={rest} label="Form step problems" />
      <Field label="Title" hint="What the person is asked to fill in">
        <input value={cfg.title ?? ""} onChange={(e) => set({ title: e.target.value })} />
      </Field>
      <Field label="Description">
        <textarea rows={2} value={cfg.description ?? ""} onChange={(e) => set({ description: e.target.value })} />
      </Field>
      <div className="row">
        <Field label="Send to">
          <select
            value={kind}
            onChange={(e) => {
              const k = e.target.value;
              set({ to: { [k]: k === "role" ? "" : toValue.startsWith("=") ? toValue : "" }, ...(k === "role" && { via: undefined, verify: undefined }) });
            }}
          >
            <option value="phone">A phone number</option>
            <option value="email">An email address</option>
            <option value="role">Members with a role</option>
          </select>
        </Field>
        <Field label={kind === "role" ? "Role" : kind === "phone" ? "Phone (expression)" : "Email (expression)"} hint={kind === "role" ? "They find it under To fill in" : `e.g. =trigger.body.${kind}`}>
          <input className={kind === "role" ? undefined : "mono"} value={toValue} onChange={(e) => set({ to: { [kind]: e.target.value } })} />
        </Field>
      </div>
      {kind !== "role" && (
        <>
          <fieldset className="fe-via">
            <legend>Send the link by</legend>
            {(["sms", "email"] as const).map((v) => (
              <label key={v} className="inline">
                <input
                  type="checkbox"
                  checked={via.length === 0 || via.includes(v)}
                  onChange={(e) => {
                    const cur = via.length === 0 ? ["sms", "email"] : via;
                    const next = e.target.checked ? ["sms", "email"].filter((x) => x === v || cur.includes(x)) : cur.filter((x) => x !== v);
                    set({ via: next.length === 2 ? undefined : next });
                  }}
                />{" "}
                {v === "sms" ? "SMS" : "Email"}
              </label>
            ))}
            <div className="hint">The first channel that reaches them, in this order; again at each reminder.</div>
          </fieldset>
          <label className="inline">
            <input type="checkbox" checked={cfg.verify === true} onChange={(e) => set({ verify: e.target.checked || undefined })} /> Ask for a code sent to them before they answer
          </label>
        </>
      )}
      <h3>Questions</h3>
      <FieldsEditor
        fields={fields}
        onChange={(fs) => {
          const out = saveFields(fs, schema).schema;
          const names = fs.map((f) => f.name);
          const p = Object.fromEntries(Object.entries(prefill).filter(([k]) => names.includes(k)));
          set({ schema: out, prefill: Object.keys(p).length ? p : undefined });
        }}
        problems={fieldProblems}
      />
      {fields.length > 0 && (
        <details open={Object.keys(prefill).length > 0}>
          <summary>Fill in answers from the run</summary>
          {fields.map((f) => (
            <Field key={f.name} label={`${f.label || f.name} (prefill)`}>
              <input
                className="mono"
                value={typeof prefill[f.name] === "string" ? (prefill[f.name] as string) : prefill[f.name] === undefined ? "" : JSON.stringify(prefill[f.name])}
                placeholder="=trigger.body.…"
                onChange={(e) => {
                  const p = merge(prefill, { [f.name]: e.target.value });
                  set({ prefill: Object.keys(p).length ? p : undefined });
                }}
              />
            </Field>
          ))}
        </details>
      )}
      <div className="row">
        <Field label="Wait for" hint="Default 7d">
          <input value={cfg.timeout ?? ""} placeholder="7d" onChange={(e) => set({ timeout: e.target.value })} />
        </Field>
        <Field label="Reminders after" hint="Comma-separated, e.g. 24h, 60h">
          <input
            value={(cfg.remind ?? []).join(", ")}
            onChange={(e) => {
              const r = e.target.value.split(",").map((s) => s.trim()).filter(Boolean);
              set({ remind: r.length ? r : undefined });
            }}
          />
        </Field>
      </div>
      <Field label="If nobody answers in time">
        <select value={cfg.on_timeout ?? "fail"} onChange={(e) => set({ on_timeout: e.target.value === "fail" ? undefined : e.target.value })}>
          <option value="fail">Fail the step</option>
          <option value="continue">Carry on (output timed_out: true)</option>
        </select>
      </Field>
      <details>
        <summary>Preview</summary>
        <FormView title={cfg.title} description={cfg.description} schema={schema} preview />
      </details>
      <details>
        <summary>Edit the configuration as JSON</summary>
        <JsonInput value={cfg} onChange={(v) => v && typeof v === "object" && onChange(v as FormStepConfig)} rows={14} />
      </details>
    </div>
  );
}

/** A starting form trigger for a workflow switching to Form. */
export function startFormTrigger(def: Def): Def {
  const schema = (typeof def.inputs?.schema === "object" ? def.inputs.schema : undefined) as FormSchema | undefined;
  const has = Object.keys(schema?.properties ?? {}).length > 0;
  const starter: FormSchema = {
    type: "object",
    required: ["full_name"],
    properties: { full_name: { type: "string", title: "Full name", "x-pii": "name" } },
  };
  return { ...def, trigger: { type: "form", config: { title: def.name } }, inputs: has ? def.inputs : { schema: starter } };
}

