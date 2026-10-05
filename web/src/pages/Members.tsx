import { useState } from "react";
import { del, get, post } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, Modal, fmtTime, useAction, useLoad } from "../ui";

const ROLES = ["owner", "admin", "builder", "operator", "approver", "auditor", "viewer"];
const PERMISSIONS = ["workflow.read", "workflow.edit", "workflow.publish", "run.read", "run.start", "run.cancel", "run.resolve", "approval.decide", "pii.reveal", "pii.erase", "secret.manage", "connection.manage", "member.manage", "audit.read"];

interface Member {
  user_id: string;
  email: string;
  name: string;
  roles: string[];
}
interface Key {
  id: string;
  name: string;
  prefix: string;
  permissions: string[];
  expires_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
}

export function Members() {
  const members = useLoad(() => get<{ members: Member[] }>("/v1/members"), []);
  const keys = useLoad(() => get<{ api_keys: Key[] }>("/v1/api-keys"), []);
  const [adding, setAdding] = useState(false);
  const [minting, setMinting] = useState(false);
  const act = useAction();
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Members
        </h1>
        <button className="primary" onClick={() => setAdding(true)}>
          Add member
        </button>
      </div>
      <ErrorBox error={members.error} />
      {members.data && (
        <table>
          <thead>
            <tr>
              <th>Email</th>
              <th>Name</th>
              <th>Roles</th>
            </tr>
          </thead>
          <tbody>
            {members.data.members.map((m) => (
              <tr key={m.user_id}>
                <td>{m.email}</td>
                <td>{m.name}</td>
                <td>{m.roles.join(", ")}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="toolbar" style={{ marginTop: 24 }}>
        <h2 className="grow" style={{ margin: 0 }}>
          API keys
        </h2>
        <button onClick={() => setMinting(true)}>New API key</button>
      </div>
      <ErrorBox error={keys.error ?? act.error} />
      {keys.data && (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Key</th>
              <th>Permissions</th>
              <th>Expires</th>
              <th>Last used</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {keys.data.api_keys.map((k) => (
              <tr key={k.id}>
                <td>{k.name}</td>
                <td>
                  <code>{k.prefix}…</code>
                </td>
                <td className="hint">{k.permissions.join(", ")}</td>
                <td>{fmtTime(k.expires_at)}</td>
                <td>{fmtTime(k.last_used_at)}</td>
                <td>
                  {k.revoked_at ? (
                    <span className="hint">revoked</span>
                  ) : (
                    <button className="danger" onClick={() => confirm(`Revoke ${k.name}?`) && void act.run(async () => (await del(`/v1/api-keys/${k.id}`), keys.reload()))}>
                      Revoke
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && <AddMember onClose={() => setAdding(false)} onDone={() => (setAdding(false), members.reload())} />}
      {minting && <NewKey onClose={() => (setMinting(false), keys.reload())} />}
    </>
  );
}

function AddMember({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const { me } = useAuth();
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [roles, setRoles] = useState<string[]>(["viewer"]);
  const [custom, setCustom] = useState("");
  const act = useAction();
  return (
    <Modal title="Add member" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const all = [...roles, ...custom.split(",").map((s) => s.trim()).filter(Boolean)];
          void act.run(async () => (await post("/v1/members", { email, name, password, roles: all }), onDone()));
        }}
      >
        <Field label="Email">
          <input type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </Field>
        <Field label="Name">
          <input value={name} onChange={(e) => setName(e.target.value)} />
        </Field>
        <Field label="Initial password" hint="Needed for new users (12+ characters); ignored for existing ones">
          <input type="password" autoComplete="new-password" value={password} onChange={(e) => setPassword(e.target.value)} />
        </Field>
        <Field label="Roles">
          <div>
            {ROLES.filter((r) => r !== "owner" || me?.roles.includes("owner")).map((r) => (
              <label key={r} className="inline">
                <input type="checkbox" checked={roles.includes(r)} onChange={(e) => setRoles(e.target.checked ? [...roles, r] : roles.filter((x) => x !== r))} />
                {r}
              </label>
            ))}
          </div>
        </Field>
        <Field label="Business roles" hint="Comma-separated, e.g. payroll_approver; approval steps name these">
          <input value={custom} onChange={(e) => setCustom(e.target.value)} />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Add
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function NewKey({ onClose }: { onClose: () => void }) {
  const { can } = useAuth();
  const [name, setName] = useState("");
  const [perms, setPerms] = useState<string[]>(["run.start", "run.read"]);
  const [env, setEnv] = useState("");
  const [shown, setShown] = useState("");
  const act = useAction();
  if (shown) {
    return (
      <Modal title="API key created" onClose={onClose}>
        <p>Copy it now; it is not shown again.</p>
        <pre className="secret-once">{shown}</pre>
        <button className="primary" onClick={onClose}>
          Done
        </button>
      </Modal>
    );
  }
  return (
    <Modal title="New API key" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => setShown((await post<{ key: string }>("/v1/api-keys", { name, permissions: perms, ...(env && { environment: env }) })).key));
        }}
      >
        <Field label="Name">
          <input value={name} onChange={(e) => setName(e.target.value)} required />
        </Field>
        <Field label="Environment">
          <select value={env} onChange={(e) => setEnv(e.target.value)}>
            <option value="">All</option>
            <option value="prod">prod only</option>
            <option value="dev">dev only</option>
          </select>
        </Field>
        <Field label="Permissions" hint="A key cannot have permissions you lack">
          <div>
            {PERMISSIONS.filter(can).map((p) => (
              <label key={p} className="inline">
                <input type="checkbox" checked={perms.includes(p)} onChange={(e) => setPerms(e.target.checked ? [...perms, p] : perms.filter((x) => x !== p))} />
                {p}
              </label>
            ))}
          </div>
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || perms.length === 0}>
            Create
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
