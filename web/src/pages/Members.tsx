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
      {can("member.manage") && <SingleSignOn />}
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

interface SSODomain {
  domain: string;
  txt_record: string;
  txt_value: string;
  verified_at: string | null;
}
interface SSOConnection {
  id: string;
  protocol: "oidc" | "saml";
  name: string;
  default_roles: string[];
  group_roles: Record<string, string[]>;
  jit: boolean;
  enforce: boolean;
  disabled: boolean;
  domains: SSODomain[] | null;
  redirect_uri?: string;
  entity_id?: string;
  acs_url?: string;
  metadata_url?: string;
}

/** Single sign-on: the tenant's OIDC or SAML identity providers. */
function SingleSignOn() {
  const list = useLoad(() => get<{ connections: SSOConnection[] }>("/v1/sso"), []);
  const [adding, setAdding] = useState(false);
  const [domain, setDomain] = useState<Record<string, string>>({});
  const act = useAction();
  const update = (c: SSOConnection, change: Partial<SSOConnection>) =>
    act.run(async () => {
      const n = { ...c, ...change };
      await put(`/v1/sso/${c.id}`, { default_roles: n.default_roles, group_roles: n.group_roles, jit: n.jit, enforce: n.enforce, disabled: n.disabled });
      list.reload();
    });
  return (
    <>
      <div className="toolbar" style={{ marginTop: 24 }}>
        <h2 className="grow" style={{ margin: 0 }}>
          Single sign-on
        </h2>
        <button onClick={() => setAdding(true)}>Add identity provider</button>
      </div>
      <p className="hint">
        People sign in through your identity provider when their email is on a domain you have verified. Roles follow their groups; single sign-on only manages the roles it gave. Enforced, members on those domains cannot use passwords or passkeys (owners excepted, so a broken provider cannot lock you out).
      </p>
      <ErrorBox error={list.error ?? act.error} />
      {list.data?.connections.map((c) => (
        <section key={c.id} className="card">
          <div className="toolbar">
            <strong className="grow">
              {c.name} <span className="hint">{c.protocol.toUpperCase()}</span> {c.disabled && <span className="hint">disabled</span>}
            </strong>
            <label className="inline">
              <input type="checkbox" checked={c.enforce} onChange={(e) => void update(c, { enforce: e.target.checked })} /> Enforce
            </label>
            <label className="inline">
              <input type="checkbox" checked={c.jit} onChange={(e) => void update(c, { jit: e.target.checked })} /> Create members on first sign-in
            </label>
            <button onClick={() => void update(c, { disabled: !c.disabled })}>{c.disabled ? "Enable" : "Disable"}</button>
          </div>
          <p className="hint">
            {c.protocol === "oidc" ? (
              <>
                Redirect URI for the provider: <code>{c.redirect_uri}</code>
              </>
            ) : (
              <>
                Entity ID <code>{c.entity_id}</code> · ACS URL <code>{c.acs_url}</code> · <a href={c.metadata_url}>SP metadata</a>
              </>
            )}
          </p>
          <p className="hint">
            Default roles: {c.default_roles.join(", ") || "none"} · Groups: {Object.entries(c.group_roles).map(([g, r]) => `${g} → ${r.join(", ")}`).join("; ") || "none"}
          </p>
          <table>
            <thead>
              <tr>
                <th>Domain</th>
                <th>Verification</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {(c.domains ?? []).map((d) => (
                <tr key={d.domain}>
                  <td>{d.domain}</td>
                  <td className="hint">
                    {d.verified_at ? (
                      `verified ${fmtTime(d.verified_at)}`
                    ) : (
                      <>
                        Add a TXT record <code>{d.txt_record}</code> with value <code>{d.txt_value}</code>
                      </>
                    )}
                  </td>
                  <td>{!d.verified_at && <button onClick={() => void act.run(async () => (await post(`/v1/sso/domains/${d.domain}/verify`), list.reload()))}>Verify</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <div className="row" style={{ alignItems: "flex-end" }}>
            <Field label="Add a domain">
              <input value={domain[c.id] ?? ""} placeholder="bank.com" onChange={(e) => setDomain({ ...domain, [c.id]: e.target.value })} />
            </Field>
            <button disabled={!domain[c.id]} onClick={() => void act.run(async () => (await post(`/v1/sso/${c.id}/domains`, { domain: domain[c.id] }), setDomain({ ...domain, [c.id]: "" }), list.reload()))}>
              Add
            </button>
          </div>
        </section>
      ))}
      {adding && <AddSSO onClose={() => setAdding(false)} onDone={() => (setAdding(false), list.reload())} />}
    </>
  );
}

function AddSSO({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [protocol, setProtocol] = useState<"oidc" | "saml">("oidc");
  const [name, setName] = useState("");
  const [issuer, setIssuer] = useState("");
  const [clientID, setClientID] = useState("");
  const [secret, setSecret] = useState("");
  const [groupsField, setGroupsField] = useState("groups");
  const [metadata, setMetadata] = useState("");
  const [defaults, setDefaults] = useState("viewer");
  const [mapping, setMapping] = useState("");
  const act = useAction();
  const parseMapping = () => {
    const out: Record<string, string[]> = {};
    for (const line of mapping.split("\n")) {
      const [g, roles] = line.split("=").map((x) => x.trim());
      if (g && roles) out[g] = roles.split(",").map((r) => r.trim()).filter(Boolean);
    }
    return out;
  };
  return (
    <Modal title="Add identity provider" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const roles = defaults.split(",").map((r) => r.trim()).filter(Boolean);
          const body =
            protocol === "oidc"
              ? { protocol, name, oidc: { issuer, client_id: clientID, client_secret: secret, groups_claim: groupsField }, default_roles: roles, group_roles: parseMapping() }
              : { protocol, name, saml: { metadata_xml: metadata, name_attr: "name", groups_attr: groupsField }, default_roles: roles, group_roles: parseMapping() };
          void act.run(async () => (await post("/v1/sso", body), onDone()));
        }}
      >
        <Field label="Protocol">
          <select value={protocol} onChange={(e) => setProtocol(e.target.value as "oidc" | "saml")}>
            <option value="oidc">OpenID Connect</option>
            <option value="saml">SAML 2.0</option>
          </select>
        </Field>
        <Field label="Name">
          <input value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. Okta" required />
        </Field>
        {protocol === "oidc" ? (
          <>
            <Field label="Issuer URL">
              <input value={issuer} onChange={(e) => setIssuer(e.target.value)} placeholder="https://login.example.com" required />
            </Field>
            <Field label="Client ID">
              <input value={clientID} onChange={(e) => setClientID(e.target.value)} required />
            </Field>
            <Field label="Client secret" hint="Encrypted when saved; never shown again">
              <input type="password" value={secret} onChange={(e) => setSecret(e.target.value)} required />
            </Field>
          </>
        ) : (
          <Field label="Identity provider metadata (XML)">
            <textarea rows={6} value={metadata} onChange={(e) => setMetadata(e.target.value)} required />
          </Field>
        )}
        <Field label={protocol === "oidc" ? "Groups claim" : "Groups attribute"}>
          <input value={groupsField} onChange={(e) => setGroupsField(e.target.value)} />
        </Field>
        <Field label="Default roles" hint="Comma-separated; everyone who signs in gets these">
          <input value={defaults} onChange={(e) => setDefaults(e.target.value)} />
        </Field>
        <Field label="Group mapping" hint="One per line: group = role, role">
          <textarea rows={3} value={mapping} onChange={(e) => setMapping(e.target.value)} placeholder={"treasury = operator\npayroll-approvers = approver, payroll_approver"} />
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
