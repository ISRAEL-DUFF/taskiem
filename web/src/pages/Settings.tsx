import { plainValue, showValue } from "../lib/values";
import { useState } from "react";
import { del, get, post, put, type GitConnection, type GitSync } from "../api";
import { useAdvanced, useAuth } from "../auth";
import { EnvSelect, useEnvironments } from "../environments";
import { ErrorBox, Field, Items, JsonInput, PageHeader, Row, fmtTime, useAction, useLoad } from "../ui";
import { WhatsAppNumber } from "./WhatsAppNumber";
import { PeopleYouMessage } from "./PeopleYouMessage";
import { USSDChannels } from "./USSDChannels";
import { Help } from "../onboarding";
import { SchoolsSwitch } from "./Schools";

interface Secret {
  environment: string;
  name: string;
  created_by: string;
  updated_at: string;
  last_used_at: string | null;
}
interface Variable {
  environment: string;
  name: string;
  value: unknown;
  updated_at: string;
}

/** The Advanced switch: owners and admins show the technical parts. */
function AdvancedSwitch() {
  const { me, refresh } = useAuth();
  const act = useAction();
  return (
    <div className="card">
      <h2 style={{ marginTop: 0 }}>Advanced</h2>
      <label>
        <input
          type="checkbox"
          checked={!!me?.advanced}
          disabled={act.busy}
          onChange={(e) => void act.run(async () => (await put("/v1/settings/advanced", { advanced: e.target.checked }), await refresh()), "Saved")}
        />{" "}
        Show the technical parts
      </label>
      <p className="hint">For someone who builds workflows by hand: a blank canvas, formulas, the test environment and your own connectors. Everyone in the business sees the same.</p>
      <ErrorBox error={act.error} />
    </div>
  );
}

interface LanguagesView {
  languages: string[];
  default_language: string;
  voice_notes: boolean;
  voice_in_workflows: boolean;
  speech_hear: boolean;
  speech_speak: boolean;
  voices: string[];
  conversation_days: number;
  max_conversation_days: number;
}

/** Voice in workflows: the owner's switch for workflows that hear customers' voice notes and answer by voice (speech, decision 0062 T7). */
function VoiceSwitch() {
  const { data, error, reload } = useLoad(() => get<LanguagesView>("/v1/languages"), []);
  const act = useAction();
  const [days, setDays] = useState<string>();
  if (!data) return error ? <ErrorBox error={error} /> : null;
  const can = data.speech_hear || data.speech_speak;
  const save = (patch: { voice_in_workflows?: boolean; conversation_days?: number }) =>
    act.run(
      async () => (
        await put("/v1/languages", { languages: data.languages, default_language: data.default_language, voice_notes: data.voice_notes, ...patch }),
        reload()
      ),
      "Saved",
    );
  const what = data.speech_hear && data.speech_speak
    ? "hear voice notes and answer by voice"
    : data.speech_hear
      ? "hear voice notes (answering by voice is not set up yet)"
      : "answer by voice (hearing voice notes is not set up yet)";
  return (
    <section className="card" id="voice">
      <h2 style={{ marginTop: 0 }}>Voice in workflows</h2>
      <label>
        <input
          type="checkbox"
          checked={data.voice_in_workflows}
          disabled={act.busy || (!can && !data.voice_in_workflows)}
          data-testid="voice-in-workflows"
          onChange={(e) => void save({ voice_in_workflows: e.target.checked })}
        />{" "}
        Let workflows {can ? what : "hear voice notes and answer by voice"}
      </label>
      <p className="hint">
        {can
          ? "Your customers' voice notes are turned into text and your replies spoken back. Each second heard or spoken counts against your plan's monthly voice allowance."
          : "Not available yet: whoever runs your Taskiem has not set up a speech service."}
      </p>
      <Field label="Keep conversations with customers for (days)" hint={`What customers said and what your assistant answered, with its voice notes, are deleted after this many days (at most ${data.max_conversation_days} on your plan).`}>
        <form
          className="toolbar"
          onSubmit={(e) => {
            e.preventDefault();
            const n = Number(days);
            if (n >= 1) void save({ conversation_days: n }).then((ok) => ok && setDays(undefined));
          }}
        >
          <input
            type="number"
            min={1}
            max={data.max_conversation_days}
            value={days ?? String(data.conversation_days)}
            data-testid="conversation-days"
            onChange={(e) => setDays(e.target.value)}
            style={{ maxWidth: 120 }}
          />
          <button type="submit" disabled={act.busy || days === undefined}>
            Save
          </button>
        </form>
      </Field>
      <ErrorBox error={act.error} />
    </section>
  );
}

