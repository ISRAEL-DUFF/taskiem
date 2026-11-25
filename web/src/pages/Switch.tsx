import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ApiError, get, post, switchStep, type Business, type Lock, type Me, type ProofWays } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { businessLine, plainly } from "../lib/phone";
import { assert, passkeyProof, passkeysSupported } from "../passkeys";
import { ErrorBox, Field, Modal, PasswordInput, useAction } from "../ui";

// The switcher (docs/spec/taskiem-for-everyone.md §6, §5.4): You, then each
// of the person's businesses. Switching moves this session; a business may
// first ask for its single sign-on, a passkey, or (a new device signed in
// by a phone code) a second proof: a passkey, a link to the email, or the
// password. Never the PIN: it pays and approves only.

/** Where a switch lands: You, the workflows of a workspace, else Home. */
export function landing(to: Business | "you"): string {
  if (to === "you") return "/you";
  return to.kind === "workflows" ? "/workflows" : "/home";
}

/** A fresh page in the other side: nothing of this one stays. [dest] is
 * where to go in it instead of its start (a link opened signed out). */
function arrive(to: Business | "you", dest?: string) {
  window.location.assign(dest ?? landing(to));
}

/** What a business asked for, with what the sheet needs to offer it. */
interface Asked {
  business: Business;
  lock: Lock;
  ways: ProofWays;
}

/** Signs in with a passkey straight into the business (a business that
 * opens only with a passkey: a session made with it). */
async function passkeyInto(tenant: string) {
  await post("/v1/auth/passkey", { credential: await assert("/v1/auth/passkey/options"), tenant_id: tenant });
}

/** Switching, and the step a business asks for first; [dest] is where to
 * go once in, instead of the business's start. */
export function useSwitch(dest?: string) {
  const { me } = useAuth();
  const [asked, setAsked] = useState<Asked | null>(null);
  const act = useAction();
  const go = (to: Business | "you") =>
    void act.run(async () => {
      try {
        await post("/v1/me/switch", to === "you" ? { personal: true } : { tenant_id: to.id });
      } catch (e) {
        const lock = e instanceof ApiError ? switchStep(e.body) : null;
        if (to !== "you" && lock) {
          const ways = (e as ApiError).body.ways as ProofWays | undefined;
          setAsked({ business: to, lock, ways: ways ?? me?.second_proof?.ways ?? { passkey: false, email: false, password: false } });
          return;
        }
        throw e;
      }
      arrive(to, dest);
    }, false);
  return { go, asked, ask: setAsked, close: () => setAsked(null), busy: act.busy, error: act.error };
}

/** The list: You, then each business, the one open marked. */
export function SwitchList({ me, onPick, busy, hideYou = false }: { me: Me; onPick: (to: Business | "you") => void; busy: boolean; hideYou?: boolean }) {
  const personal = !!me.personal;
  const name = me.user?.name || "You";
  return (
    <ul className="switch-list" aria-label="Switch to">
      {!hideYou && (
      <li>
        <button type="button" className="switch-row" aria-current={personal || undefined} disabled={busy} onClick={() => !personal && onPick("you")} data-testid="switch-you">
          <span className="list-icon" aria-hidden="true">
            {name.slice(0, 1).toUpperCase()}
          </span>
          <span className="grow">
            <span className="title">You</span>
            <span className="sub">{personal ? "Open now" : "Your own chats and orders"}</span>
          </span>
          {personal && <Icon name="tick" className="switch-here" />}
        </button>
      </li>
      )}
      {(me.businesses ?? []).map((b) => (
        <li key={b.id}>
          <button
            type="button"
            className="switch-row"
            aria-current={b.current || undefined}
            disabled={busy}
            onClick={() => !b.current && onPick(b)}
            data-testid="switch-business"
          >
            <span className={b.locked ? "list-icon muted" : "list-icon"} aria-hidden="true">
              {b.kind === "workflows" ? <Icon name="workflows" /> : b.name.slice(0, 1).toUpperCase()}
            </span>
            <span className="grow">
              <span className="title">{b.name}</span>
              <span className="sub">{businessLine(b)}</span>
            </span>
            {b.current ? <Icon name="tick" className="switch-here" /> : b.locked ? <Icon name="lock" className="switch-lock" /> : null}
          </button>
        </li>
      ))}
    </ul>
  );
}

