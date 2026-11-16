import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { get, post, type TermsAsked } from "../api";
import { useAuth } from "../auth";
import { codeProblem, countdown, sentWords, toE164 } from "../lib/phone";
import { ErrorBox, Field, useAction } from "../ui";

// The way in (docs/spec/taskiem-for-everyone.md §5.1): one screen for
// signing in and making an account. A phone number, the 6-digit code sent
// to it, and for a new number a name and the two agreements. Email and
// password stay a link away.

/** Seconds before a new code can be asked for. */
const RESEND_AFTER = 60;

/** Runs fn, turning what went wrong into plain words about the code. */
export async function plainly<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    throw new Error(codeProblem(e), { cause: e });
  }
}

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
          maxLength={6}
          value={code}
          onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
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

export function WayIn() {
  const { refresh } = useAuth();
  const [step, setStep] = useState<"phone" | "code" | "new">("phone");
  const [typed, setTyped] = useState("");
  const [phone, setPhone] = useState("");
  const [via, setVia] = useState("whatsapp");
  const [code, setCode] = useState("");
  const [ticket, setTicket] = useState("");
  const [name, setName] = useState("");
  const [adult, setAdult] = useState(false);
  const [agree, setAgree] = useState(false);
  const [terms, setTerms] = useState<TermsAsked | undefined>();
  const [left, wait] = useCountdown();
  const act = useAction();
  useEffect(() => {
    get<{ terms?: TermsAsked }>("/v1/signup").then(
      (o) => setTerms(o.terms),
      () => undefined,
    );
  }, []);
  const search = window.location.search;
  const next = new URLSearchParams(search).get("next");
  const emailLink = `/login?with=email${next ? `&next=${encodeURIComponent(next)}` : ""}`;

  const send = (number: string, how?: "whatsapp" | "sms") =>
    plainly(async () => {
      const r = await post<{ sent_via: string }>("/v1/auth/phone/start", how === "sms" ? { phone: number, via: "sms" } : { phone: number });
      setVia(r.sent_via);
      setCode("");
      wait(RESEND_AFTER);
    });

  if (step === "phone")
    return (
      <div className="login card way-in">
        <h1>Welcome to Taskiem</h1>
        <p className="way-in-line">Chat with the businesses you buy from, and run your own.</p>
        <form
          key="phone"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const n = toE164(typed);
              if (!n) throw new Error("Enter your phone number, with its country code if it is not Nigerian.");
              await send(n);
              setPhone(n);
              setStep("code");
            }, false);
          }}
        >
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
            void act.run(
              () =>
                plainly(async () => {
                  const r = await post<{ status: string; ticket?: string }>("/v1/auth/phone/verify", { phone, code });
                  if (r.status === "new" && r.ticket) {
                    setTicket(r.ticket);
                    setStep("new");
                    return;
                  }
                  await refresh();
                }),
              false,
            );
          }}
        >
          <CodeField code={code} setCode={setCode} left={left} busy={act.busy} onResend={(how) => void act.run(() => send(phone, how), false)} sms={via !== "sms"} />
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy || code.length !== 6}>
            {act.busy ? "Checking…" : "Continue"}
          </button>
        </form>
        <p className="hint way-in-other">
          <button
            type="button"
            className="link"
            onClick={() => {
              act.clear();
              setStep("phone");
            }}
          >
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
          void act.run(async () => {
            await post("/v1/auth/phone/create", { ticket, name: name.trim(), adult, terms: agree });
            await refresh();
          }, false);
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
        <button className="primary" type="submit" disabled={act.busy}>
          {act.busy ? "Creating…" : "Create"}
        </button>
      </form>
    </div>
  );
}
