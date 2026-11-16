import { useState } from "react";
import { createPortal } from "react-dom";
import { ApiError, post, switchStep, type Business, type Lock, type Me, type ProofWays } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { businessLine } from "../lib/phone";
import { assert, passkeysSupported } from "../passkeys";
import { ErrorBox, Field, Modal, PasswordInput, useAction } from "../ui";

// The switcher (docs/spec/taskiem-for-everyone.md §6, §5.4): You, then each
// of the person's businesses. Switching moves this session; a business may
// first ask for its single sign-on, a passkey, or (a new device signed in
// by a phone code) a second proof: a passkey, a link to the email, or the
// chat PIN.

/** Where a switch lands: You, the workflows of a workspace, else Home. */
export function landing(to: Business | "you"): string {
  if (to === "you") return "/you";
  return to.kind === "workflows" ? "/workflows" : "/home";
}

/** A fresh page in the other side: nothing of this one stays. */
function arrive(to: Business | "you") {
  window.location.assign(landing(to));
}

/** What a business asked for, with what the sheet needs to offer it. */
interface Asked {
  business: Business;
  lock: Lock;
  ways: ProofWays;
}

/** Signs in with a passkey straight into the business (it proves this device too). */
async function passkeyInto(tenant: string) {
  await post("/v1/auth/passkey", { credential: await assert("/v1/auth/passkey/options"), tenant_id: tenant });
}

/** Switching, and the step a business asks for first. */
export function useSwitch() {
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
          setAsked({ business: to, lock, ways: ways ?? me?.second_proof?.ways ?? { passkey: false, email: false, pin: false } });
          return;
        }
        throw e;
      }
      arrive(to);
    }, false);
  return { go, asked, close: () => setAsked(null), busy: act.busy, error: act.error };
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
      <button type="button" className="switcher" aria-haspopup="dialog" onClick={() => setOpen(true)} data-testid={testid}>
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

/** What a business asks for before it opens, as a sheet. */
export function SwitchStep({ asked, onClose }: { asked: Asked; onClose: () => void }) {
  const { business, lock, ways } = asked;
  if (lock === "sso_required") return <SSOStep business={business} onClose={onClose} />;
  if (lock === "passkey_required") return <PasskeyStep business={business} onClose={onClose} />;
  return <ProofStep business={business} ways={ways} onClose={onClose} />;
}

function SSOStep({ business, onClose }: { business: Business; onClose: () => void }) {
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
              if (r.sso && "start" in r && r.start) window.location.assign(`${r.start}?return_to=${encodeURIComponent("/home")}`);
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

function PasskeyStep({ business, onClose }: { business: Business; onClose: () => void }) {
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
            arrive(business);
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

/** A second proof for a new device: whichever ways the account has. */
export function ProofStep({ business, ways, onClose, onProved }: { business?: Business; ways: ProofWays; onClose: () => void; onProved?: () => void }) {
  const [sentTo, setSentTo] = useState<string | null>(null);
  const [pin, setPin] = useState("");
  const act = useAction();
  const proved = async () => {
    if (onProved) return onProved();
    if (!business) return onClose();
    await post("/v1/me/switch", { tenant_id: business.id });
    arrive(business);
  };
  const what = business ? `Before ${business.name} opens` : "First";
  const none = !ways.passkey && !ways.email && !ways.pin;
  return (
    <Modal title="Prove it's you" onClose={onClose}>
      <div data-testid="second-proof">
        <p>
          This is a new device for your account. {what}, prove it's you another way. This keeps your businesses safe if your number ever
          goes to someone else.
        </p>
        {none && <p>Your account has no other way to prove it's you yet. Add an email or a passkey from a device you have used before.</p>}
        {ways.passkey && passkeysSupported() && (
          <p>
            <button
              type="button"
              className="primary"
              disabled={act.busy}
              onClick={() =>
                void act.run(async () => {
                  if (business) {
                    await passkeyInto(business.id);
                    arrive(business);
                  } else {
                    await post("/v1/auth/passkey", { credential: await assert("/v1/auth/passkey/options") });
                    await proved();
                  }
                }, false)
              }
            >
              Use your passkey
            </button>
          </p>
        )}
        {ways.email &&
          (sentTo ? (
            <p role="status" className="notice" data-testid="proof-link-sent">
              We sent a link to {sentTo}. Open it on this device within 15 minutes.
            </p>
          ) : (
            <p>
              <button
                type="button"
                disabled={act.busy}
                onClick={() =>
                  void act.run(async () => {
                    const r = await post<{ sent_to: string }>("/v1/auth/second-proof/email/start");
                    setSentTo(r.sent_to);
                  }, false)
                }
              >
                <Icon name="mail" /> Email me a link
              </button>
            </p>
          ))}
        {ways.pin && (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void act.run(async () => {
                await post("/v1/auth/second-proof/pin", { pin });
                await proved();
              }, false);
            }}
          >
            <Field label="Your chat PIN" hint="The PIN you set for approving on WhatsApp.">
              <PasswordInput inputMode="numeric" autoComplete="off" value={pin} onChange={(e) => setPin(e.target.value.replace(/\D/g, ""))} maxLength={8} required />
            </Field>
            <button type="submit" disabled={act.busy || pin.length < 4}>
              Confirm with my PIN
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
