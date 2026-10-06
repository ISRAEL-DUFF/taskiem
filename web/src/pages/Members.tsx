import { useState } from "react";
import { del, get, post, put } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, Modal, fmtTime, useAction, useLoad } from "../ui";

interface Permission {
  name: string;
  description: string;
}
interface Role {
  name: string;
  description: string;
  permissions: string[];
  built_in: boolean;
  members: number;
}

const usePermissions = () => useLoad(() => get<{ permissions: Permission[] }>("/v1/permissions"), []);
const useRoles = () => useLoad(() => get<{ roles: Role[] }>("/v1/roles"), []);

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
  const { can } = useAuth();
  const members = useLoad(() => get<{ members: Member[] }>("/v1/members"), []);
  const keys = useLoad(() => get<{ api_keys: Key[] }>("/v1/api-keys"), []);
  const roles = useRoles();
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<Role | "new" | null>(null);
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
      <ErrorBox error={members.error ?? act.error} />
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
                <td>
                  {m.roles.map((r) => (
                    <span key={r} className="chip">
                      {r}
                      <button
                        className="chip-x"
                        aria-label={`Take ${r} from ${m.email}`}
                        title={`Take ${r} from ${m.email}`}
                        onClick={() => confirm(`Take ${r} from ${m.email}?`) && void act.run(async () => (await del(`/v1/members/${m.user_id}/roles/${encodeURIComponent(r)}`), members.reload(), roles.reload()))}
                      >
                        ×
                      </button>
                    </span>
                  ))}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <div className="toolbar" style={{ marginTop: 24 }}>
        <h2 className="grow" style={{ margin: 0 }}>
          Roles
        </h2>
        {can("role.manage") && <button onClick={() => setEditing("new")}>New role</button>}
      </div>
      <p className="hint">Custom roles are named sets of permissions. No one can create or grant a role with permissions they do not hold themselves.</p>
      <ErrorBox error={roles.error} />
      {roles.data && (
        <table>
          <thead>
            <tr>
              <th>Role</th>
              <th>Permissions</th>
              <th>Members</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {roles.data.roles.map((r) => (
              <tr key={r.name}>
                <td>
                  {r.name} {r.built_in && <span className="hint">built-in</span>}
                  {r.description && <div className="hint">{r.description}</div>}
                </td>
                <td className="hint">{r.permissions.join(", ")}</td>
                <td>{r.members}</td>
                <td>
                  {!r.built_in && can("role.manage") && (
                    <>
                      <button onClick={() => setEditing(r)}>Edit</button>{" "}
                      <button className="danger" disabled={r.members > 0} onClick={() => confirm(`Delete ${r.name}?`) && void act.run(async () => (await del(`/v1/roles/${r.name}`), roles.reload()))}>
                        Delete
                      </button>
                    </>
                  )}
                </td>
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
      {adding && <AddMember roles={roles.data?.roles ?? []} onClose={() => setAdding(false)} onDone={() => (setAdding(false), members.reload(), roles.reload())} />}
      {editing && <EditRole role={editing === "new" ? null : editing} onClose={() => setEditing(null)} onDone={() => (setEditing(null), roles.reload())} />}
      {minting && <NewKey onClose={() => (setMinting(false), keys.reload())} />}
    </>
  );
}

function EditRole({ role, onClose, onDone }: { role: Role | null; onClose: () => void; onDone: () => void }) {
  const { can } = useAuth();
  const perms = usePermissions();
  const [name, setName] = useState(role?.name ?? "");
  const [description, setDescription] = useState(role?.description ?? "");
  const [chosen, setChosen] = useState<string[]>(role?.permissions ?? []);
  const act = useAction();
  return (
    <Modal title={role ? `Edit ${role.name}` : "New role"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await put(`/v1/roles/${name}`, { description, permissions: chosen }), onDone()));
        }}
      >
        {!role && (
          <Field label="Name" hint="Lowercase letters, digits and underscores, e.g. treasury_ops">
            <input value={name} onChange={(e) => setName(e.target.value)} pattern="[a-z][a-z0-9_]{1,47}" required />
          </Field>
        )}
        <Field label="Description">
          <input value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        <Field label="Permissions" hint="Only permissions you hold can be given">
          <div>
            {perms.data?.permissions.map((p) => (
              <label key={p.name} className="inline" title={p.description}>
                <input
                  type="checkbox"
                  disabled={!can(p.name)}
                  checked={chosen.includes(p.name)}
                  onChange={(e) => setChosen(e.target.checked ? [...chosen, p.name] : chosen.filter((x) => x !== p.name))}
                />
                {p.name}
              </label>
            ))}
          </div>
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || chosen.length === 0 || !name}>
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

function AddMember({ roles: available, onClose, onDone }: { roles: Role[]; onClose: () => void; onDone: () => void }) {
  const { me, can } = useAuth();
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
            {available.filter((r) => (r.name !== "owner" || me?.roles.includes("owner")) && r.permissions.every(can)).map(({ name: r }) => (
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
  const all = usePermissions();
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
            {(all.data?.permissions.map((x) => x.name) ?? []).filter(can).map((p) => (
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
