import { useState } from "react";
import { del, get, post, put } from "../api";
import { useEnvironments } from "../environments";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";

interface Channel {
  id: string;
  kind: "email" | "slack" | "webhook";
  name: string;
  config: { to?: string[]; url?: string };
  created_at: string;
}
interface Rule {
  id: string;
  name: string;
  kind: string;
  config: { workflow_id?: string; environment?: string; threshold?: string };
  channel_ids: string[];
  enabled: boolean;
}
interface AlertInfo {
  id: string;
  kind: string;
  rule: string | null;
  title: string;
  body: string;
  link: string | null;
  created_at: string;
  deliveries: { channel: string; kind: string; status: string; attempts: number; last_error: string | null; sent_at: string | null }[];
}

const KIND_LABELS: Record<string, string> = {
  run_failed: "A run fails",
  slow_run: "A run takes longer than",
  stuck_approval: "An approval waits longer than",
  needs_reconciliation: "A step needs reconciliation",
  connector_drift: "A provider changes its responses",
  credential_expiry: "A credential or API key expires within",
  audit_anchor: "The audit log is anchored (email the signed anchor)",
  limit: "A plan limit is reached (run quota, backlog, ingest rate)",
};
const THRESHOLDS: Record<string, string> = { slow_run: "1h", stuck_approval: "24h", credential_expiry: "168h" };

