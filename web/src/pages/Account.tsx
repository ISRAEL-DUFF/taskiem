import { useState } from "react";
import { del, get, post, put, type Invitation } from "../api";
import { useAuth } from "../auth";
import { addPasskey, passkeysSupported, proof } from "../passkeys";
import { ErrorBox, Field, Items, PageHeader, Row, fmtTime, useAction, useLoad } from "../ui";
import { ChangePassword } from "./Password";
import { savedTheme, setTheme, type Theme } from "../theme";

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
        <Items label="Passkeys">
          {list.data.passkeys.map((k) => (
            <Row
              key={k.id}
              icon="lock"
              title={k.name}
              side={k.synced ? <span className="hint">synced</span> : undefined}
              sub={
                <span className="sub">
                  Added {fmtTime(k.created_at)} · last used {fmtTime(k.last_used_at)}
                </span>
              }
              testid="passkey"
            >
              <button
                className="danger"
                onClick={() =>
                  confirm(`Remove ${k.name}?`) &&
                  void act.run(async () => (await del(`/v1/me/passkeys/${k.id}`, await proof(me?.factors, typed, `passkey.remove/${k.id}`)), setTyped(""), list.reload(), await refresh()), "Passkey removed")
                }
              >
                Remove
              </button>
            </Row>
          ))}
        </Items>
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
              void act.run(async () => (await addPasskey(name || "Passkey", await proof(me?.factors, typed, "passkey.add")), setName(""), setTyped(""), list.reload(), await refresh(), onAdded?.()), "Passkey added")
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
          <button className="danger" disabled={act.busy} onClick={() => confirm("Remove your WhatsApp PIN?") && void act.run(async () => (await del("/v1/me/whatsapp/pin"), onChange()), "PIN removed")}>
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
            void act.run(async () => (await put("/v1/me/whatsapp/pin", { pin: value, ...(await proof(me?.factors, typed, "whatsapp.pin")) }), setValue(""), setTyped(""), onChange()), "PIN saved");
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
interface TelegramLink {
  enabled: boolean;
  bot_username?: string;
  linked: boolean;
  username?: string;
  verified_at?: string;
  pending_expires_at?: string;
}

/** Linking Telegram: a one-time t.me link opens the bot, and the Telegram
 * account that opens it is linked (docs/telegram.md). */
function Telegram() {
  const { me } = useAuth();
  const b = useLoad(() => get<TelegramLink>("/v1/me/telegram"), []);
  const [typed, setTyped] = useState("");
  const [link, setLink] = useState<string | null>(null);
  const act = useAction();
  if (!b.data?.enabled) return b.error ? <ErrorBox error={b.error} /> : null;
  const bot = b.data.bot_username;
  return (
    <section className="card" data-testid="telegram">
      <h2 style={{ marginTop: 0 }}>Telegram</h2>
      <p className="hint">
        Link your Telegram account to approve requests, check what ran and start workflows by chat
        {bot && (
          <>
            {" "}
            with <strong>@{bot}</strong>
          </>
        )}
        . Your roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.
      </p>
      <ErrorBox error={act.error} />
      {b.data.linked && (
        <div className="row">
          <p className="grow">
            Linked: <strong data-testid="telegram-account">{b.data.username ? "@" + b.data.username : "your Telegram account"}</strong>{" "}
            {b.data.verified_at && <span className="hint">since {fmtTime(b.data.verified_at)}</span>}
          </p>
          <button className="danger" disabled={act.busy} onClick={() => confirm("Unlink Telegram?") && void act.run(async () => (await del("/v1/me/telegram"), setLink(null), b.reload()), "Telegram unlinked")}>
            Unlink
          </button>
        </div>
      )}
      <form
        className="row"
        style={{ alignItems: "flex-end" }}
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            const r = await post<{ url: string }>("/v1/me/telegram", { ...(await proof(me?.factors, typed, "telegram.link")) });
            setTyped("");
            setLink(r.url);
          }, false);
        }}
      >
        <ProofField to="link Telegram" value={typed} onChange={setTyped} />
        <button type="submit" disabled={act.busy}>
          {b.data.linked ? "Link another account instead" : "Link Telegram"}
        </button>
      </form>
      {link && (
        <div className="row" style={{ alignItems: "center" }}>
          <p className="grow hint">
            Open this link on the phone or computer where you use Telegram, then press <strong>Start</strong>. It works once, for 10 minutes.
          </p>
          <a className="button primary" href={link} target="_blank" rel="noopener noreferrer" data-testid="telegram-link">
            Open Telegram
          </a>
          <button disabled={act.busy} onClick={() => void act.run(async () => (setLink(null), b.reload()), false)}>
            Done
          </button>
        </div>
      )}
    </section>
  );
}

interface SlackLink {
  enabled: boolean;
  linked: boolean;
  pending: boolean;
  workspace?: string;
  verified_at?: string;
}

/** Linking Slack in this business: Taskiem finds the member in its
 * workspace by email and sends a Confirm button there
 * (docs/integrations/slack.md#approvals-in-slack). */
