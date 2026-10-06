import { useState } from "react";
import { del, get, post } from "../api";
import { useAuth } from "../auth";
import { addPasskey, passkeysSupported } from "../passkeys";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";

interface Passkey {
  id: string;
  name: string;
  synced: boolean;
  created_at: string;
  last_used_at: string | null;
}

/** The member's passkeys: sign-in and step-up with the device's lock. */
export function Passkeys({ onAdded }: { onAdded?: () => void }) {
  const list = useLoad(() => get<{ passkeys: Passkey[] }>("/v1/me/passkeys"), []);
  const [name, setName] = useState("");
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
                  <button className="danger" onClick={() => confirm(`Remove ${k.name}?`) && void act.run(async () => (await del(`/v1/me/passkeys/${k.id}`), list.reload()))}>
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
          <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => (await addPasskey(name || "Passkey"), setName(""), list.reload(), onAdded?.()))}>
            Add a passkey
          </button>
        </div>
      ) : (
        <p className="hint">This browser does not support passkeys.</p>
      )}
    </section>
  );
}

/** The signed-in member's own security settings: the authenticator app used
 * for step-up on approvals that need it. */
export function Account() {
  const { me, refresh } = useAuth();
  const [pending, setPending] = useState<{ secret: string; uri: string }>();
  const [code, setCode] = useState("");
  const act = useAction();
  return (
    <>
      <h1>Account</h1>
      <Passkeys />
      <section className="card">
        <h2 style={{ marginTop: 0 }}>Authenticator app</h2>
        <p className="hint">
          Some approvals need a fresh code from an authenticator app (step-up), set by the approval policy. Codes work once.
        </p>
        <ErrorBox error={act.error} />
        {me?.totp && !pending && (
          <>
            <p>An authenticator is enrolled.</p>
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
          <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => setPending(await post<{ secret: string; uri: string }>("/v1/me/totp")))}>
            Set up an authenticator
          </button>
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
