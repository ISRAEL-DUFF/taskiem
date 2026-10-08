import { useState } from "react";
import { Link } from "react-router-dom";
import { get, post, upload, type ConnectorInfo, type DriftFinding, type TenantConnector } from "../api";
import { useAuth } from "../auth";
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
  created_at: string;
  last_used_at: string | null;
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
  return (
    <>
      <PageHeader
        title="Connections"
        description="The accounts your workflows use at other services, one per environment. Credentials are encrypted when saved and never shown again."
        actions={
          <button className="primary" onClick={() => setAdding(true)}>
            <Icon name="plus" />
            New connection
          </button>
        }
      />
      <Help topic="connections" />
      <ErrorBox error={list.error} />
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
            </tr>
          </thead>
          <tbody>
            {list.data.connections.map((c) => (
              <tr key={c.id}>
                <td>{c.connector}</td>
                <td>{c.name}</td>
                <td>{c.environment}</td>
                <td>
                  <Badge value={c.status === "active" ? "ok" : c.status} />
                </td>
                <td>
                  <Remote remote={c.remote} />
                </td>
                <td>{fmtTime(c.created_at)}</td>
                <td>{c.last_used_at ? fmtTime(c.last_used_at) : <span className="hint">never</span>}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && connectors.data && <NewConnection connectors={connectors.data.connectors} onClose={() => setAdding(false)} onDone={() => (setAdding(false), list.reload())} />}
      <Drift />
      <OwnConnectors onChange={connectors.reload} />
    </>
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

function NewConnection({ connectors, onClose, onDone }: { connectors: ConnectorInfo[]; onClose: () => void; onDone: () => void }) {
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
