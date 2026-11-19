import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, type Business, type Me, type TermsAsked } from "../api";
import { afterPhone, codeDigits, codeProblem, countdown, NOT_A_PHONE, plainly, sentWords, signedInTo, toE164 } from "../lib/phone";
import { safeNext } from "../lib/safePath";
import { ErrorBox, Field, useAction } from "../ui";
import { SwitchList, SwitchStep, useSwitch } from "./Switch";

// The way in (docs/spec/taskiem-for-everyone.md §5.1): one screen for
// signing in and making an account. A phone number, the 6-digit code sent
// to it, and for a new number a name and the two agreements. Email and
// password stay a link away.

/** Seconds before a new code can be asked for. */
const RESEND_AFTER = 60;

export { plainly };

export { codeDigits, signedInTo };

/** Ticks down to zero from when it was last set, once a second. */
export function useCountdown(): [number, (s: number) => void] {
  const [until, setUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (until <= now) return;
    const t = window.setTimeout(() => setNow(Date.now()), 1000);
    return () => window.clearTimeout(t);
  }, [until, now]);
  return [Math.max(0, (until - now) / 1000), (s) => (setNow(Date.now()), setUntil(Date.now() + s * 1000))];
}

/** The code's field, its new-code timer and Send by SMS instead. */
export function CodeField({
  code,
  setCode,
  left,
  busy,
  onResend,
  label = "6-digit code",
  sms = true,
}: {
  code: string;
  setCode: (c: string) => void;
  left: number;
  busy: boolean;
  onResend: (via: "whatsapp" | "sms") => void;
  label?: string;
  sms?: boolean;
}) {
  return (
    <>
      <Field label={label}>
        <input
          inputMode="numeric"
          autoComplete="one-time-code"
          value={code}
          onChange={(e) => setCode(codeDigits(e.target.value))}
          required
          autoFocus
        />
      </Field>
      <p className="hint way-in-again">
        {left > 0 ? (
          <span>You can ask for a new code in {countdown(left)}.</span>
        ) : (
          <button type="button" className="link" disabled={busy} onClick={() => onResend("whatsapp")}>
            Send a new code
          </button>
        )}
        {sms && (
          <button type="button" className="link" disabled={busy} onClick={() => onResend("sms")}>
            Send by SMS instead
          </button>
        )}
      </p>
    </>
  );
}

/** After a phone sign-in, a business's page asked for (?next=): its
 * business switched to when the person has one, else chosen here. */
function ChooseBusiness({ me, next }: { me: Me; next: string }) {
  const sw = useSwitch(next);
  return (
    <div className="login card way-in" data-testid="choose-business">
      <h1>Choose the business</h1>
      <p className="way-in-line">Choose the business to open this in.</p>
      <SwitchList me={me} onPick={sw.go} busy={sw.busy} hideYou />
      <ErrorBox error={sw.error} />
      {sw.asked && <SwitchStep asked={sw.asked} onClose={sw.close} dest={next} />}
      <p className="hint way-in-other">
        <a href="/you">Go to You instead</a>
      </p>
    </div>
  );
}

/** The one business a link opened signed out belongs to, switched to. */
function OpenIn({ me, business, next }: { me: Me; business: Business; next: string }) {
  const sw = useSwitch(next);
  const [tried, setTried] = useState(false);
  useEffect(() => {
    if (tried) return;
    setTried(true);
    sw.go(business);
  }, [tried]);
  return (
    <div className="login card way-in" data-testid="open-in">
      <h1>Opening {business.name}</h1>
      <ErrorBox error={sw.error} />
      {sw.asked && <SwitchStep asked={sw.asked} onClose={() => window.location.assign("/you")} dest={next} />}
      {(sw.error || sw.asked) && (
        <p className="hint way-in-other">
          <a href="/you">Go to You instead</a>
        </p>
      )}
      {!sw.error && !sw.asked && <p className="way-in-line">One moment…</p>}
      {!!sw.error && <SwitchList me={me} onPick={sw.go} busy={sw.busy} hideYou />}
    </div>
  );
}

