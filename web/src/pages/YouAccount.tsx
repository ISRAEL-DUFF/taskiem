import { KEPT_WORDS } from "../lib/people";
import { useEffect, useRef, useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { ApiError, del, get, post, put, putForm, type ProofWays } from "../api";
import { useAuth } from "../auth";
import { dateLabel } from "../lib/invoices";
import { showPhone } from "../lib/phone";
import { browserEnv, enableYouPush, findYouDevice, previewWords, pushSupport, serverKey, setYouPreview, type PushDevice } from "../lib/push";
import {
  accountDeletedPath,
  deleteBlocked,
  deleteBody,
  deletedWords,
  deleteProblem,
  deleteProofs,
  holdersWords,
  NOTIFY_KINDS,
  NOTIFY_OFF_WORDS,
  photoFileProblem,
  photoProblem,
  privacyLine,
  toldOf,
  type DeleteProof,
  type YouNotifications,
  type YouPrivacy,
} from "../lib/youaccount";
import { assert, passkeysSupported } from "../passkeys";
import { ErrorBox, Field, Items, PageHeader, PasswordInput, Row, Skeleton, useAction, useLoad } from "../ui";
import { ChatAvatar } from "./ChatAvatar";
import { ProofStep } from "./Switch";
import { CodeField, useCountdown } from "./WayIn";

// You's own settings (docs/spec/taskiem-for-everyone.md §7, §14): the
// person's photo (theirs alone: businesses never see it), which pushes
// they get from businesses, the privacy page (what businesses see, which
// businesses have their number, how to ask one to erase it) and deleting
// the account.

/** The person's photo, at the top of Your account. */
export function YouPhoto() {
  const { me, refresh } = useAuth();
  const file = useRef<HTMLInputElement>(null);
  const [problem, setProblem] = useState<string>();
  const act = useAction();
  const photo = me?.user?.photo ?? null;
  const name = me?.user?.name || "You";
  const pick = (f: File | undefined) => {
    if (!f) return;
    const wrong = photoFileProblem(f);
    setProblem(wrong ?? undefined);
    if (wrong) return;
    const form = new FormData();
    form.append("file", f);
    void act.run(async () => {
      await putForm("/v1/me/photo", form);
      await refresh();
    }, photo ? "Photo changed" : "Photo added");
  };
  const said = problem ?? photoProblem(act.error);
  return (
    <div className="setting-line you-photo-line" data-testid="you-photo">
      <ChatAvatar name={name} logo={photo?.url} className="chat-link-avatar" />
      <div className="grow">
        <strong>Photo</strong>
        <div className="sub">{photo ? "Only you see it." : "No photo yet."} Businesses you chat with or order from never see your photo: they see your name and phone number.</div>
        <div className="row-actions">
          <label className="button small you-photo-pick">
            <input
              ref={file}
              type="file"
              accept="image/jpeg,image/png,image/webp"
              className="sr-only"
              disabled={act.busy}
              onChange={(e) => {
                pick(e.target.files?.[0]);
                e.target.value = "";
              }}
              data-testid="you-photo-file"
            />
            {act.busy ? "Saving…" : photo ? "Change photo" : "Add a photo"}
          </label>
          {photo && (
            <button
              type="button"
              className="small danger"
              disabled={act.busy}
              onClick={() =>
                void act.run(async () => {
                  await del("/v1/me/photo");
                  await refresh();
                }, "Photo removed")
              }
              data-testid="you-photo-remove"
            >
              Remove photo
            </button>
          )}
        </div>
        {said ? (
          <p className="error" role="alert" data-testid="you-photo-problem">
            {said}
          </p>
        ) : (
          <ErrorBox error={act.error} />
        )}
      </div>
    </div>
  );
}

/** Which pushes the person gets from businesses and people: a switch for each kind. */
export function YouNotificationsCard() {
  const view = useLoad(() => get<YouNotifications>("/v1/me/notifications"), []);
  const [now, setNow] = useState<YouNotifications>();
  const act = useAction();
  const n = now ?? view.data;
  const turn = (key: keyof YouNotifications, on: boolean) => {
    if (!n) return;
    const was = n;
    setNow({ ...n, [key]: on });
    void act
      .run(async () => setNow(await put<YouNotifications>("/v1/me/notifications", { [key]: on })), false)
      .then((ok) => ok || setNow(was));
  };
  return (
    <section className="card" aria-labelledby="you-notify" data-testid="you-notifications">
      <h2 id="you-notify">Notifications</h2>
      <p className="hint">What businesses and people may notify you about on your phone.</p>
      {!n && !view.error && <Skeleton rows={1} label="Loading your notifications…" />}
      <ErrorBox error={view.error ?? act.error} />
      {n &&
        NOTIFY_KINDS.map((k) => (
          <label key={k.key} className="switch-line you-notify-line">
            <input type="checkbox" role="switch" checked={!!n[k.key]} disabled={act.busy} onChange={(e) => turn(k.key, e.target.checked)} data-testid={`you-notify-${k.key}`} />{" "}
            <span>
              {k.label}
              <span className="hint block">{k.hint}</span>
            </span>
          </label>
        ))}
      {n && <p className="hint">{NOTIFY_OFF_WORDS}</p>}
      <YouBrowserPush />
    </section>
  );
}

/**
 * This browser as one of the person's own devices (people-chatting §6.2):
 * notifications on it, and whether they show what a message says
 * (PUT /v1/me/push `preview`, PATCH /v1/me/push/{id}). Offered only where
 * the browser and the server can push.
 */
export function YouBrowserPush() {
  const [st, setSt] = useState<{ k: "look" | "none" | "off" | "busy" | "denied" | "failed" } | { k: "on"; device: PushDevice }>({ k: "look" });
  const [saving, setSaving] = useState(false);
  const [problem, setProblem] = useState<string>();
  useEffect(() => {
    let live = true;
    void (async () => {
      if (pushSupport(browserEnv()) !== "yes" || !(await serverKey())) return live && setSt({ k: "none" });
      const device = await findYouDevice();
      if (live) setSt(device ? { k: "on", device } : { k: "off" });
    })();
    return () => {
      live = false;
    };
  }, []);
  if (st.k === "look" || st.k === "none") return null;
  const turnOn = () => {
    setSt({ k: "busy" });
    void enableYouPush(true).then(async (r) => {
      if (!r.ok) return setSt({ k: r.why === "denied" ? "denied" : "failed" });
      const device = await findYouDevice();
      setSt(device ? { k: "on", device } : { k: "failed" });
    });
  };
  const preview = (on: boolean) => {
    if (st.k !== "on") return;
    const was = st.device;
    setProblem(undefined);
    setSaving(true);
    setSt({ k: "on", device: { ...was, preview: on } });
    setYouPreview(was.id, on)
      .then(
        (d) => setSt({ k: "on", device: d ?? { ...was, preview: on } }),
        () => {
          setSt({ k: "on", device: was });
          setProblem("That did not save. Check your connection and try again.");
        },
      )
      .finally(() => setSaving(false));
  };
  return (
    <div className="you-browser-push" data-testid="you-browser-push">
      <h3>On this browser</h3>
      {st.k === "on" ? (
        <>
          <p className="hint">Notifications come to this browser when Taskiem is closed.</p>
          <label className="switch-line you-notify-line">
            <input type="checkbox" role="switch" checked={st.device.preview} disabled={saving} onChange={(e) => preview(e.target.checked)} data-testid="you-push-preview" />{" "}
            <span>
              Show what the message says
              <span className="hint block" data-testid="you-push-preview-words">
                {previewWords(st.device.preview)}
              </span>
            </span>
          </label>
          {problem && (
            <p className="error" role="alert">
              {problem}
            </p>
          )}
        </>
      ) : (
        <>
          <p className="hint">
            {st.k === "denied"
              ? "Notifications are blocked in this browser. Allow them for Taskiem in your browser's settings, then try again."
              : st.k === "failed"
                ? "That did not work. Try again."
                : "Get a notification here when someone writes to you, even with Taskiem closed."}
          </p>
          <button type="button" disabled={st.k === "busy"} onClick={turnOn} data-testid="you-push-on">
            {st.k === "busy" ? "Turning on…" : "Notify me on this browser"}
          </button>
        </>
      )}
    </div>
  );
}

/** Privacy and deleting the account, as rows on Settings. */
export function YouPrivacyLinks() {
  return (
    <section className="card" aria-labelledby="you-privacy-links">
      <h2 id="you-privacy-links">Privacy and your account</h2>
      <div className="setting-line">
        <div className="grow">
          <strong>Privacy</strong>
          <div className="sub">What businesses see of you, which have your number, and how to ask one to erase it.</div>
        </div>
        <Link className="button" to="/you/settings/privacy" data-testid="you-privacy-open">
          See privacy
        </Link>
      </div>
      <div className="setting-line">
        <div className="grow">
          <strong>Delete your account</strong>
          <div className="sub">Your chats, saved addresses, photo and settings go, for good.</div>
        </div>
        <Link className="button you-danger-link" to="/you/settings/delete" data-testid="you-delete-open">
          Delete account
        </Link>
      </div>
    </section>
  );
}

/** /you/settings/privacy. */
export function YouPrivacyPage() {
  const view = useLoad(() => get<YouPrivacy>("/v1/me/privacy"), []);
  const p = view.data;
  return (
    <>
      <p className="crumbs">
        <Link to="/you/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Privacy" description="What businesses on Taskiem see of you, and how to ask one to erase it." />
      {!p && !view.error && <Skeleton rows={3} label="Loading your privacy page…" />}
      <ErrorBox error={view.error} />
      {p && (
        <>
          <section className="card" aria-labelledby="privacy-shown" data-testid="privacy-shown">
            <h2 id="privacy-shown">What a business sees</h2>
            <p className="hint">A business you chat with or order from sees:</p>
            <Items label="What a business sees">
              <Row icon="members" title="Your name" sub={<span className="sub">{p.shown_to_businesses.name || "No name yet"}</span>} testid="privacy-name" />
              <Row icon="bell" title="Your phone number" sub={<span className="sub">{p.shown_to_businesses.phone ? showPhone(p.shown_to_businesses.phone) : "No phone yet"}</span>} testid="privacy-phone" />
            </Items>
            <p className="hint" data-testid="privacy-photo">
              {p.shown_to_businesses.photo ? "Your photo is shown to them." : "Not your photo: only you see it."} With an order, they also see the address you gave for it.
            </p>
          </section>
          <section className="card" aria-labelledby="privacy-holders" data-testid="privacy-businesses">
            <h2 id="privacy-holders">Businesses that have your number</h2>
            <p className="hint">{holdersWords(p.businesses.length)}</p>
            {p.businesses.length > 0 && (
              <Items label="Businesses that have your number">
                {p.businesses.map((b) => (
                  <Row key={b.business_id} letter={b.business} title={b.business} sub={<span className="sub">{privacyLine(b, dateLabel)}</span>} testid="privacy-business">
                    {b.chat_id ? (
                      <Link className="button small" to={`/you/chats/${encodeURIComponent(b.chat_id)}`}>
                        Ask in the chat<span className="sr-only"> with {b.business}</span>
                      </Link>
                    ) : (
                      <Link className="button small" to={`/you/chats/new?business=${encodeURIComponent(b.business_id)}`}>
                        Start a chat<span className="sr-only"> with {b.business}</span>
                      </Link>
                    )}
                  </Row>
                ))}
              </Items>
            )}
          </section>
          <section className="card" aria-labelledby="privacy-erase" data-testid="privacy-erase">
            <h2 id="privacy-erase">Asking a business to erase your details</h2>
            <p>{p.erase.words}</p>
            {p.erase.how === "ask_in_chat" && (
              <p className="hint">
                Open the business's chat above and ask it there. If it does not answer, use Report in the chat. Deleting your account does not erase what businesses keep.
              </p>
            )}
          </section>
          <section className="card" aria-labelledby="privacy-people" data-testid="privacy-people-chats">
            <h2 id="privacy-people">Chats with people</h2>
            <p>{KEPT_WORDS}</p>
            <p className="hint">
              <Link to="/transparency" data-testid="privacy-transparency">
                How many requests from the authorities Taskiem received
              </Link>
            </p>
            <p className="hint">
              <Link to="/you/settings/standing" data-testid="privacy-standing">
                Your standing: what Taskiem's moderators decided about you, and how to appeal
              </Link>
            </p>
            <p className="hint">
              <Link to="/you/settings/people" data-testid="privacy-people-settings">
                Who can find you, message you, and see when you were last here
              </Link>
            </p>
          </section>
        </>
      )}
    </>
  );
}

/** An action whose refusal the page itself shows where it happened. */
function useOnPage() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const run = async (fn: () => Promise<unknown>, _success: false) => {
    void _success;
    setBusy(true);
    setError(undefined);
    try {
      await fn();
      return true;
    } catch (e) {
      setError(e);
      return false;
    } finally {
      setBusy(false);
    }
  };
  return { busy, error, run, clear: () => setError(undefined) };
}

