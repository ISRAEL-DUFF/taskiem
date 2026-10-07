import { useState } from "react";
import { Link } from "react-router-dom";
import { post } from "../api";
import { useAuth } from "../auth";
import { proof } from "../passkeys";
import { ErrorBox, Field, useAction } from "../ui";

const minLength = 12;

/** New password, typed twice; the error to show, if any. */
function mismatch(a: string, b: string): string | null {
  if (a.length > 0 && a.length < minLength) return `At least ${minLength} characters.`;
  if (b.length > 0 && a !== b) return "The two passwords differ.";
  return null;
}

/** Asks for a reset link. The answer is the same whether or not the email
 * has an account. */
export function ForgotPassword() {
  const [email, setEmail] = useState("");
  const [sent, setSent] = useState<string | null>(null);
  const act = useAction();
  return (
    <div className="login card">
      <h1>Reset your password</h1>
      {sent ? (
        <p role="status">{sent}</p>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => setSent((await post<{ status: string }>("/v1/auth/password/forgot", { email })).status));
          }}
        >
          <p className="hint">Enter the email you sign in with. If it signs in with a password, we email a link to choose a new one.</p>
          <Field label="Email">
            <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
          </Field>
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy} style={{ width: "100%" }}>
            {act.busy ? "Sending…" : "Email me a link"}
          </button>
        </form>
      )}
      <p className="hint" style={{ textAlign: "center" }}>
        <Link to="/login">Back to sign in</Link>
      </p>
    </div>
  );
}

/** Reads the link's token from the fragment (never sent to a server) and
 * sets the new password. It does not sign in. */
export function ResetPassword() {
  const [token] = useState(() => {
    const t = new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "";
    // Keep the token out of the address bar and history from here on.
    if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
    return t;
  });
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [done, setDone] = useState<string | null>(null);
  const act = useAction();
  const problem = mismatch(password, again);
  return (
    <div className="login card">
      <h1>Choose a new password</h1>
      {done ? (
        <>
          <p role="status">{done}</p>
          <p>
            <Link to="/login">Sign in</Link>
          </p>
        </>
      ) : !token ? (
        <>
          <p className="error">This page needs the link from your email. Ask for a new one if it does not work.</p>
          <p>
            <Link to="/forgot-password">Ask for a new link</Link>
          </p>
        </>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => setDone((await post<{ status: string }>("/v1/auth/password/reset", { token, password })).status));
          }}
        >
          <Field label="New password" hint={`At least ${minLength} characters.`}>
            <input type="password" autoComplete="new-password" minLength={minLength} value={password} onChange={(e) => setPassword(e.target.value)} required autoFocus />
          </Field>
          <Field label="New password again">
            <input type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} required />
          </Field>
          {problem && <p className="hint">{problem}</p>}
          <ErrorBox error={act.error} />
          <button className="primary" type="submit" disabled={act.busy || !!problem || password !== again} style={{ width: "100%" }}>
            {act.busy ? "Saving…" : "Set password"}
          </button>
          <p className="hint">Every session of yours is signed out. Administrators still sign in with their passkey.</p>
        </form>
      )}
    </div>
  );
}

/** Account: change the password (with the current one), or set a first
 * one with a passkey or authenticator code. Not offered to people who sign
 * in by single sign-on only. */
export function ChangePassword() {
  const { me, refresh } = useAuth();
  const [current, setCurrent] = useState("");
  const [password, setPassword] = useState("");
  const [again, setAgain] = useState("");
  const [code, setCode] = useState("");
  const [done, setDone] = useState(false);
  const act = useAction();
  const f = me?.factors;
  if (!me?.user || !f || (!f.password && !f.passkey && !f.totp)) return null;
  const problem = mismatch(password, again);
  const submit = async () => {
    const body: Record<string, unknown> = { new_password: password };
    if (f.password) body.current_password = current;
    else Object.assign(body, await proof(f, code));
    await post("/v1/me/password", body);
    setCurrent("");
    setPassword("");
    setAgain("");
    setCode("");
    setDone(true);
    await refresh();
  };
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>{f.password ? "Password" : "Set a password"}</h2>
      <p className="hint">
        {f.password
          ? "Changing your password signs out your other sessions."
          : "You sign in without a password. You can set one, confirming with your passkey or authenticator code."}
      </p>
      {done && <p role="status">Password saved. Your other sessions are signed out.</p>}
      <ErrorBox error={act.error} />
      <form
        onSubmit={(e) => {
          e.preventDefault();
          setDone(false);
          void act.run(submit);
        }}
      >
        <div className="row" style={{ alignItems: "flex-end", flexWrap: "wrap" }}>
          {f.password && (
            <Field label="Current password">
              <input type="password" autoComplete="current-password" value={current} onChange={(e) => setCurrent(e.target.value)} required />
            </Field>
          )}
          {!f.password && !f.passkey && f.totp && (
            <Field label="Authenticator code, to set a password">
              <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
            </Field>
          )}
          <Field label="New password">
            <input type="password" autoComplete="new-password" minLength={minLength} value={password} onChange={(e) => setPassword(e.target.value)} required />
          </Field>
          <Field label="New password again">
            <input type="password" autoComplete="new-password" value={again} onChange={(e) => setAgain(e.target.value)} required />
          </Field>
          <button className="primary" type="submit" disabled={act.busy || !!problem || password !== again || password === ""}>
            {f.password ? "Change password" : "Set password"}
          </button>
        </div>
        {problem && <p className="hint">{problem}</p>}
      </form>
    </section>
  );
}
