import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, get, post, put, type ConnectorInfo, type JSONSchema, type Problem } from "../api";
import { useAuth } from "../auth";
import { Help, useOnboarding } from "../onboarding";
import { CHECKLIST, formatDuration, sampleInput, starterTemplates } from "../lib/onboarding";
import { initialValues, toParams, type Template } from "../lib/templates";
import { Badge, ErrorBox, Field, JsonInput, useAction, useLoad } from "../ui";
import { ParamField } from "./Templates";

// Get started (docs/onboarding.md): the checklist, derived from what the
// organisation has done, and the guided first workflow: pick a template,
// connect its app, create and publish it, run it once.

export function Start() {
  const { can } = useAuth();
  const { data, reload } = useOnboarding();
  const act = useAction();
  const [sent, setSent] = useState<string | null>(null);
  const nav = useNavigate();
  if (!data) return <div className="empty">Loading…</div>;
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Get started
        </h1>
        <span className="hint" data-testid="onboarding-progress">
          {data.done} of {data.total} done
        </span>
        {!data.dismissed && can("workflow.edit") && (
          <button onClick={() => void act.run(async () => (await post("/v1/onboarding/dismiss"), reload(), nav("/workflows")))}>Hide this</button>
        )}
      </div>
      <ErrorBox error={act.error} />
      {data.seconds_to_first_run !== null && (
        <div className="notice" role="status" data-testid="first-run-time">
          Your first successful run came {formatDuration(data.seconds_to_first_run)} after you signed up.
        </div>
      )}
      <ol className="checklist" aria-label="Checklist">
        {data.items.map((it) => {
          const c = CHECKLIST[it.id];
          return (
            <li key={it.id} className={it.done ? "done" : ""} data-testid={`check-${it.id}`}>
              <span className="check" aria-label={it.done ? "done" : "to do"}>
                {it.done ? "✓" : "○"}
              </span>
              <div className="grow">
                <strong>{c.title}</strong>
                <div className="hint">{c.text}</div>
              </div>
              {!it.done &&
                (it.id === "verify_email" ? (
                  data.can_send_email && (
                    <button disabled={act.busy} onClick={() => void act.run(async () => setSent((await post<{ status?: string }>("/v1/me/email/verify/resend")).status ?? "Your email is confirmed."))}>
                      {c.action}
                    </button>
                  )
                ) : (
                  <Link to={c.href}>{c.action}</Link>
                ))}
            </li>
          );
        })}
      </ol>
      {sent && <p role="status">{sent}</p>}
      {can("workflow.edit") && (
        <section>
          <h2>Your first workflow</h2>
          <p>Pick a ready workflow below. We walk you through connecting it, publishing it and running it once.</p>
          <TemplatePicker />
        </section>
      )}
      <Help topic="start" />
    </>
  );
}

/** The starter templates as cards; a card opens the guide. */
function TemplatePicker() {
  const { data, error } = useLoad(() => get<{ templates: Template[] }>("/v1/templates"), []);
  const list = useMemo(() => starterTemplates(data?.templates ?? []), [data]);
  const [all, setAll] = useState(false);
  return (
    <>
      <ErrorBox error={error} />
      <div className="template-grid">
        {(all ? list : list.slice(0, 6)).map((t) => (
          <Link key={t.id} className="card template-card" to={`/start/guide?template=${t.id}`}>
            <strong>{t.title}</strong>
            <span>{t.summary}</span>
            <span className="hint">{t.connectors.map((c) => c.split("@")[0]).join(" · ")}</span>
          </Link>
        ))}
      </div>
      {!all && list.length > 6 && <button onClick={() => setAll(true)}>Show all {list.length} templates</button>}
    </>
  );
}

interface Conn {
  connector: string;
  name: string;
  environment: string;
}

const ENV = "prod"; // publishing deploys to every environment; runs default to prod

/** The guided first workflow, for one template. */
export function Guide() {
  const [params] = useSearchParams();
  const id = params.get("template");
  const { data: t, error } = useLoad(() => (id ? get<Template>(`/v1/templates/${id}`) : Promise.resolve(undefined)), [id]);
  if (!id) {
    return (
      <>
        <h1>Pick a template</h1>
        <TemplatePicker />
      </>
    );
  }
  return (
    <>
      <p>
        <Link to="/start">← Get started</Link>
      </p>
      <ErrorBox error={error} />
      {t && <GuideSteps key={t.id} t={t} />}
    </>
  );
}

