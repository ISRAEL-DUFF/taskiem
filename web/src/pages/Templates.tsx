import { useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, get, post } from "../api";
import { useAuth } from "../auth";
import { Help } from "../onboarding";
import { byCategory, initialValues, inputKind, lineLabel, toParams, WEEKDAYS, type Template, type TemplateParam } from "../lib/templates";
import { Badge, ErrorBox, Field, PageHeader, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { RolePicker } from "../forms/RolePicker";
import { ConnectionPicker } from "../forms/ConnectionPicker";
import { SheetChooser } from "../forms/SheetChooser";
import { ColumnPicker, LanguagePicker, TabPicker, TablePicker, WhatsAppTemplatePicker } from "../forms/Pickers";

// The SME template library (docs/templates.md): browse ready workflows,
// read their steps in plain words, and create a draft from one by filling
// in its parameters. Nothing is published from here.

export function Templates() {
  const { can } = useAuth();
  const [params, setParams] = useSearchParams();
  const [q, setQ] = useState(params.get("q") ?? "");
  const query = params.get("q") ?? "";
  const { data, error } = useLoad(() => get<{ templates: Template[] }>(`/v1/templates${query ? `?q=${encodeURIComponent(query)}` : ""}`), [query]);
  const selected = params.get("id");
  const groups = useMemo(() => byCategory(data?.templates ?? []), [data]);
  const current = data?.templates.find((t) => t.id === selected);
  return (
    <>
      <PageHeader title="Templates" description="Ready workflows for small businesses. Pick one, fill in the details, and it becomes a draft you can review and publish." />
      <form
        className="describe-bar"
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          setParams(q.trim() ? { q: q.trim() } : {});
        }}
      >
        <Icon name="search" />
        <input aria-label="Search templates" placeholder="What should it do? e.g. every Friday text my customers who owe me" value={q} onChange={(e) => setQ(e.target.value)} />
        <button type="submit" className="primary">
          Search
        </button>
        {query && (
          <button
            type="button"
            onClick={() => {
              setQ("");
              setParams({});
            }}
          >
            Show all
          </button>
        )}
      </form>
      <Help topic="templates" />
      <ErrorBox error={error} />
      {data && data.templates.length === 0 && <div className="empty card">No template matches. Try other words, or build it with AI from the Workflows page.</div>}
      {current ? (
        <TemplateDetail
          key={current.id}
          template={current}
          canCreate={can("workflow.edit")}
          onBack={() => setParams(query ? { q: query } : {})}
        />
      ) : (
        groups.map(([category, list]) => (
          <section key={category}>
            <h2 className="capitalize">{category}</h2>
            <div className="template-grid">
              {list.map((t) => (
                <button key={t.id} className="card template-card" onClick={() => setParams(query ? { q: query, id: t.id } : { id: t.id })}>
                  <strong>{t.title}</strong>
                  <span>{t.summary}</span>
                  <span className="hint">
                    {t.connectors.map((c) => c.split("@")[0]).join(" · ")}
                    {!t.available && " · needs a connector not available here"}
                    {t.needs_developer && " · needs a developer"}
                  </span>
                </button>
              ))}
            </div>
          </section>
        ))
      )}
    </>
  );
}