/** The switcher's button (what is open, tap to change) and its sheet. */
export function Switcher({ onStart, testid = "switcher" }: { onStart?: () => void; testid?: string }) {
  const { me } = useAuth();
  const [open, setOpen] = useState(false);
  const sw = useSwitch();
  if (!me?.user) return null;
  const here = me.personal ? "You" : me.tenant_name || "Your business";
  return (
    <>
      <button type="button" className="switcher" aria-haspopup="dialog" aria-expanded={open} onClick={() => setOpen(true)} data-testid={testid}>
        <span className="switcher-name">{here}</span>
        <Icon name="down" />
        <span className="sr-only">: switch</span>
      </button>
      {/* On the body: the bars it sits in blur what is under them, which
          would hold a fixed sheet inside the bar. */}
      {createPortal(
        <>
      {open && !sw.asked && (
        <Modal title="Switch to" onClose={() => setOpen(false)}>
          <SwitchList me={me} onPick={sw.go} busy={sw.busy} />
          <ErrorBox error={sw.error} />
          {onStart && (
            <p>
              <button
                type="button"
                onClick={() => {
                  setOpen(false);
                  onStart();
                }}
              >
                <Icon name="plus" /> Start something
              </button>
            </p>
          )}
        </Modal>
      )}
      {sw.asked && (
        <SwitchStep
          asked={sw.asked}
          onClose={() => {
            sw.close();
            setOpen(false);
          }}
        />
      )}
        </>,
        document.body,
      )}
    </>
  );
}

/** What a business asks for before it opens, as a sheet; [dest] is where
 * to go once it has. */
export function SwitchStep({ asked, onClose, dest }: { asked: Asked; onClose: () => void; dest?: string }) {
  const { business, lock, ways } = asked;
  if (lock === "sso_required") return <SSOStep business={business} onClose={onClose} dest={dest} />;
  if (lock === "passkey_required") return <PasskeyStep business={business} onClose={onClose} dest={dest} />;
  return <ProofStep business={business} ways={ways} onClose={onClose} dest={dest} />;
}

function SSOStep({ business, onClose, dest }: { business: Business; onClose: () => void; dest?: string }) {
  const { me, logout } = useAuth();
  const [none, setNone] = useState(false);
  const act = useAction();
  return (
    <Modal title={`Open ${business.name}`} onClose={onClose}>
      <p>{business.name} asks you to sign in with your organisation's sign-in.</p>
      {none ? (
        <>
          <p>Sign out, then sign in to {business.name} with your organisation's email.</p>
          <button className="primary" onClick={() => void logout()}>
            Sign out
          </button>
        </>
      ) : (
        <button
          className="primary"
          disabled={act.busy}
          onClick={() =>
            void act.run(async () => {
              const r = me?.user?.email_placeholder
                ? { sso: false }
                : await post<{ sso: boolean; start?: string }>("/v1/auth/sso/discover", { email: me?.user?.email ?? "" });
              if (r.sso && "start" in r && r.start) window.location.assign(`${r.start}?return_to=${encodeURIComponent(dest ?? landing(business))}`);
              else setNone(true);
            }, false)
          }
        >
          Continue with your organisation's sign-in
        </button>
      )}
      <ErrorBox error={act.error} />
      <p>
        <button type="button" onClick={onClose}>
          Not now
        </button>
      </p>
    </Modal>
  );
}

function PasskeyStep({ business, onClose, dest }: { business: Business; onClose: () => void; dest?: string }) {
  const act = useAction();
  return (
    <Modal title={`Open ${business.name}`} onClose={onClose}>
      <p>{business.name} opens with your passkey: your device's fingerprint, face or PIN.</p>
      <ErrorBox error={act.error} />
      <button
        className="primary"
        disabled={act.busy || !passkeysSupported()}
        onClick={() =>
          void act.run(async () => {
            await passkeyInto(business.id);
            arrive(business, dest);
          }, false)
        }
      >
        Use your passkey
      </button>
      {!passkeysSupported() && <p className="hint">This browser cannot use passkeys. Open Taskiem on a device with your passkey.</p>}
      <p>
        <button type="button" onClick={onClose}>
          Not now
        </button>
      </p>
    </Modal>
  );
}

