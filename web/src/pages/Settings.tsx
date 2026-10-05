import { useState } from "react";
import { del, get, post, put } from "../api";
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
