import { useState } from "react";
import { del, get, post, type Invitation } from "../api";
import { useAuth } from "../auth";
import { addPasskey, passkeysSupported, proof } from "../passkeys";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";

interface Passkey {
  id: string;
  name: string;
  synced: boolean;
  created_at: string;
  last_used_at: string | null;
}

/** What the member types to prove it is them before changing a factor:
 * nothing when they have a passkey (the browser asks for it), else their
 * authenticator code or password. */
function ProofField({ to, value, onChange }: { to: string; value: string; onChange: (v: string) => void }) {
  const { me } = useAuth();
  const f = me?.factors;
  if (!f || f.passkey) return null;
  if (f.totp)
    return (
      <Field label={`Authenticator code, to ${to}`}>
        <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={value} onChange={(e) => onChange(e.target.value.replace(/\D/g, ""))} />
      </Field>
    );
  if (f.password)
    return (
      <Field label={`Your password, to ${to}`}>
        <input type="password" autoComplete="current-password" value={value} onChange={(e) => onChange(e.target.value)} />
      </Field>
    );
  return null;
}

/** The member's passkeys: sign-in and step-up with the device's lock. */
export function Passkeys({ onAdded }: { onAdded?: () => void }) {
  const { me, refresh } = useAuth();
  const list = useLoad(() => get<{ passkeys: Passkey[] }>("/v1/me/passkeys"), []);
  const [name, setName] = useState("");
  const [typed, setTyped] = useState("");
  const act = useAction();
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Passkeys</h2>
      <p className="hint">A passkey signs you in with your device's fingerprint, face or PIN, and cannot be phished. It also answers step-up on approvals.</p>
      <ErrorBox error={list.error ?? act.error} />
      {list.data && list.data.passkeys.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Name</th>
              <th>Added</th>
              <th>Last used</th>
              <th />
            </tr>
          </thead>
          <tbody>
            {list.data.passkeys.map((k) => (
              <tr key={k.id}>
                <td>
                  {k.name} {k.synced && <span className="hint">synced</span>}
                </td>
                <td>{fmtTime(k.created_at)}</td>
                <td>{fmtTime(k.last_used_at)}</td>
                <td>
                  <button
                    className="danger"
                    onClick={() =>
                      confirm(`Remove ${k.name}?`) &&
                      void act.run(async () => (await del(`/v1/me/passkeys/${k.id}`, await proof(me?.factors, typed)), setTyped(""), list.reload(), await refresh()))
                    }
                  >
                    Remove
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {passkeysSupported() ? (
        <div className="row" style={{ alignItems: "flex-end" }}>
          <Field label="Name for this passkey">
            <input value={name} placeholder="e.g. Work laptop" onChange={(e) => setName(e.target.value)} />
          </Field>
          <ProofField to="add or remove a passkey" value={typed} onChange={setTyped} />
          <button
            className="primary"
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => (await addPasskey(name || "Passkey", await proof(me?.factors, typed)), setName(""), setTyped(""), list.reload(), await refresh(), onAdded?.()))
            }
          >
            Add a passkey
          </button>
        </div>
      ) : (
        <p className="hint">This browser does not support passkeys.</p>
      )}
    </section>
  );
}

/** Organisations that invited the member: they join when they accept. */
function Invitations() {
  const list = useLoad(() => get<{ invitations: Invitation[] }>("/v1/me/invitations"), []);
  const act = useAction();
  if (!list.data?.invitations.length) return null;
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Invitations</h2>
      <ErrorBox error={act.error} />
      <table>
        <tbody>
          {list.data.invitations.map((i) => (
            <tr key={i.tenant_id}>
              <td>{i.tenant}</td>
              <td>{i.roles.join(", ")}</td>
              <td>{fmtTime(i.invited_at)}</td>
              <td>
                <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => (await post(`/v1/me/invitations/${i.tenant_id}/accept`), list.reload()))}>
                  Accept
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="hint">Once accepted, choose the organisation when you sign in.</p>
    </section>
  );
}

/** The signed-in member's own security settings: the authenticator app used
 * for step-up on approvals that need it. */
export function Account() {
  const { me, refresh } = useAuth();
  const [pending, setPending] = useState<{ secret: string; uri: string }>();
  const [code, setCode] = useState("");
  const [typed, setTyped] = useState("");
  const act = useAction();
  return (
    <>
      <h1>Account</h1>
      <Invitations />
      <Passkeys />
      <section className="card">
        <h2 style={{ marginTop: 0 }}>Authenticator app</h2>
        <p className="hint">
          Some approvals need a fresh code from an authenticator app (step-up), set by the approval policy. Codes work once. Five wrong codes in fifteen minutes lock codes out for fifteen minutes.
        </p>
        <ErrorBox error={act.error} />
        {me?.totp && !pending && (
          <>
            <p>An authenticator is enrolled. To change it, remove it and set it up again.</p>
            <div className="row">
              <Field label="Current code, to remove it">
                <input inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
              </Field>
              <button className="danger" disabled={act.busy || code.length !== 6} onClick={() => void act.run(async () => (await del("/v1/me/totp", { code }), setCode(""), await refresh()))}>
                Remove
              </button>
            </div>
          </>
        )}
        {!me?.totp && !pending && (
          <div className="row" style={{ alignItems: "flex-end" }}>
            <ProofField to="set up an authenticator" value={typed} onChange={setTyped} />
            <button
              className="primary"
              disabled={act.busy}
              onClick={() => void act.run(async () => (setPending(await post<{ secret: string; uri: string }>("/v1/me/totp", await proof(me?.factors, typed))), setTyped("")))}
            >
              Set up an authenticator
            </button>
          </div>
        )}
        {pending && (
          <>
            <p>
              Add this account to your authenticator app with the key below (or open the link on the phone that has the app), then enter the code it shows.
            </p>
            <p>
              Key <code data-testid="totp-secret">{pending.secret}</code>
              <br />
              <a href={pending.uri}>Open in authenticator app</a>
            </p>
            <div className="row">
              <Field label="Code from the app">
                <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
              </Field>
              <button
                className="primary"
                disabled={act.busy || code.length !== 6}
                onClick={() => void act.run(async () => (await post("/v1/me/totp/confirm", { code }), setPending(undefined), setCode(""), await refresh()))}
              >
                Confirm
              </button>
            </div>
          </>
        )}
      </section>
    </>
  );
}
