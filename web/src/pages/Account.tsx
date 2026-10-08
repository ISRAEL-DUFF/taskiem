import { useState } from "react";
import { del, get, post, put, type Invitation } from "../api";
import { useAuth } from "../auth";
import { addPasskey, passkeysSupported, proof } from "../passkeys";
import { ErrorBox, Field, PageHeader, fmtTime, useAction, useLoad } from "../ui";
import { ChangePassword } from "./Password";

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
              <th><span className="sr-only">Actions</span></th>
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
                      void act.run(async () => (await del(`/v1/me/passkeys/${k.id}`, await proof(me?.factors, typed, `passkey.remove/${k.id}`)), setTyped(""), list.reload(), await refresh()))
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
              void act.run(async () => (await addPasskey(name || "Passkey", await proof(me?.factors, typed, "passkey.add")), setName(""), setTyped(""), list.reload(), await refresh(), onAdded?.()))
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

interface WhatsAppBinding {
  enabled: boolean;
  platform_number?: string;
  number: string | null;
  verified_at: string | null;
  pending?: { number: string; expires_at: string };
  pin?: { set: boolean; set_at?: string; locked_until?: string };
  flows?: boolean;
}

/** The WhatsApp approval PIN: entered in a WhatsApp form, it confirms
 * decisions whose policy accepts it (step_up whatsapp_pin). */
function WhatsAppPin({ pin, onChange }: { pin: NonNullable<WhatsAppBinding["pin"]>; onChange: () => void }) {
  const { me } = useAuth();
  const [value, setValue] = useState("");
  const [typed, setTyped] = useState("");
  const act = useAction();
  const strong = me?.factors?.passkey || me?.factors?.totp;
  return (
    <div data-testid="whatsapp-pin">
      <h3>Approval PIN</h3>
      <p className="hint">
        Six digits you enter in WhatsApp to confirm approvals whose policy allows a WhatsApp PIN. Policies asking for a passkey or authenticator code still
        send you here. Five wrong PINs lock it for 15 minutes.
      </p>
      <ErrorBox error={act.error} />
      {pin.set && (
        <div className="row">
          <p className="grow">
            PIN set {pin.set_at && <span className="hint">{fmtTime(pin.set_at)}</span>}
            {pin.locked_until && <strong> · locked until {fmtTime(pin.locked_until)}</strong>}
          </p>
          <button className="danger" disabled={act.busy} onClick={() => confirm("Remove your WhatsApp PIN?") && void act.run(async () => (await del("/v1/me/whatsapp/pin"), onChange()))}>
            Remove
          </button>
        </div>
      )}
      {strong ? (
        <form
          className="row"
          style={{ alignItems: "flex-end" }}
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => (await put("/v1/me/whatsapp/pin", { pin: value, ...(await proof(me?.factors, typed, "whatsapp.pin")) }), setValue(""), setTyped(""), onChange()));
          }}
        >
          <Field label={pin.set ? "New PIN" : "PIN"} hint="Not one digit repeated, nor a run like 123456">
            <input type="password" inputMode="numeric" autoComplete="new-password" maxLength={6} value={value} onChange={(e) => setValue(e.target.value.replace(/\D/g, ""))} />
          </Field>
          <ProofField to="set the PIN" value={typed} onChange={setTyped} />
          <button type="submit" disabled={act.busy || value.length !== 6}>
            {pin.set ? "Change PIN" : "Set PIN"}
          </button>
        </form>
      ) : (
        <p className="hint">Add a passkey or an authenticator first: the PIN stands in for them in WhatsApp.</p>
      )}
    </div>
  );
}

/** The member's WhatsApp number: linked by a code sent to it, it lets them
 * approve, check status and start runs from WhatsApp. */