function GuideSteps({ t }: { t: Template }) {
  const onboarding = useOnboarding();
  const [values, setValues] = useState(() => initialValues(t.params));
  const conns = useLoad(() => get<{ connections: Conn[] }>("/v1/connections"), []);
  const connectors = useLoad(() => get<{ connectors: ConnectorInfo[] }>("/v1/connectors"), []);
  const vars = useLoad(() => get<{ variables: { environment: string; name: string }[] }>("/v1/variables"), []);
  const [wf, setWf] = useState<{ id: string; published: boolean; problems: Problem[] } | null>(null);

  // Which connection each connector uses: the template's connection
  // parameter for it, else "main".
  const needs = t.connectors.map((ref) => {
    const cid = ref.split("@")[0];
    const p = t.params.find((x) => x.type === "connection" && x.connector === cid);
    const name = (p ? values[p.name] : "") || "main";
    const have = !!conns.data?.connections.some((c) => c.connector === cid && c.name === name && c.environment === ENV);
    return { ref, cid, name, have, info: connectors.data?.connectors.find((c) => c.ref === ref || c.id === cid) };
  });
  const missingVars = t.variables.filter((v) => !vars.data?.variables.some((x) => x.name === v.name && x.environment === ENV));
  const connected = conns.data !== undefined && needs.every((n) => n.have) && vars.data !== undefined && missingVars.length === 0;

  return (
    <>
      <h1>{t.title}</h1>
      <p>{t.description}</p>
      <ol className="guide">
        <li>
          <h2>Fill in the details</h2>
          {t.params.length === 0 && <p className="hint">Nothing to fill in.</p>}
          {t.params.map((p) => (
            <ParamField key={p.name} param={p} value={values[p.name] ?? ""} onChange={(v) => setValues({ ...values, [p.name]: v })} />
          ))}
        </li>
        <li>
          <h2>Connect {needs.map((n) => n.info?.name ?? n.cid).join(" and ")}</h2>
          {needs.map((n) =>
            n.have ? (
              <p key={n.ref} data-testid={`connected-${n.cid}`}>
                <Badge value="ok" /> {n.info?.name ?? n.cid} is connected as <code>{n.name}</code>.
              </p>
            ) : (
              n.info && <ConnectForm key={n.ref} info={n.info} name={n.name} onDone={conns.reload} />
            ),
          )}
          {t.variables.map((v) => (
            <VariableForm key={v.name} name={v.name} why={v.why} saved={!missingVars.includes(v)} onDone={vars.reload} />
          ))}
          <Help topic="connections" />
        </li>
        <li>
          <h2>Create and publish</h2>
          <CreateAndPublish t={t} values={values} ready={connected} wf={wf} onDone={setWf} />
        </li>
        <li>
          <h2>Run it once</h2>
          {wf?.published ? <TestRun workflow={wf.id} onDone={onboarding.reload} /> : <p className="hint">Publish first.</p>}
        </li>
      </ol>
    </>
  );
}

function ConnectForm({ info, name, onDone }: { info: ConnectorInfo; name: string; onDone: () => void }) {
  const [creds, setCreds] = useState<Record<string, string>>({});
  const act = useAction();
  return (
    <form
      className="card"
      aria-label={`Connect ${info.name}`}
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => (await post("/v1/connections", { connector: info.ref, environment: ENV, name, credentials: creds }), onDone()));
      }}
    >
      <strong>
        {info.name} <span className="hint">(saved as {name})</span>
      </strong>
      {info.auth.fields?.map((f) => (
        <Field key={f.key} label={f.label + (f.required === false ? " (optional)" : "")}>
          <input
            type={f.secret ? "password" : "text"}
            autoComplete="off"
            value={creds[f.key] ?? ""}
            required={f.required !== false}
            onChange={(e) => setCreds({ ...creds, [f.key]: e.target.value })}
          />
        </Field>
      ))}
      <ErrorBox error={act.error} />
      <button type="submit" className="primary" disabled={act.busy}>
        Save connection
      </button>
    </form>
  );
}

function VariableForm({ name, why, saved, onDone }: { name: string; why: string; saved: boolean; onDone: () => void }) {
  const [value, setValue] = useState("");
  const act = useAction();
  if (saved) {
    return (
      <p data-testid={`variable-${name}`}>
        <Badge value="ok" /> <code>{name}</code> is set.
      </p>
    );
  }
  return (
    <form
      className="card"
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => (await put(`/v1/variables/${ENV}/${name}`, { value: value.trim() }), onDone()));
      }}
    >
      <Field label={name} hint={why}>
        <input value={value} onChange={(e) => setValue(e.target.value)} required />
      </Field>
      <ErrorBox error={act.error} />
      <button type="submit" className="primary" disabled={act.busy}>
        Save {name}
      </button>
    </form>
  );
}