function Slack() {
  const { me } = useAuth();
  const b = useLoad(() => get<SlackLink>("/v1/me/slack"), []);
  const [typed, setTyped] = useState("");
  const act = useAction();
  if (!b.data?.enabled) return b.error ? <ErrorBox error={b.error} /> : null;
  return (
    <section className="card" data-testid="slack">
      <h2 style={{ marginTop: 0 }}>Slack</h2>
      <p className="hint">
        Get the approvals you decide in this business in Slack, as a direct message from your business's Slack app with Approve and Reject buttons. Your
        roles and approval policies apply exactly as here; some approvals still ask for your passkey or authenticator code.
      </p>
      <ErrorBox error={act.error} />
      {b.data.linked && (
        <div className="row">
          <p className="grow">
            Linked in your workspace {b.data.verified_at && <span className="hint">since {fmtTime(b.data.verified_at)}</span>}
          </p>
          <button className="danger" disabled={act.busy} onClick={() => confirm("Unlink Slack?") && void act.run(async () => (await del("/v1/me/slack"), b.reload()), "Slack unlinked")}>
            Unlink
          </button>
        </div>
      )}
      {b.data.pending && (
        <p className="hint" role="status">
          Open Slack and press <strong>Confirm</strong> in the message from your business's app. Then come back here.{" "}
          <button className="link" onClick={() => b.reload()}>
            I pressed it
          </button>
        </p>
      )}
      {!b.data.linked && (
        <form
          className="row"
          style={{ alignItems: "flex-end" }}
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              await post("/v1/me/slack", { ...(await proof(me?.factors, typed, "slack.link")) });
              setTyped("");
              b.reload();
            }, false);
          }}
        >
          <ProofField to="link Slack" value={typed} onChange={setTyped} />
          <button type="submit" disabled={act.busy}>
            {b.data.pending ? "Send the Confirm again" : "Link Slack"}
          </button>
        </form>
      )}
    </section>
  );
}

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
          <button className="danger" disabled={act.busy} onClick={() => confirm("Unlink this number?") && void act.run(async () => (await del("/v1/me/whatsapp"), b.reload()), "WhatsApp unlinked")}>
            Unlink
          </button>
        </div>
      )}
      <form
        className="row"
        style={{ alignItems: "flex-end" }}
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await post("/v1/me/whatsapp", { number, ...(await proof(me?.factors, typed, "whatsapp.link")) }), setTyped(""), setSent(true), b.reload()), "Code sent");
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
            void act.run(async () => (await post("/v1/me/whatsapp/verify", { code }), setCode(""), setNumber(""), setSent(false), b.reload()), "WhatsApp linked");
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
      <Items label="Invitations">
        {(list.data?.invitations ?? []).map((i) => (
          <Row key={i.tenant_id} letter={i.tenant} title={i.tenant} sub={<span className="sub">{i.roles.join(", ")} · invited {fmtTime(i.invited_at)}</span>} testid="my-invitation">
            <button
              className="primary"
              disabled={act.busy}
              onClick={() => void act.run(async () => (await post(`/v1/me/invitations/${i.tenant_id}/accept`), onJoined ? onJoined() : list.reload()), "Joined")}
            >
              Accept
            </button>
            <button
              disabled={act.busy}
              onClick={() => confirm(`Decline the invitation from ${i.tenant}?`) && void act.run(async () => (await post(`/v1/me/invitations/${i.tenant_id}/decline`), list.reload()), "Invitation declined")}
            >
              Decline
            </button>
          </Row>
        ))}
      </Items>
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
      <PageHeader title="Account" description="Your sign-in methods, invitations, and linked WhatsApp number, Telegram account and Slack." />
      <Appearance />
      <Invitations />
      <ChangePassword />
      <Passkeys />
      <WhatsApp />
      <Telegram />
      <Slack />
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
              <button className="danger" disabled={act.busy || code.length !== 6} onClick={() => void act.run(async () => (await del("/v1/me/totp", { code }), setCode(""), await refresh()), "Authenticator removed")}>
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
              onClick={() => void act.run(async () => (setPending(await post<{ secret: string; uri: string }>("/v1/me/totp", await proof(me?.factors, typed, "totp.setup"))), setTyped("")), false)}
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
                onClick={() => void act.run(async () => (await post("/v1/me/totp/confirm", { code }), setPending(undefined), setCode(""), await refresh()), "Authenticator added")}
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

/** Light, dark, or the device's setting; kept in this browser (theme.ts). */
function Appearance() {
  const [theme, set] = useState<Theme>(savedTheme);
  const choices: [Theme, string][] = [
    ["light", "Light"],
    ["dark", "Dark"],
    ["system", "Match this device"],
  ];
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Appearance</h2>
      <div className="tabs" role="radiogroup" aria-label="Theme" style={{ marginBottom: 0 }}>
        {choices.map(([t, label]) => (
          <button
            key={t}
            type="button"
            role="radio"
            aria-checked={theme === t}
            className={theme === t ? "active" : ""}
            onClick={() => {
              setTheme(t);
              set(t);
            }}
          >
            {label}
          </button>
        ))}
      </div>
    </section>
  );
}
