import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, post } from "../api";
import { useAuth } from "../auth";
import { passkeysSupported } from "../passkeys";
import { ErrorBox, Field, useAction } from "../ui";

export function Login() {
  const { login, loginWithPasskey } = useAuth();
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
  const next = new URLSearchParams(window.location.search).get("next") || "/workflows";
  const startSSO = () => window.location.assign(`${sso?.start}?return_to=${encodeURIComponent(next)}`);
  return (
    <div className="login card">
      <h1>Sign in to Taskiem</h1>
      {ssoError && <div className="error">{ssoError}</div>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(() => login(email, password));
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
              <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
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
          <button type="button" disabled={act.busy} style={{ width: "100%" }} onClick={() => void act.run(loginWithPasskey)}>
            Sign in with a passkey
          </button>
        </>
      )}
      {act.error instanceof ApiError && act.error.body.passkey_required ? <p className="hint">Administrators sign in with their passkey.</p> : null}
    </div>
  );
}
