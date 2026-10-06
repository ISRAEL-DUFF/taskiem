import { useState } from "react";
import { del, get, post, put, type GitConnection, type GitSync } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, JsonInput, fmtTime, useAction, useLoad } from "../ui";

interface Secret {
  environment: string;
  name: string;
  created_by: string;
  updated_at: string;
}
interface Variable {
  environment: string;
  name: string;
  value: unknown;
  updated_at: string;
}

export function Settings() {
  const { can } = useAuth();
  const [env, setEnv] = useState("prod");
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Secrets &amp; settings
        </h1>
        <select aria-label="Environment" style={{ width: "auto" }} value={env} onChange={(e) => setEnv(e.target.value)}>
          <option value="prod">prod</option>
          <option value="dev">dev</option>
        </select>
      </div>
      {can("secret.manage") && <Secrets env={env} />}
      <Variables env={env} editable={can("secret.manage")} />
      {can("secret.manage") && <Egress env={env} />}
      {can("git.manage") && <Git env={env} />}
    </>
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
                <td style={{ textAlign: "right" }}>
                  <button className="danger" onClick={() => confirm(`Delete ${s.name}?`) && void act.run(async () => (await del(`/v1/secrets/${env}/${s.name}`), reload()))}>
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
          });
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
  const [name, setName] = useState("");
  const [value, setValue] = useState<unknown>("");
  const act = useAction();
  const rows = data?.variables.filter((v) => v.environment === env) ?? [];
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Variables</h2>
      <p className="hint">Expressions read these as env.&lt;name&gt;. Each run keeps the values it started with.</p>
      <ErrorBox error={error ?? act.error} />
      {rows.length > 0 && (
        <table style={{ marginBottom: 10 }}>
          <tbody>
            {rows.map((v) => (
              <tr key={v.name}>
                <td>
                  <code>{v.name}</code>
                </td>
                <td>
                  <code>{JSON.stringify(v.value)}</code>
                </td>
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
              await put(`/v1/variables/${env}/${name}`, { value });
              setName("");
              setValue("");
              reload();
            });
          }}
        >
          <div className="row">
            <Field label="Name">
              <input className="mono" value={name} onChange={(e) => setName(e.target.value)} required />
            </Field>
            <Field label="Value (JSON: &quot;text&quot;, 42, true, {...})">
              <JsonInput value={value} onChange={setValue} rows={1} />
            </Field>
          </div>
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
          void act.run(async () => (await post("/v1/egress", { environment: env, host }), setHost(""), reload()));
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
  const v = (k: keyof GitConnection | "token", d = "") => form[k] ?? (conn && k !== "token" ? String(conn[k as keyof GitConnection] ?? "") : d);
  const set = (k: string) => (e: { target: { value: string } }) => setForm({ ...form, [k]: e.target.value });
  const save = () =>
    act.run(async () => {
      const body: Record<string, unknown> = {
        provider: v("provider", "github"), api_url: v("api_url"), repo: v("repo"), branch: v("branch", "main"),
        path: v("path", "flows"), tests_path: v("tests_path", "tests"), mode: v("mode", "platform_led"),
      };
      if (form.token) body.auth = { type: "token", token: form.token };
      const r = await put<{ webhook_secret?: string; webhook_url: string }>(`/v1/git/${env}`, body);
      if (r.webhook_secret) setIssued({ secret: r.webhook_secret, url: location.origin + r.webhook_url });
      setForm({});
      reload();
    });
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
          </select>
        </Field>
        <Field label="API URL" hint="Empty for github.com or gitlab.com">
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
          <input value={v("repo")} onChange={set("repo")} placeholder="owner/name" />
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
      <div className="row">
        <button className="primary" disabled={act.busy} onClick={() => void save()}>
          {conn ? "Save" : "Connect"}
        </button>
        {conn && (
          <button className="danger" disabled={act.busy} onClick={() => confirm(`Disconnect ${conn.repo}?`) && void act.run(async () => (await del(`/v1/git/${env}`), reload()))}>
            Disconnect
          </button>
        )}
        {conn?.mode === "git_led" && (
          <button disabled={act.busy} onClick={() => void act.run(async () => (await post(`/v1/git/${env}/sync`), syncs.reload()))}>
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