function TemplateDetail({ template: t, canCreate, onBack }: { template: Template; canCreate: boolean; onBack: () => void }) {
  const nav = useNavigate();
  const [values, setValues] = useState(() => initialValues(t.params));
  const [name, setName] = useState("");
  const tables = t.tables ?? [];
  const act = useAction();
  const fieldErrors = useMemo(() => {
    const e = act.error;
    const out: Record<string, string> = {};
    if (e instanceof ApiError && Array.isArray(e.body.params)) {
      for (const p of e.body.params as { param: string; message: string }[]) out[p.param] = p.message;
    }
    return out;
  }, [act.error]);
  const create = () =>
    act.run(async () => {
      const r = await post<{ id: string; tables?: { name: string; status: string; error?: string }[] }>(`/v1/templates/${t.id}/instantiate`, {
        params: toParams(t.params, values),
        name: name.trim() || undefined,
      });
      const notMade = (r.tables ?? []).filter((x) => x.status === "failed" || x.status === "skipped");
      if (notMade.length > 0) {
        throw new Error(
          `The draft was created (find it under Workflows), but ${notMade.map((x) => `the table ${x.name} could not be made: ${x.error ?? "unknown reason"}`).join("; ")}.`,
        );
      }
      nav(`/workflows/${r.id}`);
    });
  return (
    <div className="card">
      <div className="toolbar">
        <h2 className="grow" style={{ margin: 0 }}>
          {t.title}
        </h2>
        {canCreate && t.available && <Link to={`/start/guide?template=${t.id}`}>Set it up step by step</Link>}
        <button onClick={onBack}>All templates</button>
      </div>
      <p>{t.description}</p>
      <p>
        {t.tags.map((tag) => (
          <Badge key={tag} value={tag} />
        ))}
      </p>
      <h3>Steps</h3>
      <ol className="plain-steps" aria-label="Steps">
        {t.steps.map((l, i) => (
          <li key={i} style={{ marginLeft: `${l.depth * 1.5}em` }}>
            {lineLabel(l)}
          </li>
        ))}
      </ol>
      {t.variables.length > 0 && (
        <div className="notice">
          Before it runs, set these variables in <a href="/settings">Secrets &amp; settings</a>:
          <ul>
            {t.variables.map((v) => (
              <li key={v.name}>
                <code>{v.name}</code>: {v.why}
              </li>
            ))}
          </ul>
        </div>
      )}
      {tables.length > 0 && (
        <div className="notice">
          It keeps its records in {tables.length === 1 ? "a data table" : "data tables"} Taskiem makes for you if you do not have {tables.length === 1 ? "it" : "them"} yet:
          <ul>
            {tables.map((tb) => (
              <li key={tb.name}>
                <strong>{tb.title || tb.name}</strong> ({tb.columns.map((c) => c.title || c.name).join(", ")})
              </li>
            ))}
          </ul>
        </div>
      )}
      {!t.available && <div className="notice">This template uses a connector that is not available in this organisation yet.</div>}
      {t.needs_developer && (
        <div className="notice">
          <strong>Needs a developer.</strong> It starts when another system (your website or shop) calls a web address Taskiem gives you, so someone technical
          has to set that up. Templates that start on a schedule or from a form need nobody.
        </div>
      )}
      {canCreate ? (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void create();
          }}
        >
          <h3>Details</h3>
          {t.params.map((p) => (
            <ParamField
              key={p.name}
              param={p}
              params={t.params}
              values={values}
              value={values[p.name] ?? ""}
              error={fieldErrors[p.name]}
              onChange={(v) => setValues((old) => ({ ...old, [p.name]: v }))}
              onOther={(name, v) => setValues((old) => ({ ...old, [name]: v }))}
            />
          ))}
          <Field label="Workflow name" hint="Leave empty to use the template's name.">
            <input value={name} maxLength={200} onChange={(e) => setName(e.target.value)} />
          </Field>
          <ErrorBox error={Object.keys(fieldErrors).length > 0 ? null : act.error} />
          <div className="toolbar">
            <button type="submit" className="primary" disabled={act.busy}>
              Create draft
            </button>
          </div>
          <p className="hint">The draft is checked like any workflow and does nothing until it is published.</p>
        </form>
      ) : (
        <p className="hint">Creating a workflow from a template takes the workflow.edit permission.</p>
      )}
    </div>
  );
}

