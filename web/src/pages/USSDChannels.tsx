import { useState } from "react";
import { del, get, put } from "../api";
import { useEnvironments } from "../environments";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";

interface Channel {
  provider: string;
  environment: string;
  allowed_cidrs: string[];
  status: "active" | "disabled";
  callback_path: string;
  created_at: string;
  token?: string;
  callback_url?: string;
}
interface Route {
  service_code: string;
  environment: string;
  workflow_id: string;
  workflow: string;
  version: number;
}
interface USSDView {
  channels: Channel[];
  routes: Route[];
}

/** The aggregators known to this build (api.Server.USSD.Adapters). */
const providers = [{ id: "africastalking", label: "Africa's Talking" }];

/** Splits an allow-list typed one per line or comma-separated. */
export function parseCIDRs(text: string): string[] {
  return text
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

/** USSD channels (docs/ussd.md): an aggregator's callbacks reach the
 * tenant through a channel holding a token, shown once, and optionally an
 * address allow-list. Which workflow answers a service code is decided by
 * publishing a workflow with a ussd trigger. */
export function USSDChannels() {
  const view = useLoad(() => get<USSDView>("/v1/ussd"), []);
  const [issued, setIssued] = useState<Channel>();
  const act = useAction();
  const v = view.data;
  if (!v) return view.error ? <ErrorBox error={view.error} /> : null;
  const connected = new Set(v.channels.map((c) => c.provider));
  const free = providers.filter((p) => !connected.has(p.id));
  const saved = (c: Channel) => {
    if (c.token) setIssued(c);
    view.reload();
  };
  return (
    <section className="card" data-testid="ussd-channels">
      <h2 style={{ marginTop: 0 }}>USSD channels</h2>
      <p className="hint">
        Connect a USSD aggregator so people who dial your service code reach the workflow published with a <code>ussd</code> trigger for it. Each callback must
        carry the channel's token, and may also have to come from listed addresses.
      </p>
      <ErrorBox error={act.error} />
      {issued && (
        <div className="notice" data-testid="ussd-issued">
          Register this callback URL with {providerLabel(issued.provider)}. It holds the channel's token and is shown once; keep it out of logs and tickets.
          <br />
          <code data-testid="ussd-callback-url" style={{ wordBreak: "break-all" }}>
            {issued.callback_url}
          </code>
          <br />
          <button onClick={() => setIssued(undefined)}>I have copied it</button>
        </div>
      )}
      {v.channels.map((c) => (
        <ChannelRow key={c.provider} channel={c} onSaved={saved} onRemoved={() => (setIssued(undefined), view.reload())} act={act} />
      ))}
      {free.length > 0 && <NewChannel providers={free} onSaved={saved} act={act} />}
      <h3>Service codes</h3>
      {v.routes.length === 0 ? (
        <p className="hint">No workflow with a USSD trigger is deployed yet.</p>
      ) : (
        <table data-testid="ussd-routes">
          <thead>
            <tr>
              <th>Environment</th>
              <th>Service code</th>
              <th>Workflow</th>
            </tr>
          </thead>
          <tbody>
            {v.routes.map((r) => (
              <tr key={r.environment + r.service_code}>
                <td>{r.environment}</td>
                <td className="mono">{r.service_code}</td>
                <td>
                  <a href={`/workflows/${r.workflow_id}`}>{r.workflow}</a> <span className="hint">v{r.version}</span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

function providerLabel(id: string) {
  return providers.find((p) => p.id === id)?.label ?? id;
}

type Act = ReturnType<typeof useAction>;

function NewChannel({ providers: options, onSaved, act }: { providers: { id: string; label: string }[]; onSaved: (c: Channel) => void; act: Act }) {
  const envs = useEnvironments();
  const [provider, setProvider] = useState(options[0]?.id ?? "");
  const [environment, setEnvironment] = useState("prod");
  const [cidrs, setCIDRs] = useState("");
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          const c = await put<Channel>(`/v1/ussd/channels/${provider}`, { environment, allowed_cidrs: parseCIDRs(cidrs) });
          setCIDRs("");
          onSaved(c);
        });
      }}
    >
      <div className="row">
        <Field label="Aggregator">
          <select value={provider} onChange={(e) => setProvider(e.target.value)}>
            {options.map((p) => (
              <option key={p.id} value={p.id}>
                {p.label}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Channel environment" hint="Callbacks reach the workflow deployed in this environment">
          <select value={environment} onChange={(e) => setEnvironment(e.target.value)}>
            {(envs.data?.environments.map((x) => x.name) ?? ["prod", "dev"]).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <Field label="Allowed addresses" hint="Optional: addresses or ranges (CIDR) callbacks must come from, one per line">
        <textarea className="mono" rows={2} value={cidrs} onChange={(e) => setCIDRs(e.target.value)} placeholder="192.0.2.0/24" />
      </Field>
      <button className="primary" type="submit" disabled={act.busy}>
        Connect
      </button>
    </form>
  );
}

function ChannelRow({ channel: c, onSaved, onRemoved, act }: { channel: Channel; onSaved: (c: Channel) => void; onRemoved: () => void; act: Act }) {
  const envs = useEnvironments();
  const [environment, setEnvironment] = useState(c.environment);
  const [cidrs, setCIDRs] = useState(c.allowed_cidrs.join("\n"));
  const [disabled, setDisabled] = useState(c.status === "disabled");
  const save = (rotate: boolean) =>
    act.run(async () => onSaved(await put<Channel>(`/v1/ussd/channels/${c.provider}`, { environment, allowed_cidrs: parseCIDRs(cidrs), disabled, rotate_token: rotate })));
  return (
    <div className="card" data-testid={`ussd-channel-${c.provider}`} style={{ marginBottom: 10 }}>
      <p>
        <strong>{providerLabel(c.provider)}</strong> <span className={`badge ${c.status === "active" ? "completed" : "cancelled"}`}>{c.status}</span>{" "}
        <span className="hint">since {fmtTime(c.created_at)}</span>
        <br />
        <span className="hint">
          Callback: <code>{c.callback_path}?token=…</code>
        </span>
      </p>
      <div className="row">
        <Field label={`Environment for ${c.provider}`}>
          <select value={environment} onChange={(e) => setEnvironment(e.target.value)}>
            {(envs.data?.environments.map((x) => x.name) ?? [c.environment]).map((n) => (
              <option key={n} value={n}>
                {n}
              </option>
            ))}
          </select>
        </Field>
        <Field label={`Allowed addresses for ${c.provider}`} hint="Empty: any address with the token">
          <textarea className="mono" rows={2} value={cidrs} onChange={(e) => setCIDRs(e.target.value)} />
        </Field>
      </div>
      <label className="row">
        <input type="checkbox" checked={disabled} onChange={(e) => setDisabled(e.target.checked)} /> Turned off (callbacks get 404)
      </label>
      <div className="toolbar">
        <button className="primary" disabled={act.busy} onClick={() => void save(false)}>
          Save
        </button>
        <button disabled={act.busy} onClick={() => confirm("Make a new token? The current callback URL stops working at once.") && void save(true)}>
          Rotate token
        </button>
        <button
          className="danger"
          disabled={act.busy}
          onClick={() => confirm(`Disconnect ${providerLabel(c.provider)}? Its callbacks get 404.`) && void act.run(async () => (await del(`/v1/ussd/channels/${c.provider}`), onRemoved()))}
        >
          Disconnect
        </button>
      </div>
    </div>
  );
}
