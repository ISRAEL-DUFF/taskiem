import { useState } from "react";
import { ApiError } from "../api";
import { useAuth } from "../auth";
import { passkeysSupported } from "../passkeys";
import { ErrorBox, Field, useAction } from "../ui";

export function Login() {
  const { login, loginWithPasskey } = useAuth();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const act = useAction();
  return (
    <div className="login card">
      <h1>Sign in to Taskiem</h1>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(() => login(email, password));
        }}
      >
        <Field label="Email">
          <input type="email" autoComplete="username" value={email} onChange={(e) => setEmail(e.target.value)} required autoFocus />
        </Field>
        <Field label="Password">
          <input type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} required />
        </Field>
        <ErrorBox error={act.error} />
        <button className="primary" type="submit" disabled={act.busy} style={{ width: "100%" }}>
          {act.busy ? "Signing in…" : "Sign in"}
        </button>
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
