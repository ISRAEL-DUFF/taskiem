import { useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { ApiError, del, get, post, put, upload, type AuthField, type ConnectorInfo, type DriftFinding, type TenantConnector } from "../api";
import { useAdvanced, useAuth } from "../auth";
import {
  connectorOf,
  eventsInWords,
  fieldsToComplete,
  keyProblem,
  linkResult,
  modesOf,
  pollNote,
  splitFields,
  generateRsaKeyPair,
  withMoneyLogWebhooks,
  type MoneyLogWebhook,
} from "../lib/connections";
import { Help } from "../onboarding";
import { Badge, EmptyState, ErrorBox, Field, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { Copy } from "./Forms";

interface Conn {
  id: string;
  environment: string;
  connector: string;
  name: string;
  auth_type: string;
  status: string;
  /** When the credentials stop working (an OAuth token's expiry), if known. */
  expires_at: string | null;
  created_at: string;
  last_used_at: string | null;
  /** What made the connection when it was not a person: taskiem:data_tables for the data tables project's. */
  provisioned_by: string | null;
  /** live or test, when the connector has modes (a Paystack key's start, iswallet's host). */
  mode?: string | null;
  /** Subscriptions Taskiem keeps at the provider through this connection (a Xenibase webhook per workflow). */
  remote?: {
    subscriptions: number;
    health: string;
    problems: { workflow_id: string; environment: string; health: string; status_reason: string | null; last_error: string | null }[];
  };
  /** Addresses to set in the rail's dashboard so the money log hears this prod connection, workflow or not. */
  money_log_webhooks?: MoneyLogWebhook[];
  /** What the connection still needs from its service, in plain words (Paystack's Pay with Transfer turned on by its support). */
  notes?: string[];
}

/** The worst state of a connection's remote subscriptions, with what is wrong. */
function Remote({ remote }: { remote: Conn["remote"] }) {
  if (!remote) return <span className="hint">—</span>;
  const why = remote.problems.map((p) => `${p.environment}: ${p.health}${p.status_reason ? ` (${p.status_reason})` : ""}${p.last_error ? ` (${p.last_error})` : ""}`).join("\n");
  return (
    <span title={why || undefined}>
      <Badge value={remote.health} /> <span className="hint">{remote.subscriptions === 1 ? "1 webhook" : `${remote.subscriptions} webhooks`}</span>
      {remote.health !== "ok" && remote.health !== "pending" && <div className="hint">Publish the workflow again to repair it.</div>}
    </span>
  );
}

export function Connections() {
  const list = useLoad(() => get<{ connections: Conn[] }>("/v1/connections"), []);
  const connectors = useLoad(() => get<{ connectors: ConnectorInfo[] }>("/v1/connectors"), []);
  const [adding, setAdding] = useState(false);
  const [completing, setCompleting] = useState<Conn | null>(null);
  const [removing, setRemoving] = useState<Conn | null>(null);
  const location = useLocation();
  const navigate = useNavigate();
  const back = linkResult(location.search);
  const linkable = connectors.data?.connectors.filter((c) => c.oauth_linkable) ?? [];
  const connect = useAction();
  const linked = back.linked ? list.data?.connections.find((c) => c.id === back.linked) : undefined;
  const linkedConnector = linked ? connectorOf(connectors.data?.connectors, linked.connector) : undefined;
  const toComplete = fieldsToComplete(linkedConnector) ?? [];
  const dismiss = () => void navigate("/connections", { replace: true });
  // A link from chat or a readiness fix: ?connect=paystack opens that
  // service's guided screen (N6).
  const connectID = new URLSearchParams(location.search).get("connect") ?? "";
  const returnTo = back.linked || back.error ? readReturn() : "";
  return (
    <>
      <PageHeader
        title="Connections"
        description="The accounts your workflows use at other services, one per environment. Credentials are encrypted when saved and never shown again."
        actions={
          <>
            {linkable.map((c) => (
              <button key={c.ref} className="primary" disabled={connect.busy} onClick={() => void connect.run(() => startLink(c), false)}>
                <Icon name="plus" />
                Connect {c.name}
              </button>
            ))}
            <button className={linkable.length ? "" : "primary"} onClick={() => setAdding(true)}>
              <Icon name="plus" />
              New connection
            </button>
          </>
        }
      />
      <Help topic="connections" />
      <ErrorBox error={list.error ?? connect.error} />
      {back.error && (
        <div className="notice" role="alert">
          {back.error}{" "}
          <button className="link" onClick={dismiss}>
            Dismiss
          </button>
        </div>
      )}
      {back.linked && linked && (
        <div className="notice" role="status" aria-live="polite">
          <strong>{linkedConnector?.name ?? linked.connector}</strong> is connected as <code>{linked.name}</code> in {linked.environment}
          {linked.expires_at ? <> until {fmtTime(linked.expires_at)}</> : null}.
          {toComplete.length > 0 && (
            <>
              {" "}
              {linked.connector === "xenibase" ? "Writes, functions and read_rows also need the project's secret key and data API URL. " : "It may also take: " + toComplete.map((f) => f.label).join(", ") + ". "}
              <button className="link" onClick={() => setCompleting(linked)}>
                Add them now
              </button>{" "}
              or later from the row.
            </>
          )}{" "}
          {returnTo ? (
            <Link to={returnTo} onClick={() => forgetReturn()}>
              Go back to what you were doing
            </Link>
          ) : (
            <button className="link" onClick={dismiss}>
              Dismiss
            </button>
          )}
        </div>
      )}
      {!list.data && !list.error && <Skeleton />}
      {list.data && list.data.connections.length === 0 && (
        <EmptyState
          icon="connections"
          action={
            <button className="primary" onClick={() => setAdding(true)}>
              Connect a service
            </button>
          }
        >
          A connection holds the credentials a workflow's steps use to reach a service, such as a bank or a messaging provider.
        </EmptyState>
      )}
      {list.data && list.data.connections.length > 0 && (
        <ul className="items" aria-label="Connections">
          {list.data.connections.map((c) => {
            const info = connectorOf(connectors.data?.connectors, c.connector);
            const fields = c.status !== "expired" && (fieldsToComplete(info)?.length ?? 0) > 0;
            return (
              <li key={c.id} className={c.id === back.linked ? "conn highlight" : "conn"} data-testid="connection">
                <span className={c.status === "active" ? "list-icon" : "list-icon warn"} aria-hidden="true">
                  {(info?.name ?? c.connector).slice(0, 1).toUpperCase()}
                </span>
                <div className="grow">
                  <div className="item-top">
                    <span className="title">{info?.name ?? c.connector}</span>
                    <Badge value={c.status === "active" ? "ok" : c.status} />
                    {c.mode === "test" && <span className="chip">Test: no real money</span>}
                  </div>
                  <span className="sub">
                    {c.provisioned_by === "taskiem:data_tables" ? (
                      <>
                        <Link to="/tables">Data tables</Link> ({c.name}, kept by Taskiem)
                      </>
                    ) : (
                      c.name
                    )}{" "}
                    · {c.environment} · {c.last_used_at ? `last used ${fmtTime(c.last_used_at)}` : "never used"} · added {fmtTime(c.created_at)}
                  </span>
                  {c.remote && (
                    <div className="sub">
                      <Remote remote={c.remote} />
                    </div>
                  )}
                  {(c.notes ?? []).map((n) => (
                    <div key={n} className="sub" data-testid="connection-note">
                      {n}
                    </div>
                  ))}
                  {!c.provisioned_by && (
                    <span className="conn-actions">
                      {fields && <button onClick={() => setCompleting(c)}>Add fields</button>}
                      <button onClick={() => setRemoving(c)}>Disconnect</button>
                    </span>
                  )}
                </div>
              </li>
            );
          })}
        </ul>
      )}
      {list.data && <MoneyLogWebhooks conns={withMoneyLogWebhooks(list.data.connections)} connectors={connectors.data?.connectors} />}
      {adding && connectors.data && (
        <NewConnection connectors={connectors.data.connectors} onClose={() => setAdding(false)} onDone={() => (setAdding(false), list.reload())} onLink={startLink} />
      )}
      {!adding && connectID && connectors.data?.connectors.some((c) => c.id === connectID) && (
        <NewConnection
          connectors={connectors.data.connectors}
          preset={{ connector: connectID, environment: "prod", name: "" }}
          onClose={dismiss}
          onDone={() => (dismiss(), list.reload())}
          onLink={startLink}
        />
      )}
      {completing && (
        <CompleteConnection
          conn={completing}
          fields={fieldsToComplete(connectorOf(connectors.data?.connectors, completing.connector)) ?? []}
          onClose={() => setCompleting(null)}
          onDone={() => (setCompleting(null), dismiss(), list.reload())}
        />
      )}
      {removing && <Disconnect conn={removing} connector={connectorOf(connectors.data?.connectors, removing.connector)} onClose={() => setRemoving(null)} onDone={() => (setRemoving(null), list.reload())} />}
      <Drift />
      <OwnConnectors onChange={connectors.reload} />
    </>
  );
}

/** Starts connecting a connector by OAuth: the API records the request
 * and the browser goes to the provider's consent page, which sends it
 * back to this page with ?linked= or ?error=. */
const RETURN_KEY = "taskiem.connect.return";

/** Where to go back after connecting by OAuth (the provider sends the
 * browser to the Connections page): the page that asked, kept for the tab. */
function readReturn(): string {
  try {
    const v = sessionStorage.getItem(RETURN_KEY) ?? "";
    return v.startsWith("/") && !v.startsWith("//") ? v : "";
  } catch {
    return "";
  }
}

function forgetReturn() {
  try {
    sessionStorage.removeItem(RETURN_KEY);
  } catch {
    // storage refused: nothing to forget
  }
}

export async function startLink(c: ConnectorInfo, environment = "prod", name = "") {
  try {
    if (!window.location.pathname.startsWith("/connections")) sessionStorage.setItem(RETURN_KEY, window.location.pathname + window.location.search);
    else sessionStorage.removeItem(RETURN_KEY);
  } catch {
    // storage refused: the person comes back to Connections
  }
  const out = await post<{ url: string }>("/v1/connections/oauth/start", { connector: c.ref, environment, name });
  window.location.assign(out.url);
}

/** Adds the fields a linked connection still wants (Xenibase: the secret
 * key and data API URL), keeping the token. */
function CompleteConnection({ conn, fields, onClose, onDone }: { conn: Conn; fields: NonNullable<ConnectorInfo["auth"]["fields"]>; onClose: () => void; onDone: () => void }) {
  const [creds, setCreds] = useState<Record<string, string>>({});
  const act = useAction();
  const given = Object.fromEntries(Object.entries(creds).filter(([, v]) => v !== ""));
  return (
    <Modal title={`Add to ${conn.name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await put(`/v1/connections/${conn.id}/credentials`, { credentials: given }), onDone()), "Credentials saved");
        }}
      >
        <p className="hint">The fields you leave empty are not changed.</p>
        {fields.map((f) => (
          <Field key={f.key} label={f.label}>
            <input type={f.secret ? "password" : "text"} autoComplete="off" value={creds[f.key] ?? ""} onChange={(e) => setCreds({ ...creds, [f.key]: e.target.value })} />
          </Field>
        ))}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || Object.keys(given).length === 0}>
            Save
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Removes a connection. One a deployed workflow names is refused with
 * the workflows, and may be removed anyway. */
function Disconnect({ conn, connector, onClose, onDone }: { conn: Conn; connector: ConnectorInfo | undefined; onClose: () => void; onDone: () => void }) {
  const act = useAction();
  const [uses, setUses] = useState<{ id: string; name: string; environment: string }[] | null>(null);
  const remove = (force: boolean) =>
    void act.run(async () => {
      try {
        await del(`/v1/connections/${conn.id}${force ? "?force=true" : ""}`);
        onDone();
      } catch (e) {
        if (e instanceof ApiError && e.status === 409 && e.body.code === "in_use") {
          setUses((e.body.workflows as { id: string; name: string; environment: string }[]) ?? []);
          return;
        }
        throw e;
      }
    }, "Connection deleted");
  return (
    <Modal title={`Disconnect ${conn.name}`} onClose={onClose}>
      <p>
        Removes the {connector?.name ?? conn.connector} connection <code>{conn.name}</code> in {conn.environment} and its credentials.
        {conn.auth_type === "oauth2" && <> The token is also revoked at {connector?.name ?? "the provider"}.</>}
      </p>
      {uses && (
        <div className="notice" role="alert">
          Deployed workflows use this connection; their runs will fail at that step:
          <ul>
            {uses.map((u) => (
              <li key={u.id}>
                <Link to={`/workflows/${u.id}`}>{u.name}</Link> <span className="hint">({u.environment})</span>
              </li>
            ))}
          </ul>
        </div>
      )}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        {uses ? (
          <button className="danger" disabled={act.busy} onClick={() => remove(true)}>
            Disconnect anyway
          </button>
        ) : (
          <button className="danger" disabled={act.busy} onClick={() => remove(false)}>
            Disconnect
          </button>
        )}
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** Where to paste each rail connection's address so the money log hears
 * every payment on it, even when no workflow uses it (decision 0045). */
function MoneyLogWebhooks({ conns, connectors }: { conns: Conn[]; connectors: ConnectorInfo[] | undefined }) {
  if (conns.length === 0) return null;
  return (
    <section className="card" style={{ marginTop: 24 }} aria-labelledby="money-log-webhooks">
      <h2 id="money-log-webhooks">Webhook for the money log</h2>
      <p className="hint">Paste each address into the service's dashboard, and Taskiem records every payment and transfer on the account in the money log, whether or not a workflow uses it.</p>
      {conns.map((c) => {
        const service = connectorOf(connectors, c.connector)?.name ?? c.connector;
        return (
          <div key={c.id} style={{ marginTop: 16 }}>
            <h3>
              {service} <span className="hint">({c.name})</span>
            </h3>
            {(c.money_log_webhooks ?? []).map((h) => (
              <div key={h.trigger} style={{ marginTop: 8 }}>
                <strong>{h.title}</strong>
                <div className="email-address" style={{ marginTop: 4 }}>
                  <code>{h.url}</code>
                  <Copy text={h.url} label={`Copy the ${h.title} address for ${service} (${c.name})`} />
                </div>
                <p className="hint">
                  Carries {eventsInWords(h.events)}. {h.verification} {pollNote(h.poll_minutes)}
                </p>
                <p>Where to paste it: {h.where}</p>
              </div>
            ))}
          </div>
        );
      })}
    </section>
  );
}

/** Contract drift: providers answering in shapes their manifests do not declare. */
function Drift() {
  const list = useLoad(() => get<{ drift: DriftFinding[] }>("/v1/connector-drift"), []);
  const act = useAction();
  if (!list.data || list.data.drift.length === 0) return null;
  const open = list.data.drift.filter((d) => !d.acknowledged_at).length;
  return (
    <section className="card" style={{ marginTop: 24 }}>
      <h2>Contract drift {open > 0 && <Badge value={`${open} new`} />}</h2>
      <p className="hint">A provider answered in a shape its connector does not declare. Runs carried on with what the provider said; check that the workflows using these fields still do the right thing.</p>
      <ErrorBox error={list.error ?? act.error} />
      <table>
        <thead>
          <tr>
            <th>Connector</th>
            <th>Action</th>
            <th>Field</th>
            <th>Expected</th>
            <th>Seen</th>
            <th>Times</th>
            <th>Last</th>
            <th><span className="sr-only">Actions</span></th>
          </tr>
        </thead>
        <tbody>
          {list.data.drift.map((d) => (
            <tr key={[d.connector, d.version, d.action, d.path, d.kind].join(" ")}>
              <td>
                {d.connector} <span className="hint">{d.version}</span>
              </td>
              <td>{d.action}</td>
              <td>
                <code>{d.path}</code>
              </td>
              <td>{d.kind === "missing" ? "present" : d.expected}</td>
              <td>{d.kind === "missing" ? "absent" : d.observed}</td>
              <td>{d.occurrences}</td>
              <td>{d.last_run_id ? <Link to={`/runs/${d.last_run_id}`}>{fmtTime(d.last_seen)}</Link> : fmtTime(d.last_seen)}</td>
              <td>
                {d.acknowledged_at ? (
                  <span className="hint">seen by {d.acknowledged_by}</span>
                ) : (
                  <button
                    disabled={act.busy}
                    onClick={() =>
                      void act.run(async () => (await post("/v1/connector-drift/acknowledge", { connector: d.connector, version: d.version, action: d.action, path: d.path, kind: d.kind }), list.reload()), "Marked as seen")
                    }
                  >
                    Acknowledge
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

/** The tenant's own WebAssembly connectors (docs/connector-sdk.md). */
function OwnConnectors({ onChange }: { onChange: () => void }) {
  const { can } = useAuth();
  const list = useLoad(() => get<{ connectors: TenantConnector[] }>("/v1/tenant-connectors"), []);
  const [manifest, setManifest] = useState<File | null>(null);
  const [module, setModule] = useState<File | null>(null);
  const act = useAction();
  const manage = can("connector.manage");
  const changed = () => (list.reload(), onChange());
  return (
    <section className="card" style={{ marginTop: 24 }}>
      <h2>Your connectors</h2>
      <p className="hint">
        Connectors your team writes, compiled to WebAssembly. Each call runs isolated, reaching only the hosts its manifest names. Ids start with <code>x_</code>; a version never changes once uploaded.
      </p>
      <ErrorBox error={list.error ?? act.error} />
      {list.data && list.data.connectors.length === 0 && <div className="empty">None uploaded.</div>}
      {list.data && list.data.connectors.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Connector</th>
              <th>Version</th>
              <th>Module</th>
              <th>Uploaded</th>
              <th>State</th>
              {manage && <th><span className="sr-only">Actions</span></th>}
            </tr>
          </thead>
          <tbody>
            {list.data.connectors.map((c) => (
              <tr key={c.id + c.version}>
                <td>{c.ref}</td>
                <td>{c.version}</td>
                <td>
                  <code title={c.digest}>{c.digest.slice(0, 12)}</code>
                </td>
                <td>
                  {fmtTime(c.uploaded_at)} by {c.uploaded_by}
                </td>
                <td>
                  <Badge value={c.disabled_at ? "disabled" : c.active ? "active" : "superseded"} />
                </td>
                {manage && (
                  <td>
                    {!c.disabled_at && (
                      <button disabled={act.busy} onClick={() => void act.run(async () => (await post(`/v1/tenant-connectors/${c.id}/${c.version}/disable`), changed()), "Connector disabled")}>
                        Disable
                      </button>
                    )}
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {manage && (
        <form
          className="row"
          style={{ marginTop: 12, alignItems: "flex-end" }}
          onSubmit={(e) => {
            e.preventDefault();
            if (!manifest || !module) return;
            const form = new FormData();
            form.append("manifest", manifest);
            form.append("module", module);
            void act.run(async () => {
              await upload("/v1/tenant-connectors", form);
              setManifest(null);
              setModule(null);
              (e.target as HTMLFormElement).reset();
              changed();
            }, "Connector uploaded");
          }}
        >
          <Field label="Manifest (.yaml)">
            <input type="file" accept=".yaml,.yml,.json" onChange={(e) => setManifest(e.target.files?.[0] ?? null)} />
          </Field>
          <Field label="Module (.wasm)">
            <input type="file" accept=".wasm" onChange={(e) => setModule(e.target.files?.[0] ?? null)} />
          </Field>
          <button className="primary" disabled={act.busy || !manifest || !module}>
            Upload
          </button>
        </form>
      )}
    </section>
  );
}

/**
 * A new connection, as a guided screen (no-technical-steps N2): one click
 * where the service allows it; otherwise each value with where to find it,
 * checked as it is pasted and by the service before anything is saved.
 * With preset (a workflow's need: its connector id, environment and name)
 * the service, environment and name are fixed, so nothing has to match.
 */
export function NewConnection({
  connectors,
  onClose,
  onDone,
  onLink,
  preset,
}: {
  connectors: ConnectorInfo[];
  onClose: () => void;
  onDone: () => void;
  onLink: (c: ConnectorInfo, environment: string, name: string) => Promise<void>;
  preset?: { connector: string; environment: string; name: string };
}) {
  const [ref, setRef] = useState((preset && connectors.find((x) => x.id === preset.connector)?.ref) ?? connectors[0]?.ref ?? "");
  const [env, setEnv] = useState(preset?.environment ?? "prod");
  const advanced = useAdvanced();
  const [name, setName] = useState(preset?.name ?? "");
  const [creds, setCreds] = useState<Record<string, string>>({});
  const c = connectors.find((x) => x.ref === ref);
  const modes = modesOf(c);
  const [mode, setMode] = useState("");
  const chosen = mode || modes.find((m) => m.available)?.mode || "";
  const [paste, setPaste] = useState(false);
  // Public keys of the key pairs made here, by field, to show once.
  const [generated, setGenerated] = useState<Record<string, string>>({});
  const [copied, setCopied] = useState("");
  const act = useAction();
  const { main, more } = splitFields(c);
  const linkable = !!c?.auth.oauth2 && !!c.oauth_linkable;
  const problems = Object.fromEntries((c?.auth.fields ?? []).map((f) => [f.key, keyProblem(f, creds[f.key] ?? "", chosen)]));
  const blocked = Object.values(problems).some(Boolean);
  const field = (f: AuthField) => (
    <Field
      key={f.key}
      label={f.label + (f.required === false ? " (optional)" : "")}
      hint={
        <>
          {f.help}
          {f.where && (
            <>
              {f.help ? " " : ""}Find it in <strong>{f.where}</strong>
              {f.link && (
                <>
                  {" "}
                  (
                  <a href={f.link} target="_blank" rel="noreferrer">
                    open it
                  </a>
                  )
                </>
              )}
              .
            </>
          )}
          {problems[f.key] && (
            <span className="error-text" role="alert">
              {" "}
              {problems[f.key]?.charAt(0).toUpperCase()}
              {problems[f.key]?.slice(1)}.
            </span>
          )}
        </>
      }
    >
      {f.multiline ? (
        <textarea
          autoComplete="off"
          spellCheck={false}
          rows={generated[f.key] ? 2 : 5}
          value={generated[f.key] ? "(made for you and kept private)" : (creds[f.key] ?? "")}
          readOnly={!!generated[f.key]}
          required={f.required !== false}
          aria-invalid={!!problems[f.key]}
          onChange={(e) => setCreds({ ...creds, [f.key]: e.target.value.trim() })}
        />
      ) : (
        <input
          type={f.secret ? "password" : "text"}
          autoComplete="off"
          spellCheck={false}
          value={creds[f.key] ?? ""}
          required={f.required !== false}
          aria-invalid={!!problems[f.key]}
          onChange={(e) => setCreds({ ...creds, [f.key]: e.target.value.trim() })}
        />
      )}
      {f.generate === "rsa_key_pair" && !generated[f.key] && (
        <p className="hint">
          <button
            type="button"
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                const pair = await generateRsaKeyPair();
                setCreds((old) => ({ ...old, [f.key]: pair.privatePem }));
                setGenerated((old) => ({ ...old, [f.key]: pair.publicPem }));
              }, false)
            }
          >
            Make a key pair for me
          </button>{" "}
          Taskiem keeps the private key; you give the public key to the service.
        </p>
      )}
      {generated[f.key] && (
        <div className="notice">
          <p style={{ marginTop: 0 }}>
            <strong>Copy this public key now</strong> and paste it where the service asks for your public key{f.where ? <> ({f.where})</> : null}. It is shown only once; to get
            another, connect again.
          </p>
          <textarea readOnly rows={6} value={generated[f.key]} onFocus={(e) => e.target.select()} aria-label={`${f.label}: the public key to give the service`} />
          <p style={{ marginBottom: 0 }}>
            <button
              type="button"
              onClick={() => {
                void navigator.clipboard?.writeText(generated[f.key] ?? "").then(() => setCopied(f.key));
              }}
            >
              {copied === f.key ? "Copied" : "Copy the public key"}
            </button>
          </p>
        </div>
      )}
    </Field>
  );
  return (
    <Modal title={c ? `Connect ${c.name}` : "Connect a service"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (await post("/v1/connections", { connector: ref, environment: env, name: name || undefined, credentials: creds, mode: chosen || undefined, check: true }), onDone()),
            "Connected",
          );
        }}
      >
        {!preset && (
          <Field label="Service">
            <select value={ref} onChange={(e) => (setRef(e.target.value), setCreds({}), setMode(""), setPaste(false))}>
              {connectors.map((x) => (
                <option key={x.ref} value={x.ref}>
                  {x.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {c?.auth.oauth2 && (
          <div className="notice">
            {linkable ? (
              <>
                <p style={{ marginTop: 0 }}>Connect {c.name} with one click: you sign in there and approve, and nothing is copied.</p>
                <button type="button" className="primary" disabled={act.busy} onClick={() => void act.run(() => onLink(c, env, name), false)}>
                  Connect {c.name}
                </button>{" "}
                {!paste && main.length + more.length > 0 && (
                  <button type="button" className="link" onClick={() => setPaste(true)}>
                    Paste details instead
                  </button>
                )}
              </>
            ) : (
              <>One-click connecting to {c.name} is not set up on this Taskiem yet, so paste its details below.</>
            )}
          </div>
        )}
        {(!linkable || paste) && (
          <>
            {modes.length > 1 && (
              <fieldset className="choice">
                <legend>Which kind?</legend>
                {modes.map((m) => (
                  <label key={m.mode} className="inline">
                    <input type="radio" name="mode" checked={chosen === m.mode} disabled={!m.available} onChange={() => setMode(m.mode)} />
                    {m.label}
                    {!m.available && <span className="hint"> (not set up on this Taskiem yet)</span>}
                  </label>
                ))}
              </fieldset>
            )}
            {main.map(field)}
          </>
        )}
        <details>
          <summary>More options</summary>
          {(!linkable || paste) && more.map(field)}
          {!preset && (
            <div className="row">
              <Field label="Name" hint="Leave it empty: it is called main, which templates and the AI use.">
                <input value={name} placeholder="main" onChange={(e) => setName(e.target.value)} />
              </Field>
              {advanced && (
                <Field label="Environment">
                  <select value={env} onChange={(e) => setEnv(e.target.value)}>
                    <option value="prod">prod (the real one)</option>
                    <option value="dev">dev (for trying things)</option>
                  </select>
                </Field>
              )}
            </div>
          )}
        </details>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          {(!linkable || paste) && (
            <button type="submit" className="primary" disabled={act.busy || blocked}>
              {act.busy ? "Checking…" : "Check and save"}
            </button>
          )}
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