/** /you/settings/delete: deleting the account, with a fresh proof. */
export function DeleteAccountPage() {
  const { me, gone } = useAuth();
  const ways = deleteProofs(me?.user?.phone, me?.factors, passkeysSupported());
  const [way, setWay] = useState<DeleteProof | undefined>(ways[0]);
  const [sent, setSent] = useState<{ sent_via: string }>();
  const [code, setCode] = useState("");
  const [password, setPassword] = useState("");
  const [sure, setSure] = useState(false);
  const [proving, setProving] = useState<ProofWays>();
  const [proved, setProved] = useState(false);
  const [left, wait] = useCountdown();
  // Every refusal is said on this page, beside its button (no toast over it).
  const act = useOnPage();
  const blocked = deleteBlocked(act.error);
  const said = deleteProblem(act.error);
  const phone = me?.user?.phone ?? "";

  const sendCode = (via?: "whatsapp" | "sms") =>
    void act.run(async () => {
      const r = await post<{ sent_via: string }>("/v1/me/delete/start", via === "sms" ? { via } : {});
      setSent(r);
      setCode("");
      wait(60);
    }, false);

  const remove = () =>
    void act.run(async () => {
      let credential: unknown;
      if (way === "passkey") credential = await assert("/v1/me/delete/passkey/options");
      try {
        const r = await post<{ deleted: boolean; businesses_told: number }>(
          "/v1/me/delete",
          deleteBody({ code: way === "code" ? code : undefined, password: way === "password" ? password : undefined, credential }),
        );
        // Gone: this tab forgets the person, and the page that says so is
        // loaded afresh (signed out, nothing of the account left in memory).
        gone();
        window.location.replace(accountDeletedPath(r.businesses_told));
      } catch (e) {
        // A new device first proves itself another way (§5.4), then deletes.
        if (e instanceof ApiError && e.body.second_proof_required) {
          setProving((e.body.ways as ProofWays | undefined) ?? me?.second_proof?.ways ?? { passkey: false, email: false, password: false });
          return;
        }
        throw e;
      }
    }, false);

  const ready = sure && !!way && (way === "code" ? code.length === 6 : way === "password" ? password.length > 0 : true);
  return (
    <>
      <p className="crumbs">
        <Link to="/you/settings" aria-label="Back to Settings">
          Settings
        </Link>
      </p>
      <PageHeader title="Delete your account" />
      <section className="card" aria-labelledby="delete-what" data-testid="delete-what">
        <h2 id="delete-what">What happens</h2>
        <ul className="you-delete-list">
          <li>Your chats, saved addresses, photo and settings are deleted, and you are signed out everywhere.</li>
          <li>Each business you chat with is told only “This customer left Taskiem”, and can't message you any more.</li>
          <li>Businesses keep their own records of your orders and invoices. To ask one to erase them, ask it in its chat first: see <Link to="/you/settings/privacy">Privacy</Link>.</li>
          <li>It can't be undone. To use Taskiem again you start a new account.</li>
        </ul>
      </section>
      {blocked ? (
        <section className="card" aria-labelledby="delete-blocked" data-testid="delete-blocked">
          <h2 id="delete-blocked">{blocked.title}</h2>
          <p>{blocked.line}</p>
          <Items label="Your businesses">
            {blocked.businesses.map((b) => (
              <Row key={b} letter={b} title={b} testid="delete-blocked-business" />
            ))}
          </Items>
          <ol className="you-delete-steps">
            {blocked.steps.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ol>
          <button type="button" onClick={act.clear}>
            I've done this, try again
          </button>
        </section>
      ) : (
        <section className="card" aria-labelledby="delete-proof" data-testid="delete-proof">
          <h2 id="delete-proof">Confirm it's you</h2>
          {proved && (
            <p className="notice" role="status">
              Done: this device is proved. Now delete your account.
            </p>
          )}
          {ways.length === 0 && <p>Your account has no way to prove it's you here. Add a phone number in Settings first.</p>}
          {ways.length > 1 && (
            <fieldset className="pc-choice">
              <legend>How do you want to prove it?</legend>
              {ways.map((w) => (
                <label key={w} className="switch-line">
                  <input type="radio" name="delete-proof" checked={way === w} onChange={() => (setWay(w), act.clear())} />{" "}
                  <span>{w === "code" ? `A code to ${showPhone(phone)}` : w === "passkey" ? "Your passkey" : "Your password"}</span>
                </label>
              ))}
            </fieldset>
          )}
          {way === "code" &&
            (sent ? (
              <>
                <p role="status">{sent.sent_via === "sms" ? `We sent a 6-digit code by SMS to ${showPhone(phone)}.` : `We sent a 6-digit code to WhatsApp on ${showPhone(phone)}.`}</p>
                <CodeField label="Code to delete your account" code={code} setCode={setCode} left={left} busy={act.busy} sms={sent.sent_via !== "sms"} onResend={sendCode} />
              </>
            ) : (
              <p>
                <button type="button" disabled={act.busy} onClick={() => sendCode()} data-testid="delete-send-code">
                  Send me a code
                </button>
              </p>
            ))}
          {way === "password" && (
            <Field label="Your password">
              <PasswordInput autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            </Field>
          )}
          {way === "passkey" && <p className="hint">Your browser asks for your passkey when you press Delete.</p>}
          <label className="switch-line">
            <input type="checkbox" checked={sure} onChange={(e) => setSure(e.target.checked)} data-testid="delete-sure" />{" "}
            <span>I understand my account is deleted for good</span>
          </label>
          {said ? (
            <p className="error" role="alert" data-testid="delete-problem">
              {said}
            </p>
          ) : (
            <ErrorBox error={act.error} />
          )}
          <div className="row-actions">
            <button type="button" className="danger" disabled={!ready || act.busy} onClick={remove} data-testid="delete-account">
              {act.busy ? "Deleting…" : "Delete my account"}
            </button>
            <Link className="button" to="/you/settings">
              Keep my account
            </Link>
          </div>
        </section>
      )}
      {proving && (
        <ProofStep
          ways={proving}
          onClose={() => setProving(undefined)}
          onProved={() => {
            setProving(undefined);
            setProved(true);
          }}
        />
      )}
    </>
  );
}

/** /account-deleted: the account is gone (signed out by then). */
export function AccountDeleted() {
  const loc = useLocation();
  const told = toldOf(loc.search);
  return (
    <div className="login card" data-testid="account-deleted">
      <h1>Your account is deleted</h1>
      <p role="status">{told === undefined ? "Your account is deleted, and you are signed out everywhere." : deletedWords(told)}</p>
      <p className="hint">Thank you for using Taskiem. You can start again with a new account at any time.</p>
      <Link className="button primary" to="/login">
        Back to Taskiem
      </Link>
    </div>
  );
}