function CreateAndPublish({
  t,
  values,
  ready,
  wf,
  onDone,
}: {
  t: Template;
  values: Record<string, string>;
  ready: boolean;
  wf: { id: string; published: boolean; problems: Problem[] } | null;
  onDone: (wf: { id: string; published: boolean; problems: Problem[] }) => void;
}) {
  const act = useAction();
  const fieldErrors = act.error instanceof ApiError && Array.isArray(act.error.body.params) ? (act.error.body.params as { param: string; message: string }[]) : [];
  if (wf?.published) {
    return (
      <p role="status">
        <Badge value="published" /> Published. <Link to={`/workflows/${wf.id}`}>Open it in the editor</Link>
      </p>
    );
  }
  return (
    <>
      {!ready && <p className="hint">Connect it first: the workflow cannot run without its connection and settings.</p>}
      {wf && wf.problems.length > 0 && (
        <div className="error" role="alert">
          The draft needs changes before it can be published. <Link to={`/workflows/${wf.id}`}>Open it in the editor</Link>
          <ul>
            {wf.problems.map((p, i) => (
              <li key={i}>{`${p.path}: ${p.message}`}</li>
            ))}
          </ul>
        </div>
      )}
      {fieldErrors.length > 0 && (
        <ul className="error" role="alert">
          {fieldErrors.map((f) => (
            <li key={f.param}>{`${f.param}: ${f.message}`}</li>
          ))}
        </ul>
      )}
      <ErrorBox error={fieldErrors.length > 0 ? null : act.error} />
      <button
        className="primary"
        disabled={!ready || act.busy}
        onClick={() =>
          void act.run(async () => {
            let id = wf?.id;
            if (!id) {
              const r = await post<{ id: string; problems: Problem[] }>(`/v1/templates/${t.id}/instantiate`, { params: toParams(t.params, values) });
              id = r.id;
              if (r.problems.length > 0) {
                onDone({ id, published: false, problems: r.problems });
                return;
              }
            }
            const p = await post<{ state: string }>(`/v1/workflows/${id}/versions/1/publish`);
            onDone({ id, published: p.state === "published", problems: [] });
          })
        }
      >
        {act.busy ? "Publishing…" : "Create and publish"}
      </button>
    </>
  );
}

const terminal = ["completed", "failed", "cancelled", "needs_reconciliation"];

function TestRun({ workflow, onDone }: { workflow: string; onDone: () => void }) {
  const version = useLoad(() => get<{ definition: { inputs?: { schema?: JSONSchema }; types?: Record<string, JSONSchema> } }>(`/v1/workflows/${workflow}/versions/1`), [workflow]);
  const [input, setInput] = useState<unknown>(undefined);
  const [run, setRun] = useState<string | null>(null);
  const act = useAction();
  useEffect(() => {
    if (version.data && input === undefined) setInput(sampleInput(version.data.definition.inputs?.schema, version.data.definition.types));
  }, [version.data, input]);
  const status = useLoad(() => (run ? get<{ run: { status: string } }>(`/v1/runs/${run}`) : Promise.resolve(undefined)), [run], !!run);
  const st = status.data?.run.status;
  const finished = st !== undefined && terminal.includes(st);
  useEffect(() => {
    if (st === "completed") onDone();
  }, [st, onDone]);
  return (
    <>
      <Field label="Test input" hint="What the trigger would send. Change it if you like.">
        <JsonInput value={input} onChange={setInput} rows={6} />
      </Field>
      <ErrorBox error={act.error ?? version.error} />
      <div className="toolbar">
        <button
          className="primary"
          disabled={act.busy || (!!run && !finished)}
          onClick={() => void act.run(async () => setRun((await post<{ run_id: string }>(`/v1/workflows/${workflow}/runs`, { input: input ?? {}, environment: ENV })).run_id))}
        >
          Run it now
        </button>
        {run && (
          <span data-testid="guide-run-status">
            <Badge value={st ?? "queued"} /> <Link to={`/runs/${run}`}>See every step</Link>
          </span>
        )}
      </div>
      {st === "completed" && (
        <p role="status" className="notice">
          It worked. Your workflow is live: it runs on its own from now on.
        </p>
      )}
      {finished && st !== "completed" && <p className="error">The run did not complete. Open it to see which step stopped and why.</p>}
    </>
  );
}
