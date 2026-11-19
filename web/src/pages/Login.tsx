import { useEffect, useState } from "react";
import { Link, useLocation } from "react-router-dom";
import { ApiError, post } from "../api";
import { useAuth } from "../auth";
import { safeNext } from "../lib/safePath";
import { passkeysSupported } from "../passkeys";
import { ErrorBox, Field, useAction, PasswordInput } from "../ui";
import { plainly } from "../lib/phone";
import { codeDigits, WayIn } from "./WayIn";

/** /login and /signup: the phone way in (WayIn), or with ?with=email the
 * email, password, passkey and chat sign-in (taskiem-for-everyone §5.1). */
export function Login() {
  const q = new URLSearchParams(useLocation().search);
  // A single sign-on that came back with a problem says so on the email screen.
  const withEmail = q.get("with") === "email" || q.has("sso_error");
  return withEmail ? <EmailLogin /> : <WayIn />;
}

function EmailLogin() {
  const { login, loginWithPasskey } = useAuth();
  const [withChat, setWithChat] = useState(false);
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const act = useAction();
  const [sso, setSSO] = useState<{ start: string; required: boolean } | null>(null);
  const [breakGlass, setBreakGlass] = useState(false); // owners are exempt from enforced SSO
  const ssoError = new URLSearchParams(window.location.search).get("sso_error");
  // Offer single sign-on when the email's domain uses it.
  useEffect(() => {
    if (!/^[^@\s]+@[^@\s]+\.[^@\s]+$/.test(email)) {
      setSSO(null);
      return;
    }
    const t = setTimeout(() => {
      post<{ sso: boolean; start?: string; required?: boolean }>("/v1/auth/sso/discover", { email }).then(
        (r) => setSSO(r.sso && r.start ? { start: r.start, required: Boolean(r.required) } : null),
        () => setSSO(null),
      );
    }, 300);
    return () => clearTimeout(t);
  }, [email]);
  const next = safeNext(new URLSearchParams(window.location.search).get("next"), "/workflows");
  const nextParam = new URLSearchParams(window.location.search).get("next");
  const phoneLink = nextParam ? `/login?next=${encodeURIComponent(nextParam)}` : "/login";
  const startSSO = () => window.location.assign(`${sso?.start}?return_to=${encodeURIComponent(next)}`);
  return (
    <div className="login card">
      <h1>Sign in to Taskiem</h1>
      {ssoError && <div className="error">{ssoError}</div>}
      {withChat ? (
        <ChatSignIn onBack={() => setWithChat(false)} />
      ) : (
        <>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(() => login(email, password), false);
        }}
      >
        <Field label="Email">
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </Field>
        {sso && (
          <p>
            <button type="button" className="primary" style={{ width: "100%" }} onClick={startSSO}>
              Continue with single sign-on
            </button>
          </p>
        )}
        {sso?.required && !breakGlass && (
          <p className="hint" style={{ textAlign: "center" }}>
            <button type="button" className="link" onClick={() => setBreakGlass(true)}>
              Owner? Use your password
            </button>
          </p>
        )}
        {(!sso?.required || breakGlass) && (
          <>
            <Field label="Password">
              <PasswordInput autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
            </Field>
            <ErrorBox error={act.error} />
            <button className={sso ? "" : "primary"} type="submit" disabled={act.busy} style={{ width: "100%" }}>
              {act.busy ? "Signing in…" : "Sign in"}
            </button>
            <p className="hint" style={{ textAlign: "center" }}>
              <Link to="/forgot-password">Forgot password?</Link>
            </p>
          </>
        )}
      </form>
      {passkeysSupported() && (
        <>
          <p className="hint" style={{ textAlign: "center" }}>
            or
          </p>
          <button type="button" disabled={act.busy} style={{ width: "100%" }} onClick={() => void act.run(loginWithPasskey, false)}>
            Sign in with a passkey
          </button>
        </>
      )}
      <button type="button" disabled={act.busy} style={{ width: "100%", marginTop: "0.5rem" }} onClick={() => setWithChat(true)}>
        Sign in with WhatsApp or Telegram
      </button>
        </>
      )}
      {act.error instanceof ApiError && act.error.body.passkey_required ? <p className="hint">Sign in with your passkey.</p> : null}
      <p className="hint" style={{ textAlign: "center" }}>
        <Link to={phoneLink}>Use your phone number instead</Link>
      </p>
    </div>
  );
}

/** Signing in with the chat (docs/spec/chat-sign-in.md): the phone number
 * of the person's WhatsApp or Telegram, then the six-digit code sent there.
 * The first answer is the same for any number. */
function ChatSignIn({ onBack }: { onBack: () => void }) {
  const { refresh } = useAuth();
  const [phone, setPhone] = useState("");
  const [code, setCode] = useState("");
  const [sent, setSent] = useState<string | null>(null);
  const act = useAction();
  return (
    <>
      {sent === null ? (
        // Each step its own form (key): the code's field is new, so it takes
        // the focus (autoFocus), rather than React reusing the phone's.
        <form
          key="phone"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => setSent((await plainly(() => post<{ status: string }>("/v1/auth/chat/start", { phone }))).status), false);
          }}
        >
          <p className="hint">We send a code to the WhatsApp or Telegram on your Taskiem account.</p>
          <Field label="Phone number">
            <input type="tel" inputMode="tel" autoComplete="tel" value={phone} onChange={(e) => setPhone(e.target.value)} required autoFocus />
          </Field>
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy} style={{ width: "100%" }}>
            {act.busy ? "Sending…" : "Send me a code"}
          </button>
        </form>
      ) : (
        <form
          key="code"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              // Someone in no business, or on a device not yet proved for
              // their businesses, gets their own side: You (App.tsx).
              await plainly(() => post("/v1/auth/chat/verify", { phone, code }));
              await refresh();
            }, false);
          }}
        >
          <p role="status">{sent}</p>
          <Field label="Code">
            <input inputMode="numeric" autoComplete="one-time-code" value={code} onChange={(e) => setCode(codeDigits(e.target.value))} required autoFocus />
          </Field>
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy || code.length !== 6} style={{ width: "100%" }}>
            {act.busy ? "Signing in…" : "Sign in"}
          </button>
          <p className="hint" style={{ textAlign: "center" }}>
            <button
              type="button"
              className="link"
              onClick={() => {
                setSent(null);
                setCode("");
              }}
            >
              Send a new code
            </button>
          </p>
        </form>
      )}
      <p className="hint" style={{ textAlign: "center" }}>
        <button type="button" className="link" onClick={onBack}>
          Sign in with your email instead
        </button>
      </p>
    </>
  );
}