export function WayIn() {
  const [step, setStep] = useState<"phone" | "code" | "new" | "business">("phone");
  const [typed, setTyped] = useState("");
  const [phone, setPhone] = useState("");
  const [via, setVia] = useState("whatsapp");
  const [code, setCode] = useState("");
  const [ticket, setTicket] = useState("");
  const [name, setName] = useState("");
  const [adult, setAdult] = useState(false);
  const [agree, setAgree] = useState(false);
  const [terms, setTerms] = useState<TermsAsked | undefined>();
  const [slack, setSlack] = useState<string | undefined>();
  const [note, setNote] = useState<string | null>(null);
  const [signedIn, setSignedIn] = useState<Me | null>(null);
  // Signed in: the button stays off until the page has moved on.
  const [done, setDone] = useState(false);
  const [left, wait] = useCountdown();
  const act = useAction();
  // A second press before the page has drawn the button off sends nothing.
  const sending = useRef(false);
  const once = (fn: () => Promise<unknown>) => {
    if (sending.current) return;
    sending.current = true;
    void act.run(async () => {
      try {
        await fn();
      } finally {
        sending.current = false;
      }
    }, false);
  };
  useEffect(() => {
    get<{ terms?: TermsAsked; slack?: string }>("/v1/signup").then(
      (o) => {
        setTerms(o.terms);
        setSlack(o.slack);
      },
      () => undefined,
    );
  }, []);
  const q = new URLSearchParams(window.location.search);
  const nextParam = q.get("next");
  const next = safeNext(nextParam, "");
  const start = q.get("start");
  // Came through a business's chat link (§8.2, §9): the code goes with
  // the phone, so the business pays for it, and its name is said.
  const chatCode = q.get("chat") ?? "";
  const [chatWith, setChatWith] = useState<string>();
  useEffect(() => {
    if (!chatCode) return;
    get<{ name: string }>(`/v1/chat-links/${encodeURIComponent(chatCode)}`).then(
      (c) => setChatWith(c.name),
      () => undefined,
    );
  }, [chatCode]);
  const emailLink = `/login?with=email${nextParam ? `&next=${encodeURIComponent(nextParam)}` : ""}`;

  const send = (number: string, how?: "whatsapp" | "sms") =>
    plainly(async () => {
      const r = await post<{ sent_via: string }>("/v1/auth/phone/start", {
        phone: number,
        ...(how === "sms" ? { via: "sms" } : {}),
        ...(chatCode ? { chat_code: chatCode } : {}),
      });
      setVia(r.sent_via);
      setCode("");
      wait(RESEND_AFTER);
    });

  /** Signed in by phone (a personal session): where it was going. */
  const land = async () => {
    setDone(true);
    const to = afterPhone(next, start);
    if (to !== null) return window.location.assign(to);
    let me: Me;
    try {
      me = await get<Me>("/v1/me");
    } catch {
      // Signed in all the same: You, where everything else is reached.
      return window.location.assign("/you");
    }
    if (!me.businesses?.length) return window.location.assign("/you");
    setSignedIn(me);
    setStep("business");
  };

  /** Back to the number, saying why. */
  const again = (why: string | null) => {
    act.clear();
    setCode("");
    setNote(why);
    setStep("phone");
  };

  if (step === "business" && signedIn) {
    const [only, ...more] = signedIn.businesses ?? [];
    return only && !more.length ? <OpenIn me={signedIn} business={only} next={next} /> : <ChooseBusiness me={signedIn} next={next} />;
  }

  if (step === "phone")
    return (
      <div className="login card way-in">
        <h1>{chatCode ? `Chat with ${chatWith ?? "the business"}` : "Welcome to Taskiem"}</h1>
        <p className="way-in-line">
          {chatCode
            ? "Your phone number is all you need: we send it a code, and the chat opens here in your browser."
            : "Chat with the businesses you buy from, and run your own."}
        </p>
        {chatCode && (
          <p className="warning" role="note" data-testid="shared-phone">
            Is this your phone? If not, don't stay signed in: choose Sign out in the chat's menu when you are done.
          </p>
        )}
        <form
          key="phone"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              setNote(null);
              const n = toE164(typed);
              if (!n) throw new Error(NOT_A_PHONE + ".");
              await send(n);
              setPhone(n);
              setStep("code");
            }, false);
          }}
        >
          {note && (
            <p className="notice" role="status">
              {note}
            </p>
          )}
          <Field label="Your phone number" hint="A Nigerian number can start with 0. For another country, start with + and its code.">
            <input
              type="tel"
              inputMode="tel"
              autoComplete="tel"
              placeholder="0803 123 4567"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              required
              autoFocus
            />
          </Field>
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy}>
            {act.busy ? "Sending…" : "Continue"}
          </button>
        </form>
        <p className="hint way-in-other">
          <Link to={emailLink}>Sign in with email</Link>
        </p>
        {slack && (
          <p className="hint way-in-other" data-testid="signup-slack">
            Your team works in Slack? <a href={slack}>Start in Slack</a>: Taskiem joins your workspace and sets up workflows for your team.
          </p>
        )}
      </div>
    );

  if (step === "code")
    return (
      <div className="login card way-in">
        <h1>Enter the code</h1>
        <p role="status" className="way-in-line">
          {sentWords(via, phone)}
        </p>
        <form
          key="code"
          onSubmit={(e) => {
            e.preventDefault();
            if (done) return;
            once(async () => {
              let r: { status: string; ticket?: string };
              try {
                r = await post<{ status: string; ticket?: string }>("/v1/auth/phone/verify", { phone, code });
              } catch (err) {
                if (err instanceof ApiError && err.body.ticket_expired) return again("Your code has expired: ask for a new one.");
                throw new Error(codeProblem(err), { cause: err });
              }
              if (r.status === "new" && r.ticket) {
                setTicket(r.ticket);
                setStep("new");
                return;
              }
              await plainly(land);
            });
          }}
        >
          <CodeField code={code} setCode={setCode} left={left} busy={act.busy || done} onResend={(how) => void act.run(() => send(phone, how), false)} sms={via !== "sms"} />
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy || done || code.length !== 6}>
            {act.busy || done ? "Checking…" : "Continue"}
          </button>
        </form>
        <p className="hint way-in-other">
          <button type="button" className="link" onClick={() => again(null)}>
            Use another number
          </button>
        </p>
      </div>
    );

  return (
    <div className="login card way-in">
      <h1>Make your account</h1>
      <p className="way-in-line">This number is new to Taskiem. Tell us your name to finish.</p>
      <form
        key="new"
        onSubmit={(e) => {
          e.preventDefault();
          if (done) return;
          once(async () => {
            try {
              await post("/v1/auth/phone/create", { ticket, name: name.trim(), adult, terms: agree });
            } catch (err) {
              // The code ran out while the name was typed: a new code, from the number.
              if (err instanceof ApiError && err.status === 401) return again("Your code has expired: ask for a new one.");
              throw new Error(codeProblem(err), { cause: err });
            }
            await land();
          });
        }}
      >
        <Field label="Your name">
          <input value={name} onChange={(e) => setName(e.target.value)} required maxLength={80} autoComplete="name" autoFocus />
        </Field>
        <label className="row way-in-check">
          <input type="checkbox" checked={adult} onChange={(e) => setAdult(e.target.checked)} required />
          <span>I'm 18 or over</span>
        </label>
        <label className="row way-in-check" data-testid="way-in-terms">
          <input type="checkbox" checked={agree} onChange={(e) => setAgree(e.target.checked)} required />
          <span>
            I agree to Taskiem's{" "}
            {terms ? (
              <a href={terms.url} target="_blank" rel="noreferrer">
                terms and privacy notice
              </a>
            ) : (
              "terms and privacy notice"
            )}
          </span>
        </label>
        <ErrorBox error={act.error} />
        <button className="primary" type="submit" disabled={act.busy || done}>
          {act.busy || done ? "Creating…" : "Create"}
        </button>
      </form>
      <p className="hint way-in-other">
        <button type="button" className="link" disabled={act.busy || done} onClick={() => again(null)}>
          Use another number
        </button>
      </p>
    </div>
  );
}