/** A second proof for a new device: whichever ways the account has. Each
 * proves this device and leaves the session where it is; then the business
 * opens (or onProved says what next). */
export function ProofStep({
  business,
  ways,
  onClose,
  onProved,
  dest,
}: {
  business?: Business;
  ways: ProofWays;
  onClose: () => void;
  onProved?: () => void | Promise<void>;
  dest?: string;
}) {
  const [sentTo, setSentTo] = useState<string | null>(null);
  const [password, setPassword] = useState("");
  const act = useAction();
  const done = useRef(false);
  const proved = async () => {
    if (done.current) return;
    done.current = true;
    try {
      if (onProved) return await onProved();
      if (!business) return onClose();
      await post("/v1/me/switch", { tenant_id: business.id });
      arrive(business, dest);
    } catch (e) {
      done.current = false;
      throw e;
    }
  };
  // The link opened in another tab proves this device: back here, the tab
  // asks whether it has (as the app does on coming back to it).
  const opened = async (quiet: boolean) => {
    const m = await get<Me>("/v1/me");
    if (m.second_proof?.device_proved) return proved();
    if (!quiet) throw new Error("The link has not been opened yet. Open it from the email, then try again.");
  };
  useEffect(() => {
    if (!sentTo) return;
    const back = () => {
      if (document.visibilityState === "visible" && !done.current) void opened(true).catch(() => undefined);
    };
    window.addEventListener("focus", back);
    document.addEventListener("visibilitychange", back);
    return () => {
      window.removeEventListener("focus", back);
      document.removeEventListener("visibilitychange", back);
    };
  }, [sentTo]);
  const what = business ? `Before ${business.name} opens` : "First";
  // A passkey this browser cannot use is no way here.
  const passkey = ways.passkey && passkeysSupported();
  const none = !passkey && !ways.email && !ways.password;
  return (
    <Modal title="Prove it's you" onClose={onClose}>
      <div data-testid="second-proof">
        <p>
          This is a new device for your account. {what}, prove it's you another way. This keeps your businesses safe if your number ever
          goes to someone else.
        </p>
        {none && (
          <p data-testid="no-way-here">
            {ways.passkey
              ? "Your passkey can't be used in this browser. Sign in on your old phone, or on a device with your passkey, and add an email or a password there."
              : "You have no other way to prove it's you on this device. Sign in on your old phone and add a way there: an email, a password or a passkey."}
          </p>
        )}
        {passkey && (
          <p>
            <button
              type="button"
              className="primary"
              disabled={act.busy}
              onClick={() =>
                void act.run(async () => {
                  await plainly(passkeyProof);
                  await proved();
                }, false)
              }
            >
              Use your passkey
            </button>
          </p>
        )}
        {ways.email &&
          (sentTo ? (
            <>
              <p role="status" className="notice" data-testid="proof-link-sent">
                We sent a link to {sentTo}. Open it on this device within 15 minutes.
              </p>
              <p>
                <button type="button" className="primary" disabled={act.busy} onClick={() => void act.run(() => opened(false), false)}>
                  I've opened the link
                </button>
              </p>
            </>
          ) : (
            <p>
              <button
                type="button"
                disabled={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    const r = await plainly(() => post<{ sent_to: string }>("/v1/auth/second-proof/email/start"));
                    setSentTo(r.sent_to);
                  }, false)
                }
              >
                <Icon name="mail" /> Email me a link
              </button>
            </p>
          ))}
        {ways.password && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                try {
                  await post("/v1/auth/second-proof/password", { password });
                } catch (err) {
                  if (err instanceof ApiError && err.status === 401) throw new Error("That password is not right.", { cause: err });
                  throw err;
                }
                await proved();
              }, false);
            }}
          >
            <Field label="Your password">
              <PasswordInput autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </Field>
            <button type="submit" disabled={act.busy || !password}>
              Confirm with my password
            </button>
          </form>
        )}
        <ErrorBox error={act.error} />
        <p>
          <button type="button" onClick={onClose}>
            Not now
          </button>
        </p>
      </div>
    </Modal>
  );
}