export function ParamField({
  param: p,
  value,
  error,
  onChange,
  params = [],
  values = {},
  onOther,
}: {
  param: TemplateParam;
  value: string;
  error?: string;
  onChange: (v: string) => void;
  /** The template's other parameters and their values, for pickers that depend on one. */
  params?: TemplateParam[];
  values?: Record<string, string>;
  onOther?: (name: string, v: string) => void;
}) {
  const kind = inputKind(p);
  const label = p.required ? p.title : `${p.title} (optional)`;
  const hint = (
    <>
      {p.description}
      {p.example !== undefined && kind !== "checkbox" && <> For example: {String(p.example)}</>}
      {error && (
        <span className="error-text" role="alert">
          {" "}
          {error}
        </span>
      )}
    </>
  );
  let input;
  // A connection is picked from the business's own, with Connect in place.
  if (p.type === "connection") {
    return (
      <Field label={label} hint={hint}>
        <ConnectionPicker label={label} connector={p.connector ?? ""} value={value} onChange={onChange} />
      </Field>
    );
  }
  const from = p.from ? (values[p.from] ?? "") : "";
  const picked = (input: ReactNode) => (
    <Field label={label} hint={hint}>
      {input}
    </Field>
  );
  switch (p.picker) {
    case "sheet_tab":
      return picked(<TabPicker label={label} sheet={from} value={value} onChange={onChange} />);
    case "table":
      return picked(<TablePicker label={label} value={value} onChange={onChange} />);
    case "column":
      return picked(<ColumnPicker label={label} table={from} value={value} onChange={onChange} />);
    case "whatsapp_template": {
      // Choosing the template fills in its language too.
      const lang = params.find((x) => x.picker === "whatsapp_language" && x.from === p.name);
      return picked(
        <WhatsAppTemplatePicker
          label={label}
          value={value}
          language={lang ? values[lang.name] : undefined}
          onChange={(name, language) => {
            onChange(name);
            if (lang && language) onOther?.(lang.name, language);
          }}
        />,
      );
    }
    case "whatsapp_language":
      return picked(<LanguagePicker label={label} value={value} onChange={onChange} />);
  }
  // A spreadsheet is chosen by its link, never an id.
  if (p.picker === "sheet" || p.name === "spreadsheet_id" || p.name.endsWith("_spreadsheet_id")) {
    return (
      <Field label={label} hint={hint}>
        <SheetChooser value={value} onChange={onChange} />
      </Field>
    );
  }
  // A role a template's approval names is picked, never typed.
  if (/(^|_)role$/.test(p.name) && p.type === "string") {
    return (
      <Field label={label} hint={hint}>
        <RolePicker label={label} value={value} onChange={onChange} />
      </Field>
    );
  }
  switch (kind) {
    case "textarea":
      input = <textarea rows={3} value={value} maxLength={p.max_length ?? 900} onChange={(e) => onChange(e.target.value)} aria-invalid={!!error} />;
      break;
    case "number":
      input = <input type="number" step="any" min={p.minimum} max={p.maximum} value={value} onChange={(e) => onChange(e.target.value)} aria-invalid={!!error} />;
      break;
    case "time":
      input = <input type="time" value={value} onChange={(e) => onChange(e.target.value)} aria-invalid={!!error} />;
      break;
    case "checkbox":
      input = <input type="checkbox" checked={value === "true"} onChange={(e) => onChange(e.target.checked ? "true" : "false")} />;
      break;
    case "select": {
      const options = p.enum && p.enum.length > 0 ? p.enum : WEEKDAYS;
      input = (
        <select value={value} onChange={(e) => onChange(e.target.value)} aria-invalid={!!error}>
          <option value="">Choose…</option>
          {options.map((o) => (
            <option key={o} value={o}>
              {o}
            </option>
          ))}
        </select>
      );
      break;
    }
    default:
      input = <input value={value} maxLength={p.max_length ?? 200} onChange={(e) => onChange(e.target.value)} aria-invalid={!!error} />;
  }
  return (
    <Field label={label} hint={hint}>
      {input}
    </Field>
  );
}
