import { useState } from "react";
import { Link, useLocation, useNavigate } from "react-router-dom";
import { ApiError, del, get, post, put, upload, type ConnectorInfo, type DriftFinding, type TenantConnector } from "../api";
import { useAuth } from "../auth";
import { connectorOf, fieldsToComplete, linkResult } from "../lib/connections";
import { Help } from "../onboarding";
import { Badge, EmptyState, ErrorBox, Field, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";

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
  /** Subscriptions Taskiem keeps at the provider through this connection (a PGDock webhook per workflow). */
  remote?: {
    subscriptions: number;
    health: string;
    problems: { workflow_id: string; environment: string; health: string; status_reason: string | null; last_error: string | null }[];
  };
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
  return (
    <>
      <PageHeader
        title="Connections"
        description="The accounts your workflows use at other services, one per environment. Credentials are encrypted when saved and never shown again."
        actions={
          <>
            {linkable.map((c) => (
              <button key={c.ref} className="primary" disabled={connect.busy} onClick={() => void connect.run(() => startLink(c))}>
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
              {linked.connector === "pgdock" ? "Writes, functions and read_rows also need the project's secret key and data API URL. " : "It may also take: " + toComplete.map((f) => f.label).join(", ") + ". "}
              <button className="link" onClick={() => setCompleting(linked)}>
                Add them now
              </button>{" "}
              or later from the row.
            </>
          )}{" "}
          <button className="link" onClick={dismiss}>
            Dismiss
          </button>
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
        <table>
          <thead>
            <tr>
              <th>Connector</th>
              <th>Name</th>
              <th>Environment</th>
              <th>Status</th>
              <th>Webhooks</th>
              <th>Created</th>
              <th>Last used</th>
              <th><span className="sr-only">Actions</span></th>
            </tr>
          </thead>
          <tbody>
            {list.data.connections.map((c) => (
              <tr key={c.id} className={c.id === back.linked ? "highlight" : undefined}>
                <td>{c.connector}</td>
                <td>
                  {c.provisioned_by === "taskiem:data_tables" ? (
                    <>
                      <Link to="/tables">Data tables</Link> <span className="hint">({c.name}, kept by Taskiem)</span>
                    </>
                  ) : (
                    c.name
                  )}
                </td>
                <td>{c.environment}</td>
                <td>
                  <Badge value={c.status === "active" ? "ok" : c.status} />
                </td>
                <td>
                  <Remote remote={c.remote} />
                </td>
                <td>{fmtTime(c.created_at)}</td>
                <td>{c.last_used_at ? fmtTime(c.last_used_at) : <span className="hint">never</span>}</td>
                <td>
                  {!c.provisioned_by && (
                    <span className="row" style={{ gap: 6 }}>
                      {c.status !== "expired" && (fieldsToComplete(connectorOf(connectors.data?.connectors, c.connector))?.length ?? 0) > 0 && (
                        <button onClick={() => setCompleting(c)}>Add fields</button>
                      )}
                      <button onClick={() => setRemoving(c)}>Disconnect</button>
                    </span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && connectors.data && (
        <NewConnection connectors={connectors.data.connectors} onClose={() => setAdding(false)} onDone={() => (setAdding(false), list.reload())} onLink={startLink} />
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
async function startLink(c: ConnectorInfo, environment = "prod", name = "default") {
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
          void act.run(async () => (await put(`/v1/connections/${conn.id}/credentials`, { credentials: given }), onDone()));
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
    });
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
                      void act.run(async () => (await post("/v1/connector-drift/acknowledge", { connector: d.connector, version: d.version, action: d.action, path: d.path, kind: d.kind }), list.reload()))
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
                      <button disabled={act.busy} onClick={() => void act.run(async () => (await post(`/v1/tenant-connectors/${c.id}/${c.version}/disable`), changed()))}>
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
            });
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

function NewConnection({ connectors, onClose, onDone, onLink }: { connectors: ConnectorInfo[]; onClose: () => void; onDone: () => void; onLink: (c: ConnectorInfo, environment: string, name: string) => Promise<void> }) {
  const [ref, setRef] = useState(connectors[0]?.ref ?? "");
  const [env, setEnv] = useState("prod");
  const [name, setName] = useState("default");
  const [creds, setCreds] = useState<Record<string, string>>({});
  const act = useAction();
  const c = connectors.find((x) => x.ref === ref);
  return (
    <Modal title="New connection" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await post("/v1/connections", { connector: ref, environment: env, name, credentials: creds }), onDone()));
        }}
      >
        {c?.auth.oauth2 && (
          <div className="notice">
            {c.oauth_linkable ? (
              <>
                Connect {c.name} with one click: you approve the connection there and nothing is pasted.{" "}
                <button type="button" className="link" disabled={act.busy} onClick={() => void act.run(() => onLink(c, env, name))}>
                  Connect {c.name}
                </button>
              </>
            ) : (
              <>This installation is not registered with {c.name}, so paste its credentials below.</>
            )}
          </div>
        )}
        <div className="row">
          <Field label="Connector">
            <select value={ref} onChange={(e) => (setRef(e.target.value), setCreds({}))}>
              {connectors.map((x) => (
                <option key={x.ref} value={x.ref}>
                  {x.name}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Environment">
            <select value={env} onChange={(e) => setEnv(e.target.value)}>
              <option>prod</option>
              <option>dev</option>
            </select>
          </Field>
        </div>
        <Field label="Name">
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        {c?.auth.fields?.map((f) => (
          <Field key={f.key} label={f.label + (f.required === false ? "" : " *")}>
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
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
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
