import { useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { ApiError, get, post, put, type CapabilityInfo, type ConnectorInfo, type JSONSchema, type Problem } from "../api";
import { useAuth } from "../auth";
import { Help, useOnboarding } from "../onboarding";
import { CHECKLIST, firstThings, formatDuration, sampleInput, starterTemplates } from "../lib/onboarding";
import { initialValues, toParams, type Template } from "../lib/templates";
import { Badge, ErrorBox, Field, JsonInput, Skeleton, useAction, useLoad } from "../ui";
import { ParamField } from "./Templates";
import { NeedsList } from "./Readiness";
import { ProblemLine } from "../forms/ProblemLine";
import { plainValue, resolveSchema } from "../lib/values";
import { SchemaForm } from "../forms/SchemaForm";
import { NewConnection, startLink } from "./Connections";
import { needsConnection } from "../lib/connections";
import { railsForAll, templateCapabilities } from "../lib/capabilities";
import type { ConnectionName } from "../forms/ConnectionPicker";
import type { ReadyNeed, Readiness } from "../lib/readiness";

// Get started (docs/onboarding.md): the checklist, derived from what the
// organisation has done, and the guided first workflow: pick a template,
// connect its app, create and publish it, run it once.

export function Start() {
  const { me, can } = useAuth();
  const { data, reload } = useOnboarding();
  const act = useAction();
  const [sent, setSent] = useState<string | null>(null);
  const nav = useNavigate();
  if (!data) return <Skeleton />;
  const more = firstThings(can);
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
          <button onClick={() => void act.run(async () => (await post("/v1/onboarding/dismiss"), reload(), nav("/home")), false)}>Hide this</button>
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
                    <button disabled={act.busy} onClick={() => void act.run(async () => setSent((await post<{ status?: string }>("/v1/me/email/verify/resend")).status ?? "Your email is confirmed."), false)}>
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
      {more.length > 0 && (
        <section aria-labelledby="get-paid">
          <h2 id="get-paid">Get paid and see your money</h2>
          <ul className="checklist">
            {more.map((f) => (
              <li key={f.id} data-testid={`first-${f.id}`}>
                <div className="grow">
                  <strong>{f.title}</strong>
                  <div className="hint">{f.text}</div>
                </div>
                <Link to={f.href}>{f.action}</Link>
              </li>
            ))}
          </ul>
        </section>
      )}
      {(me?.schools || me?.roles.includes("owner")) && can("school.read") && <SchoolStart on={!!me?.schools} owner={!!me?.roles.includes("owner")} />}
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

/** The schools entry: for a business with the Schools area on, its pages and the school templates; for an owner without it, the offer to turn it on. */
function SchoolStart({ on, owner }: { on: boolean; owner: boolean }) {
  const { data, error } = useLoad(() => (on ? get<{ templates: Template[] }>("/v1/templates") : Promise.resolve(undefined)), [on]);
  const list = useMemo(() => (data?.templates ?? []).filter((t) => t.available && t.tags.includes("schools")).sort((a, b) => a.title.localeCompare(b.title)), [data]);
  if (!on) {
    if (!owner) return null;
    return (
      <section aria-labelledby="school-start" data-testid="school-start-off">
        <h2 id="school-start">Run a school?</h2>
        <p className="hint">
          Turn Schools on for students, families, term fees, each child's own account number, receipts, chasing, clearance and the daily close.{" "}
          <Link to="/settings/schools">Turn Schools on</Link>
        </p>
      </section>
    );
  }
  return (
    <section aria-labelledby="school-start" data-testid="school-start">
      <h2 id="school-start">Your school</h2>
      <p className="hint">
        <Link to="/schools/overview">Overview</Link>: the term's fees as they stand. <Link to="/schools">Students</Link>, <Link to="/schools/classes">classes and terms</Link>,{" "}
        <Link to="/schools/accounts">account numbers</Link>, <Link to="/schools/debtors">debtors</Link> and <Link to="/schools/clearance">clearance</Link>.
      </p>
      <ErrorBox error={error} />
      {list.length > 0 && (
        <>
          <p>Ready workflows for a school. Each walks you through publishing it and running it once.</p>
          <div className="template-grid">
            {list.map((t) => (
              <Link key={t.id} className="card template-card" to={`/start/guide?template=${t.id}`} data-testid={`school-template-${t.id}`}>
                <strong>{t.title}</strong>
                <span>{t.summary}</span>
              </Link>
            ))}
          </div>
        </>
      )}
    </section>
  );
}

/** The starter templates as cards; a card opens the guide. */
function TemplatePicker() {
  const { data, error } = useLoad(() => get<{ templates: Template[] }>("/v1/templates"), []);
  const names = useLoad(() => get<{ connections: ConnectionName[] }>("/v1/connection-names"), []);
  const list = useMemo(() => starterTemplates(data?.templates ?? [], (names.data?.connections ?? []).map((c) => c.connector)), [data, names.data]);
  const [all, setAll] = useState(false);
  return (
    <>
      <ErrorBox error={error} />
      <div className="template-grid">
        {(all ? list : list.slice(0, 6)).map((t) => (
          <Link key={t.id} className="card template-card" to={`/start/guide?template=${t.id}`}>
            <strong>{t.title}</strong>
            <span>{t.summary}</span>
            <span className="hint">
              {t.connectors.map((c) => c.split("@")[0]).join(" · ")}
              {t.needs_developer && " · needs a developer"}
            </span>
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
  // parameter for it, else "main". A connector without auth (notify)
  // needs none.
  // A template that runs on any rail (capability:pay_bank) needs one
  // connection whose payments account can do all it does.
  const caps = useLoad(() => (templateCapabilities(t.connectors).length > 0 ? get<{ capabilities: CapabilityInfo[] }>("/v1/capabilities") : Promise.resolve({ capabilities: [] })), [t.id]);
  const railNeed = (() => {
    const uses = templateCapabilities(t.connectors);
    if (uses.length === 0) return undefined;
    const rails = railsForAll(caps.data?.capabilities ?? [], uses);
    const p = t.params.find((x) => x.type === "connection" && (x.capability?.length ?? 0) > 0);
    const name = (p ? values[p.name] : "") || "";
    const capable = (conns.data?.connections ?? []).filter((c) => c.environment === ENV && rails.includes(c.connector) && (name === "" || c.name === name));
    return { rails, name, have: name === "" ? capable.length === 1 : capable.length > 0, using: capable[0] };
  })();
  const needs = t.connectors
    .filter((ref) => !ref.startsWith("capability:"))
    .map((ref) => {
      const cid = ref.split("@")[0];
      const p = t.params.find((x) => x.type === "connection" && x.connector === cid);
      const name = (p ? values[p.name] : "") || "main";
      const have = !!conns.data?.connections.some((c) => c.connector === cid && c.name === name && c.environment === ENV);
      return { ref, cid, name, have, info: connectors.data?.connectors.find((c) => c.ref === ref || c.id === cid) };
    })
    .filter((n) => !n.info || needsConnection(n.info));
  const missingVars = t.variables.filter((v) => !vars.data?.variables.some((x) => x.name === v.name && x.environment === ENV));
  const connected =
    conns.data !== undefined &&
    connectors.data !== undefined &&
    needs.every((n) => n.have) &&
    (railNeed === undefined || railNeed.have) &&
    vars.data !== undefined &&
    missingVars.length === 0;

  return (
    <>
      <h1>{t.title}</h1>
      <p>{t.description}</p>
      <ol className="guide">
        <li>
          <h2>Fill in the details</h2>
          {t.params.length === 0 && <p className="hint">Nothing to fill in.</p>}
          {t.params.map((p) => (
            <ParamField
              key={p.name}
              param={p}
              params={t.params}
              values={values}
              value={values[p.name] ?? ""}
              onChange={(v) => setValues((old) => ({ ...old, [p.name]: v }))}
              onOther={(name, v) => setValues((old) => ({ ...old, [name]: v }))}
            />
          ))}
        </li>
        <li>
          <h2>{needs.length > 0 || railNeed ? `Connect ${[...needs.map((n) => n.info?.name ?? n.cid), ...(railNeed ? ["a payments account"] : [])].join(" and ")}` : "Connect"}</h2>
          {connectors.data && needs.length === 0 && !railNeed && t.variables.length === 0 && <p className="hint">Nothing to connect.</p>}
          {needs.map((n) =>
            n.have ? (
              <p key={n.ref} data-testid={`connected-${n.cid}`}>
                <Badge value="ok" /> {n.info?.name ?? n.cid} is connected as <code>{n.name}</code>.
              </p>
            ) : (
              n.info && <ConnectForm key={n.ref} info={n.info} name={n.name} onDone={conns.reload} />
            ),
          )}
          {railNeed &&
            (railNeed.have && railNeed.using ? (
              <p data-testid="connected-rail">
                <Badge value="ok" /> Payments go through {connectors.data?.connectors.find((c) => c.id === railNeed.using?.connector)?.name ?? railNeed.using.connector}, connected as <code>{railNeed.using.name}</code>.
              </p>
            ) : (
              <div aria-label="Connect a payments account">
                <p className="hint">
                  {railNeed.name === "" && (conns.data?.connections ?? []).filter((c) => c.environment === ENV && railNeed.rails.includes(c.connector)).length > 1
                    ? "You have more than one payments account that can do this: choose one under Fill in the details."
                    : "Connect the payments account it should use. Any of these can:"}
                </p>
                {railNeed.rails.map((id) => {
                  const info = connectors.data?.connectors.find((c) => c.id === id);
                  return info && <ConnectForm key={id} info={info} name={railNeed.name || "main"} onDone={conns.reload} />;
                })}
              </div>
            ))}
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

/** Connects the service a template needs, on the guided screen, with its name fixed. */
function ConnectForm({ info, name, onDone }: { info: ConnectorInfo; name: string; onDone: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="card" aria-label={`Connect ${info.name}`}>
      <button type="button" className="primary" onClick={() => setOpen(true)}>
        Connect {info.name}
      </button>
      {open && (
        <NewConnection connectors={[info]} preset={{ connector: info.id, environment: ENV, name }} onClose={() => setOpen(false)} onDone={() => (setOpen(false), onDone())} onLink={startLink} />
      )}
    </div>
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
        void act.run(async () => (await put(`/v1/variables/${ENV}/${name}`, { value: plainValue(value) }), onDone()), "Saved");
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
  // Made, but not ready to publish: what it needs, fixed here.
  const [needs, setNeeds] = useState<{ id: string; needs: ReadyNeed[] }>();
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
              <li key={i}>
                <ProblemLine p={p} />
              </li>
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
      {needs && (
        <NeedsList
          needs={needs.needs}
          onChanged={() => void get<Readiness>(`/v1/workflows/${needs.id}/versions/1/readiness`).then((r) => setNeeds({ id: needs.id, needs: r.needs }))}
        />
      )}
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
            let p: { state: string };
            try {
              p = await post<{ state: string }>(`/v1/workflows/${id}/versions/1/publish`);
            } catch (e) {
              if (e instanceof ApiError && e.status === 422 && e.body.code === "not_ready") {
                setNeeds({ id, needs: (e.body.needs as ReadyNeed[] | undefined) ?? [] });
                onDone({ id, published: false, problems: [] });
                return;
              }
              throw e;
            }
            setNeeds(undefined);
            onDone({ id, published: p.state === "published", problems: [] });
          }, false)
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
  const [advanced, setAdvanced] = useState(false);
  const schema = resolveSchema(version.data?.definition.inputs?.schema, version.data?.definition.types);
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
      {schema?.properties && !advanced ? (
        <>
          <p className="hint">What the run starts with, filled in with an example. Change it if you like.</p>
          <SchemaForm schema={schema} value={input as Record<string, unknown> | undefined} onChange={setInput} expressions={false} />
        </>
      ) : (
        <Field label="Test input" hint="What the trigger would send. Change it if you like.">
          <JsonInput value={input} onChange={setInput} rows={6} />
        </Field>
      )}
      {schema?.properties && (
        <label className="inline hint" data-technical>
          <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} /> Advanced: enter JSON
        </label>
      )}
      <ErrorBox error={act.error ?? version.error} />
      <div className="toolbar">
        <button
          className="primary"
          disabled={act.busy || (!!run && !finished)}
          onClick={() => void act.run(async () => setRun((await post<{ run_id: string }>(`/v1/workflows/${workflow}/runs`, { input: input ?? {}, environment: ENV })).run_id), "Run started")}
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