export function Alerts() {
  const channels = useLoad(() => get<{ channels: Channel[]; email_configured: boolean }>("/v1/alerts/channels"), []);
  const rules = useLoad(() => get<{ rules: Rule[]; kinds: string[] }>("/v1/alerts/rules"), []);
  const recent = useLoad(() => get<{ alerts: AlertInfo[] }>("/v1/alerts"), [], true);
  const act = useAction();
  const [notice, setNotice] = useState("");
  const chans = channels.data?.channels ?? [];
  const name = (id: string) => chans.find((c) => c.id === id)?.name ?? "deleted";
  return (
    <>
      <h1>Alerts</h1>
      <ErrorBox error={channels.error ?? rules.error ?? recent.error ?? act.error} />
      {notice && (
        <div className="notice" role="status">
          {notice}
        </div>
      )}
      <section className="card">
        <h2 style={{ marginTop: 0 }}>Channels</h2>
        {channels.data && !channels.data.email_configured && <p className="hint">Email is not configured on this deployment (TASKIEM_SMTP_URL); email channels will show their deliveries as failing.</p>}
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Kind</th>
              <th>Destination</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {chans.map((c) => (
              <tr key={c.id}>
                <td>{c.name}</td>
                <td>{c.kind}</td>
                <td className="hint">{c.kind === "email" ? c.config.to?.join(", ") : c.kind === "webhook" ? c.config.url : "Slack incoming webhook"}</td>
                <td>
                  <button onClick={() => void act.run(async () => (await post(`/v1/alerts/channels/${c.id}/test`), setNotice(`A test message reached ${c.name}.`)))}>Send test</button>{" "}
                  <button className="danger" onClick={() => void act.run(async () => (await del(`/v1/alerts/channels/${c.id}`), channels.reload(), rules.reload()))}>
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <AddChannel onAdded={(key) => (channels.reload(), key && setNotice(`Signing key (shown once; verify the Taskiem-Signature header with it): ${key}`))} />
      </section>
      <section className="card">
        <h2 style={{ marginTop: 0 }}>Rules</h2>
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>When</th>
              <th>Send to</th>
              <th>On</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {(rules.data?.rules ?? []).map((r) => (
              <tr key={r.id}>
                <td>{r.name}</td>
                <td>
                  {KIND_LABELS[r.kind] ?? r.kind}
                  {r.config.threshold && ` ${r.config.threshold}`}
                  {r.config.environment && <span className="hint"> · {r.config.environment}</span>}
                </td>
                <td>{r.channel_ids.map(name).join(", ")}</td>
                <td>
                  <input
                    type="checkbox"
                    aria-label={`Rule ${r.name} on`}
                    checked={r.enabled}
                    onChange={(e) => void act.run(async () => (await put(`/v1/alerts/rules/${r.id}`, { name: r.name, kind: r.kind, config: r.config, channel_ids: r.channel_ids, enabled: e.target.checked }), rules.reload()))}
                  />
                </td>
                <td>
                  <button className="danger" onClick={() => void act.run(async () => (await del(`/v1/alerts/rules/${r.id}`), rules.reload()))}>
                    Delete
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {chans.length > 0 ? <AddRule channels={chans} onAdded={rules.reload} /> : <p className="hint">Add a channel first.</p>}
      </section>
      <section className="card">
        <h2 style={{ marginTop: 0 }}>Recent alerts</h2>
        {(recent.data?.alerts ?? []).length === 0 && <p className="hint">Nothing yet.</p>}
        {(recent.data?.alerts ?? []).map((a) => (
          <div key={a.id} className="alert-row">
            <div>
              <strong>{a.title}</strong> <span className="hint">{fmtTime(a.created_at)}</span> {a.link && <a href={a.link}>open</a>}
            </div>
            <div className="hint">
              {a.deliveries.map((d) => (
                <span key={d.channel} title={d.last_error ?? ""}>
                  {d.channel}: {d.status}
                  {d.status !== "sent" && d.last_error && ` (${d.last_error})`}{" "}
                </span>
              ))}
            </div>
          </div>
        ))}
      </section>
    </>
  );
}

function AddChannel({ onAdded }: { onAdded: (signingKey?: string) => void }) {
  const [kind, setKind] = useState<"email" | "slack" | "webhook">("email");
  const [name, setName] = useState("");
  const [dest, setDest] = useState("");
  const act = useAction();
  return (
    <form
      className="row"
      style={{ alignItems: "flex-end" }}
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          const body = kind === "email" ? { kind, name, to: dest.split(/[,\s]+/).filter(Boolean) } : { kind, name, url: dest };
          const r = await post<{ signing_key?: string }>("/v1/alerts/channels", body);
          setName("");
          setDest("");
          onAdded(r.signing_key);
        });
      }}
    >
      <Field label="Kind">
        <select value={kind} onChange={(e) => setKind(e.target.value as typeof kind)}>
          <option value="email">Email</option>
          <option value="slack">Slack</option>
          <option value="webhook">Webhook</option>
        </select>
      </Field>
      <Field label="Channel name">
        <input value={name} onChange={(e) => setName(e.target.value)} required />
      </Field>
      <Field label={kind === "email" ? "Recipients" : kind === "slack" ? "Incoming webhook URL" : "HTTPS URL"}>
        <input value={dest} onChange={(e) => setDest(e.target.value)} placeholder={kind === "email" ? "ops@bank.com, oncall@bank.com" : kind === "slack" ? "https://hooks.slack.com/services/…" : "https://"} required />
      </Field>
      <button type="submit" disabled={act.busy}>
        Add channel
      </button>
      <ErrorBox error={act.error} />
    </form>
  );
}

function AddRule({ channels, onAdded }: { channels: Channel[]; onAdded: () => void }) {
  const envs = useEnvironments();
  const [name, setName] = useState("");
  const [kind, setKind] = useState("run_failed");
  const [env, setEnv] = useState("");
  const [threshold, setThreshold] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const act = useAction();
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          await post("/v1/alerts/rules", { name, kind, config: { environment: env || undefined, threshold: threshold || undefined }, channel_ids: picked });
          setName("");
          setPicked([]);
          onAdded();
        });
      }}
    >
      <div className="row" style={{ alignItems: "flex-end" }}>
        <Field label="Rule name">
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="When">
          <select value={kind} onChange={(e) => (setKind(e.target.value), setThreshold(""))}>
            {Object.entries(KIND_LABELS).map(([k, l]) => (
              <option key={k} value={k}>
                {l}
              </option>
            ))}
          </select>
        </Field>
        {THRESHOLDS[kind] && (
          <Field label="Threshold">
            <input value={threshold} onChange={(e) => setThreshold(e.target.value)} placeholder={THRESHOLDS[kind]} />
          </Field>
        )}
        {["run_failed", "slow_run", "stuck_approval", "needs_reconciliation"].includes(kind) && (
          <Field label="Environment">
            <select value={env} onChange={(e) => setEnv(e.target.value)}>
              <option value="">any</option>
              {(envs.data?.environments ?? []).map((x) => (
                <option key={x.name} value={x.name}>
                  {x.name}
                </option>
              ))}
            </select>
          </Field>
        )}
      </div>
      <div className="row">
        {channels.map((c) => (
          <label key={c.id} className="inline">
            <input type="checkbox" checked={picked.includes(c.id)} onChange={(e) => setPicked(e.target.checked ? [...picked, c.id] : picked.filter((x) => x !== c.id))} /> {c.name}
          </label>
        ))}
        <button type="submit" disabled={act.busy || picked.length === 0}>
          Add rule
        </button>
      </div>
      <ErrorBox error={act.error} />
    </form>
  );
}
