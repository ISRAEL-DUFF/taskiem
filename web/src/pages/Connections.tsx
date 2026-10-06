import { useState } from "react";
import { get, post, upload, type ConnectorInfo, type TenantConnector } from "../api";
import { useAuth } from "../auth";
import { Badge, ErrorBox, Field, Modal, fmtTime, useAction, useLoad } from "../ui";

interface Conn {
  id: string;
  environment: string;
  connector: string;
  name: string;
  auth_type: string;
  status: string;
  created_at: string;
}

export function Connections() {
  const list = useLoad(() => get<{ connections: Conn[] }>("/v1/connections"), []);
  const connectors = useLoad(() => get<{ connectors: ConnectorInfo[] }>("/v1/connectors"), []);
  const [adding, setAdding] = useState(false);
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Connections
        </h1>
        <button className="primary" onClick={() => setAdding(true)}>
          New connection
        </button>
      </div>
      <p className="hint">Credentials are encrypted when saved and never shown again.</p>
      <ErrorBox error={list.error} />
      {list.data && list.data.connections.length === 0 && <div className="empty card">No connections yet.</div>}
      {list.data && list.data.connections.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Connector</th>
              <th>Name</th>
              <th>Environment</th>
              <th>Status</th>
              <th>Created</th>
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
                <td>{fmtTime(c.created_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && connectors.data && <NewConnection connectors={connectors.data.connectors} onClose={() => setAdding(false)} onDone={() => (setAdding(false), list.reload())} />}
      <OwnConnectors onChange={connectors.reload} />
    </>
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
              {manage && <th />}
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