function WhatsApp() {
  const { me } = useAuth();
  const b = useLoad(() => get<WhatsAppBinding>("/v1/me/whatsapp"), []);
  const [number, setNumber] = useState("");
  const [code, setCode] = useState("");
  const [typed, setTyped] = useState("");
  const [sent, setSent] = useState(false);
  const act = useAction();
  if (!b.data?.enabled) return b.error ? <ErrorBox error={b.error} /> : null;
  const pending = b.data.pending;
  return (
    <section className="card" data-testid="whatsapp">
      <h2 style={{ marginTop: 0 }}>WhatsApp</h2>
      <p className="hint">
        Link your WhatsApp number to approve requests, check what ran and start workflows by chat
        {b.data.platform_number && (
          <>
            {" "}
            with Taskiem's number <strong>{b.data.platform_number}</strong>
          </>
        )}
        . Your roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.
      </p>
      <ErrorBox error={act.error} />
      {b.data.number && (
        <div className="row">
          <p className="grow">
            Linked: <strong data-testid="whatsapp-number">{b.data.number}</strong> {b.data.verified_at && <span className="hint">since {fmtTime(b.data.verified_at)}</span>}
          </p>
          <button className="danger" disabled={act.busy} onClick={() => confirm("Unlink this number?") && void act.run(async () => (await del("/v1/me/whatsapp"), b.reload()))}>
            Unlink
          </button>
        </div>
      )}
      <form
        className="row"
        style={{ alignItems: "flex-end" }}
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await post("/v1/me/whatsapp", { number, ...(await proof(me?.factors, typed, "whatsapp.link")) }), setTyped(""), setSent(true), b.reload()));
        }}
      >
        <Field label={b.data.number ? "Link another number instead" : "Your WhatsApp number"} hint="With the country code, e.g. +2348012345678">
          <input type="tel" value={number} onChange={(e) => setNumber(e.target.value)} placeholder="+234…" required />
        </Field>
        <ProofField to="link a number" value={typed} onChange={setTyped} />
        <button type="submit" disabled={act.busy}>
          Send a code
        </button>
      </form>
      {(pending || sent) && (
        <form
          className="row"
          style={{ alignItems: "flex-end" }}
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => (await post("/v1/me/whatsapp/verify", { code }), setCode(""), setNumber(""), setSent(false), b.reload()));
          }}
        >
          <Field label={`Code sent to ${pending?.number ?? "your number"}`} hint="Or send the code back to Taskiem in WhatsApp. It works for 10 minutes.">
            <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
          </Field>
          <button className="primary" type="submit" disabled={act.busy || code.length !== 6}>
            Link
          </button>
        </form>
      )}
      {b.data.number && b.data.flows && b.data.pin && <WhatsAppPin pin={b.data.pin} onChange={b.reload} />}
    </section>
  );
}

/** Organisations that invited the member: they join when they accept, or
 * decline. Also the whole page of someone who belongs to no organisation
 * yet (`always`), whose session becomes one in the organisation they join. */
export function Invitations({ always = false, onJoined }: { always?: boolean; onJoined?: () => void }) {
  const list = useLoad(() => get<{ invitations: Invitation[] }>("/v1/me/invitations"), []);
  const act = useAction();
  if (!always && !list.data?.invitations.length) return null;
  if (always && list.data && !list.data.invitations.length) {
    return (
      <section className="card">
        <h2 style={{ marginTop: 0 }}>Invitations</h2>
        <p>No invitations are waiting for you.</p>
      </section>
    );
  }
  return (
    <section className="card" data-testid="my-invitations">
      <h2 style={{ marginTop: 0 }}>Invitations</h2>
      <ErrorBox error={act.error} />
      <table>
        <tbody>
          {(list.data?.invitations ?? []).map((i) => (
            <tr key={i.tenant_id}>
              <td>{i.tenant}</td>
              <td>{i.roles.join(", ")}</td>
              <td>{fmtTime(i.invited_at)}</td>
              <td>
                <button
                  className="primary"
                  disabled={act.busy}
                  onClick={() => void act.run(async () => (await post(`/v1/me/invitations/${i.tenant_id}/accept`), onJoined ? onJoined() : list.reload()))}
                >
                  Accept
                </button>{" "}
                <button
                  disabled={act.busy}
                  onClick={() => confirm(`Decline the invitation from ${i.tenant}?`) && void act.run(async () => (await post(`/v1/me/invitations/${i.tenant_id}/decline`), list.reload()))}
                >
                  Decline
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
      <PageHeader title="Account" description="Your sign-in methods, invitations and linked WhatsApp number." />
      <Invitations />
      <ChangePassword />
      <Passkeys />
      <WhatsApp />
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
              onClick={() => void act.run(async () => (setPending(await post<{ secret: string; uri: string }>("/v1/me/totp", await proof(me?.factors, typed, "totp.setup"))), setTyped("")))}
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