interface AssistantView {
  paused: boolean;
  paused_at: string | null;
  taskiem: { on: boolean; mode: string; beta: boolean; stopped: boolean };
  handovers: { channel: string; customer: string; where: string; hint: string; at: string; until: string }[];
}

/** Your assistant (decision 0062 V6): the owner's pause for every customer, and the customers waiting for a person. */
function AssistantCard() {
  const { data, error, reload } = useLoad(() => get<AssistantView>("/v1/assistant"), []);
  const act = useAction();
  if (!data) return error ? <ErrorBox error={error} /> : null;
  const run = (fn: () => Promise<unknown>, done: string) => void act.run(async () => (await fn(), reload()), done);
  return (
    <section className="card" id="assistant">
      <h2 style={{ marginTop: 0 }}>Your assistant</h2>
      {!data.taskiem.on && (
        <div className="notice" role="note">
          <p>
            {data.taskiem.stopped
              ? "Taskiem has turned your assistant off. Contact Taskiem to find out why."
              : "The assistant is not open to every business yet. Ask Taskiem to join."}
          </p>
        </div>
      )}
      <label>
        <input
          type="checkbox"
          checked={data.paused}
          disabled={act.busy}
          data-testid="assistant-paused"
          onChange={(e) => run(() => post("/v1/assistant/pause", { paused: e.target.checked }), e.target.checked ? "Paused" : "Answering again")}
        />{" "}
        Pause your assistant for every customer
      </label>
      <p className="hint">
        {data.paused && data.paused_at
          ? `Paused since ${fmtTime(data.paused_at)}. Customers' messages are kept, and nobody gets an automatic answer: answer them yourself.`
          : "Paused, it answers nobody: customers' messages are kept for you to answer yourself."}
      </p>
      <h3>Customers waiting for a person</h3>
      {data.handovers.length === 0 ? (
        <p className="hint">None. When a customer asks for a person, you are told, and your assistant stays quiet for them while you answer.</p>
      ) : (
        <Items label="Customers waiting for a person">
          {data.handovers.map((h) => (
            <Row
              key={h.channel + h.customer}
              icon="chat"
              tone="warn"
              title={h.hint}
              sub={
                <span className="sub">
                  {h.where} · asked {fmtTime(h.at)} · your assistant answers them again {fmtTime(h.until)}
                </span>
              }
              testid="assistant-handover"
            >
              <button disabled={act.busy} onClick={() => run(() => post("/v1/assistant/handovers/resume", { channel: h.channel, customer: h.customer }), "Answering them again")}>
                Let the assistant answer them now
              </button>
            </Row>
          ))}
        </Items>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

export function Settings() {
  const { can, me } = useAuth();
  const advanced = useAdvanced();
  const [env, setEnv] = useState("prod");
  return (
    <>
      <PageHeader
        title="Secrets &amp; settings"
        description="Your environments and the secrets and variables workflows read in each of them."
        actions={<EnvSelect value={env} onChange={setEnv} />}
      />
      {can("member.manage") && <AdvancedSwitch />}
      {me?.roles.includes("owner") && <SchoolsSwitch />}
      {can("secret.manage") && <VoiceSwitch />}
      {can("secret.manage") && <AssistantCard />}
      {advanced && <Environments editable={can("secret.manage")} />}
      {can("secret.manage") && <Secrets env={env} />}
      <Variables env={env} editable={can("secret.manage")} />
      {advanced && can("secret.manage") && <Egress env={env} />}
      {advanced && can("git.manage") && <Git env={env} />}
      {can("workflow.read") && <PeopleYouMessage />}
      {can("secret.manage") && <WhatsAppNumber />}
      {can("secret.manage") && <USSDChannels />}
      <Governance />
      <PlanLimits />
    </>
  );
}

interface LimitsView {
  limits: Record<string, number>;
  overrides: Record<string, number>;
  usage: {
    runs_today: number;
    runs_this_month: number;
    running_runs: number;
    queued_runs: number;
    workflows: number;
    secrets: number;
    connections: number;
    whatsapp_templates_this_month?: { sent: number; by_category: Record<string, number>; overage: number; blocked: number };
    container_seconds_this_month?: number;
    speech_seconds_this_month?: number;
    ai_tokens_this_month?: number;
    ai_step_tokens_this_month?: number;
  };
  recent_hits: { limit: string; day: string; hits: number }[];
  help: Record<string, string>;
}

/** The plan's limits and what the tenant uses of them (read-only: operators set them). */
function PlanLimits() {
  const { data, error } = useLoad(() => get<LimitsView>("/v1/limits"), []);
  if (!data) return error ? <ErrorBox error={error} /> : null;
  const used: Record<string, number> = {
    runs_per_day: data.usage.runs_today,
    runs_per_month: data.usage.runs_this_month,
    max_running_runs: data.usage.running_runs,
    max_queued_runs: data.usage.queued_runs,
    max_workflows: data.usage.workflows,
    max_secrets: data.usage.secrets,
    max_connections: data.usage.connections,
    whatsapp_templates_monthly: data.usage.whatsapp_templates_this_month?.sent ?? 0,
    container_minutes_monthly: Math.ceil((data.usage.container_seconds_this_month ?? 0) / 60),
    speech_seconds_monthly: data.usage.speech_seconds_this_month ?? 0,
    ai_monthly_tokens: data.usage.ai_tokens_this_month ?? 0,
    ai_step_monthly_tokens: data.usage.ai_step_tokens_this_month ?? 0,
  };
  const wt = data.usage.whatsapp_templates_this_month;
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Plan limits</h2>
      <p className="hint">Set by your operator; 0 means no limit, except where a row says 0 turns something off. Above the ingest rate deliveries are still accepted and their runs queued.</p>
      <table>
        <thead>
          <tr>
            <th>Limit</th>
            <th>Value</th>
            <th>In use</th>
            <th>What it does</th>
          </tr>
        </thead>
        <tbody>
          {Object.keys(data.help).sort().map((k) => (
            <tr key={k}>
              <td className="mono">{k}</td>
              <td>
                {data.limits[k]}
                {k in data.overrides ? "" : " (default)"}
              </td>
              <td>{used[k] ?? ""}</td>
              <td className="hint">{data.help[k]}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {wt && wt.sent + wt.blocked > 0 && (
        <p className="hint" data-testid="whatsapp-templates">
          WhatsApp templates this month: {wt.sent} sent ({Object.entries(wt.by_category).map(([c, n]) => `${n} ${c}`).join(", ")}); {wt.overage} beyond the allowance, billed
          through; {wt.blocked} marketing held back.
        </p>
      )}
      {data.recent_hits.length > 0 && (
        <>
          <h2>Reached lately</h2>
          <ul>
            {data.recent_hits.map((h) => (
              <li key={h.limit + h.day}>
                {h.day}: <span className="mono">{h.limit}</span> ({h.hits}×)
              </li>
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

function Environments({ editable }: { editable: boolean }) {
  const envs = useEnvironments();
  const [name, setName] = useState("");
  const [gate, setGate] = useState("");
  const act = useAction();
  const list = envs.data?.environments ?? [];
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Environments</h2>
      <p className="hint">
        Each environment has its own secrets, connections, variables and triggers, and runs its own version of each workflow. Publishing deploys to every environment not gated on another; a gated environment runs only what was promoted from the one it names.
      </p>
      <ErrorBox error={envs.error ?? act.error} />
      <table>
        <thead>
          <tr>
            <th>Environment</th>
            <th>Takes versions</th>
            <th>Workflows</th>
          </tr>
        </thead>
        <tbody>
          {list.map((e) => (
            <tr key={e.name}>
              <td>{e.name}</td>
              <td>
                {editable && !e.git ? (
                  <select
                    aria-label={`Gate for ${e.name}`}
                    value={e.promotion_from ?? ""}
                    onChange={(ev) => void act.run(async () => (await put(`/v1/environments/${e.name}`, { promotion_from: ev.target.value }), envs.reload()), "Saved")}
                  >
                    <option value="">when published</option>
                    {list
                      .filter((o) => o.name !== e.name)
                      .map((o) => (
                        <option key={o.name} value={o.name}>
                          by promotion from {o.name}
                        </option>
                      ))}
                  </select>
                ) : e.git ? (
                  "from Git"
                ) : e.promotion_from ? (
                  `by promotion from ${e.promotion_from}`
                ) : (
                  "when published"
                )}
              </td>
              <td>{e.workflows}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {editable && (
        <form
          className="row"
          style={{ alignItems: "flex-end" }}
          onSubmit={(ev) => {
            ev.preventDefault();
            void act.run(async () => {
              await post("/v1/environments", { name, promotion_from: gate });
              setName("");
              setGate("");
              envs.reload();
            }, "Environment added");
          }}
        >
          <Field label="New environment">
            <input value={name} onChange={(e) => setName(e.target.value)} placeholder="staging" required />
          </Field>
          <Field label="Takes versions">
            <select value={gate} onChange={(e) => setGate(e.target.value)}>
              <option value="">when published</option>
              {list.map((o) => (
                <option key={o.name} value={o.name}>
                  by promotion from {o.name}
                </option>
              ))}
            </select>
          </Field>
          <button type="submit" disabled={act.busy}>
            Add
          </button>
        </form>
      )}
    </section>
  );
}

function Secrets({ env }: { env: string }) {
  const { data, error, reload } = useLoad(() => get<{ secrets: Secret[] }>("/v1/secrets"), []);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const act = useAction();
  const rows = data?.secrets.filter((s) => s.environment === env) ?? [];
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Secrets</h2>
      <p className="hint">Values are write-only. Steps read them as secrets.&lt;name&gt;; webhook triggers use webhook_&lt;workflow id&gt;.</p>
      <ErrorBox error={error ?? act.error} />
      {rows.length > 0 && (
        <table style={{ marginBottom: 10 }}>
          <tbody>
            {rows.map((s) => (
              <tr key={s.name}>
                <td>
                  <code>{s.name}</code>
                </td>
                <td className="hint">
                  updated {fmtTime(s.updated_at)} by {s.created_by}
                </td>
                <td className="hint">{s.last_used_at ? `last used ${fmtTime(s.last_used_at)}` : "never used"}</td>
                <td style={{ textAlign: "right" }}>
                  <button className="danger" onClick={() => confirm(`Delete ${s.name}?`) && void act.run(async () => (await del(`/v1/secrets/${env}/${s.name}`), reload()), "Secret deleted")}>
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <form
        className="row"
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            await put(`/v1/secrets/${env}/${name}`, { value });
            setName("");
            setValue("");
            reload();
          }, "Secret saved");
        }}
      >
        <input className="mono" placeholder="name" value={name} onChange={(e) => setName(e.target.value)} required />
        <input type="password" placeholder="value" autoComplete="off" value={value} onChange={(e) => setValue(e.target.value)} required />
        <button type="submit" style={{ flex: "0 0 auto" }} disabled={act.busy}>
          Set secret
        </button>
      </form>
    </section>
  );
}

function Variables({ env, editable }: { env: string; editable: boolean }) {
  const { data, error, reload } = useLoad(() => get<{ variables: Variable[] }>("/v1/variables"), []);
  // What each setting is for: the templates that read it say (N5).
  const tpls = useLoad(() => get<{ templates: { variables: { name: string; why: string }[] }[] }>("/v1/templates"), []);
  const why = new Map((tpls.data?.templates ?? []).flatMap((t) => t.variables.map((v) => [v.name, v.why] as const)));
  const [name, setName] = useState("");
  const [text, setText] = useState("");
  const [advanced, setAdvanced] = useState(false);
  const [value, setValue] = useState<unknown>("");
  const act = useAction();
  const rows = data?.variables.filter((v) => v.environment === env) ?? [];
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Settings your workflows read</h2>
      <p className="hint">Such as the owner's phone number or a business name. Each run keeps the values it started with.</p>
      <Help topic="variables" />
      <ErrorBox error={error ?? act.error} />
      {rows.length > 0 && (
        <table style={{ marginBottom: 10 }}>
          <tbody>
            {rows.map((v) => (
              <tr key={v.name}>
                <td>
                  <strong>{v.name.replace(/_/g, " ")}</strong>
                  {why.get(v.name) && <div className="hint">{why.get(v.name)}</div>}
                </td>
                <td>{showValue(v.value)}</td>
                <td className="hint">{fmtTime(v.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {editable && (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              await put(`/v1/variables/${env}/${name}`, { value: advanced ? value : plainValue(text) });
              setName("");
              setText("");
              setValue("");
              reload();
            }, "Saved");
          }}
        >
          <div className="row">
            <Field label="Name">
              <input className="mono" value={name} onChange={(e) => setName(e.target.value)} required />
            </Field>
            {advanced ? (
              <Field label="Value (JSON)">
                <JsonInput value={value} onChange={setValue} rows={1} />
              </Field>
            ) : (
              <Field label="Value" hint="Text or a number, as you would write it. A phone number in any form is kept as +234….">
                <input value={text} onChange={(e) => setText(e.target.value)} required />
              </Field>
            )}
          </div>
          <label className="inline hint" data-technical>
            <input type="checkbox" checked={advanced} onChange={(e) => setAdvanced(e.target.checked)} /> Advanced: enter JSON
          </label>
          <button type="submit" disabled={act.busy || !name}>
            Set variable
          </button>
        </form>
      )}
    </section>
  );
}

function Egress({ env }: { env: string }) {
  const { data, error, reload } = useLoad(() => get<{ hosts: string[] }>(`/v1/egress?environment=${env}`), [env]);
  const [host, setHost] = useState("");
  const act = useAction();
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Allowed hosts</h2>
      <p className="hint">HTTP steps and code may reach only these hosts (connectors use their own declared hosts). Private and metadata addresses are always refused.</p>
      <ErrorBox error={error ?? act.error} />
      <p>{data?.hosts.length ? data.hosts.map((h) => <code key={h} style={{ marginRight: 10 }}>{h}</code>) : <span className="hint">None yet.</span>}</p>
      <form
        className="row"
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await post("/v1/egress", { environment: env, host }), setHost(""), reload()), "Host allowed");
        }}
      >
        <input className="mono" placeholder="api.example.com or *.example.com" value={host} onChange={(e) => setHost(e.target.value)} required />
        <button type="submit" style={{ flex: "0 0 auto" }} disabled={act.busy}>
          Allow host
        </button>
      </form>
    </section>
  );
}

/**
 * The environment's Git repository (spec 10.3). Platform-led: publishing
 * here opens a pull request. Git-led: pushes to the branch deploy once every
 * workflow test passes, and the workflows the repository holds are
 * read-only here.
 */
function Git({ env }: { env: string }) {
  const { data, error, reload } = useLoad(() => get<{ connections: GitConnection[] }>("/v1/git"), []);
  const conn = data?.connections.find((c) => c.environment === env);
  const syncs = useLoad(() => (conn?.mode === "git_led" ? get<{ syncs: GitSync[] }>(`/v1/git/${env}/syncs`) : Promise.resolve({ syncs: [] })), [env, conn?.mode]);
  const [form, setForm] = useState<Record<string, string>>({});
  const [issued, setIssued] = useState<{ secret: string; url: string }>();
  const act = useAction();
  // The token and Bitbucket username are credentials: never shown again.
  const v = (k: keyof GitConnection | "token" | "username", d = "") =>
    form[k] ?? (conn && k !== "token" && k !== "username" ? String(conn[k] ?? "") : d);
  const set = (k: string) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const bitbucket = v("provider", "github") === "bitbucket";
  const save = () =>
    act.run(async () => {
      const body: Record<string, unknown> = {
        provider: v("provider", "github"), api_url: v("api_url"), repo: v("repo"), branch: v("branch", "main"),
        path: v("path", "flows"), tests_path: v("tests_path", "tests"), mode: v("mode", "platform_led"),
      };
      if (form.token) body.auth = { type: "token", token: form.token, ...(bitbucket && form.username ? { username: form.username } : {}) };
      const r = await put<{ webhook_secret?: string; webhook_url: string }>(`/v1/git/${env}`, body);
      if (r.webhook_secret) setIssued({ secret: r.webhook_secret, url: location.origin + r.webhook_url });
      setForm({});
      reload();
    }, "Connected");
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Git</h2>
      <p className="hint">
        Platform-led: publishing here opens a pull request with the workflow's definition and code. Git-led: pushes to the branch deploy, after every
        workflow test in the repository passes, and those workflows are read-only here.
      </p>
      <ErrorBox error={error ?? act.error ?? syncs.error} />
      {issued && (
        <div className="notice">
          Add this webhook to the repository (push events). The secret is shown once.
          <br />
          URL <code>{issued.url}</code>
          <br />
          Secret <code>{issued.secret}</code>
        </div>
      )}
      <div className="row">
        <Field label="Host">
          <select value={v("provider", "github")} onChange={set("provider")}>
            <option value="github">GitHub</option>
            <option value="gitlab">GitLab</option>
            <option value="bitbucket">Bitbucket Cloud</option>
          </select>
        </Field>
        <Field label="API URL" hint="Empty for github.com, gitlab.com or bitbucket.org">
          <input value={v("api_url")} onChange={set("api_url")} placeholder="https://github.example.com/api/v3" />
        </Field>
        <Field label="Mode">
          <select value={v("mode", "platform_led")} onChange={set("mode")}>
            <option value="platform_led">Platform-led</option>
            <option value="git_led">Git-led</option>
          </select>
        </Field>
      </div>
      <div className="row">
        <Field label="Repository">
          <input value={v("repo")} onChange={set("repo")} placeholder={bitbucket ? "workspace/repo_slug" : "owner/name"} />
        </Field>
        <Field label="Branch">
          <input value={v("branch", "main")} onChange={set("branch")} />
        </Field>
        <Field label="Workflows directory">
          <input value={v("path", "flows")} onChange={set("path")} />
        </Field>
        <Field label="Tests directory">
          <input value={v("tests_path", "tests")} onChange={set("tests_path")} />
        </Field>
      </div>
      <Field label={conn ? "Access token (leave empty to keep the current one)" : "Access token"} hint="Needs read access to contents, and write access to contents and pull requests for platform-led mode">
        <input type="password" autoComplete="off" value={form.token ?? ""} onChange={set("token")} />
      </Field>
      {bitbucket && (
        <Field label="Atlassian account email" hint="Only for an API token; leave empty for a repository, project or workspace access token">
          <input type="email" autoComplete="off" value={form.username ?? ""} onChange={set("username")} />
        </Field>
      )}
      <div className="row">
        <button className="primary" disabled={act.busy} onClick={() => void save()}>
          {conn ? "Save" : "Connect"}
        </button>
        {conn && (
          <button className="danger" disabled={act.busy} onClick={() => confirm(`Disconnect ${conn.repo}?`) && void act.run(async () => (await del(`/v1/git/${env}`), reload()), "Disconnected")}>
            Disconnect
          </button>
        )}
        {conn?.mode === "git_led" && (
          <button disabled={act.busy} onClick={() => void act.run(async () => (await post(`/v1/git/${env}/sync`), syncs.reload()), "Sync started")}>
            Deploy from {conn.branch} now
          </button>
        )}
      </div>
      {conn?.mode === "git_led" && (syncs.data?.syncs.length ?? 0) > 0 && (
        <table style={{ marginTop: 10 }}>
          <thead>
            <tr>
              <th>Requested</th>
              <th>Commit</th>
              <th>Status</th>
              <th>Result</th>
            </tr>
          </thead>
          <tbody>
            {syncs.data?.syncs.map((s) => (
              <tr key={s.id}>
                <td>{fmtTime(s.requested_at)}</td>
                <td>
                  <code>{s.commit.slice(0, 10) || "head"}</code>
                </td>
                <td>{s.status}</td>
                <td className="hint">
                  {s.report?.error ??
                    [
                      s.report?.tests && `${s.report.tests.passed} tests passed, ${s.report.tests.failed} failed`,
                      ...(s.report?.problems ?? []),
                      ...(s.report?.tests?.failures ?? []),
                      ...(s.report?.workflows ?? []).filter((w) => w.action !== "unchanged").map((w) => `${w.key} ${w.action} v${w.version}`),
                    ]
                      .filter(Boolean)
                      .join("; ")}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

/** Four-eyes on change (spec 9.1); owners change it, everyone can see it. */
function Governance() {
  const { me } = useAuth();
  const { data, error, reload } = useLoad(
    () => get<{ four_eyes_publish: boolean; four_eyes_publish_environments?: string[]; four_eyes_policies: boolean; default_retention: string; ai_steps?: boolean }>("/v1/governance"),
    [],
  );
  const envs = useEnvironments();
  const ai = useLoad(() => get<{ enabled: boolean; provider?: string; model?: string; step_models?: string[]; retention?: string; steps_plan: boolean }>("/v1/ai/status"), []);
  const [retention, setRetention] = useState<string>();
  const act = useAction();
  const owner = me?.roles.includes("owner") ?? false;
  if (!data) return error ? <ErrorBox error={error} /> : null;
  const set = (k: "four_eyes_publish" | "four_eyes_publish_environments" | "four_eyes_policies" | "default_retention" | "ai_steps", v: boolean | string | string[]) =>
    act.run(async () => (await put("/v1/governance", { ...data, [k]: v }), reload()), "Saved");
  const gated = data.four_eyes_publish_environments ?? [];
  const toggleEnv = (env: string, on: boolean) => void set("four_eyes_publish_environments", on ? [...gated, env] : gated.filter((e) => e !== env));
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Four-eyes</h2>
      <p className="hint">Changes that need a second person. Only owners change these, and every change is audited.</p>
      <ErrorBox error={act.error} />
      <label>
        <input type="checkbox" checked={data.four_eyes_publish} disabled={!owner || act.busy} onChange={(e) => void set("four_eyes_publish", e.target.checked)} /> Publishing a
        workflow needs a second publisher, in every environment
      </label>
      {!data.four_eyes_publish && (envs.data?.environments.length ?? 0) > 0 && (
        <fieldset style={{ border: 0, padding: "4px 0 4px 24px", margin: 0 }} data-testid="four-eyes-environments">
          <legend className="hint" style={{ padding: 0 }}>
            Or only in these environments:
          </legend>
          {envs.data?.environments.map((e) => (
            <label key={e.name} style={{ marginRight: 16 }}>
              <input type="checkbox" checked={gated.includes(e.name)} disabled={!owner || act.busy} onChange={(ev) => toggleEnv(e.name, ev.target.checked)} /> {e.name}
            </label>
          ))}
          <p className="hint" style={{ margin: "4px 0 0" }}>
            A publish that reaches a ticked environment waits for a second person, and so does promoting into one. Let production take versions only by promotion from staging, and publishing to
            the others is never held up.
          </p>
        </fieldset>
      )}
      <br />
      <label>
        <input type="checkbox" checked={data.four_eyes_policies} disabled={!owner || act.busy} onChange={(e) => void set("four_eyes_policies", e.target.checked)} /> A new
        approval policy version needs a second person
      </label>
      <h2>AI steps</h2>
      <p className="hint">
        Workflows can ask the AI model at run time: to sort a message, pull details out of text, or draft a reply. Personal data is hidden from the model behind placeholders unless a step lets a field
        through, and every such step is recorded in the audit log when it is published.
      </p>
      <label>
        <input type="checkbox" checked={data.ai_steps !== false} disabled={!owner || act.busy} onChange={(e) => void set("ai_steps", e.target.checked)} /> Let workflows ask the AI model
      </label>
      {ai.data && (
        <p className="hint" data-testid="ai-provider">
          {ai.data.enabled
            ? `Model: ${ai.data.model} (${ai.data.provider})${(ai.data.step_models?.length ?? 0) > 1 ? `; steps may also use ${ai.data.step_models?.slice(1).join(", ")}` : ""}. ${
                ai.data.retention ? `What the provider keeps: ${ai.data.retention}.` : "This deployment has not stated what the provider keeps; ask its operator."
              }`
            : "This deployment has no AI model, so AI steps cannot run."}
          {ai.data.enabled && !ai.data.steps_plan && " Your plan has no allowance for AI steps."}
        </p>
      )}
      <h2>Retention</h2>
      <p className="hint">How long run data is kept after a run ends, when its workflow sets no retention (default 90d). The audit log is kept regardless.</p>
      <div className="row">
        <input value={retention ?? data.default_retention} placeholder="90d" disabled={!owner} onChange={(e) => setRetention(e.target.value)} style={{ maxWidth: 160 }} />
        {owner && (
          <button disabled={act.busy || retention === undefined} onClick={() => void set("default_retention", retention ?? "").then(() => setRetention(undefined))}>
            Save retention
          </button>
        )}
      </div>
    </section>
  );
}
