import { useEffect, useRef, useState, type ChangeEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { post } from "../api";
import { useAuth } from "../auth";
import { useOnboarding } from "../onboarding";
import { ErrorBox, Field, useAction } from "../ui";

/** Self-serve signup (POST /v1/signup, docs/onboarding.md): an
 * organisation and its owner, signed in at once, then Get started. */
export function Signup() {
  const { refresh } = useAuth();
  const nav = useNavigate();
  const [f, setF] = useState({ tenant: "", name: "", email: "", password: "", website: "" });
  const act = useAction();
  const set = (k: keyof typeof f) => (e: ChangeEvent<HTMLInputElement>) => setF({ ...f, [k]: e.target.value });
  return (
    <div className="login card">
      <h1>Start with Taskiem</h1>
      <p className="hint">Create your organisation. You can run your first workflow in a few minutes.</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            await post("/v1/signup", f);
            await refresh();
            nav("/start", { replace: true });
          });
        }}
      >
        <Field label="Business or organisation name">
          <input value={f.tenant} onChange={set("tenant")} required minLength={2} maxLength={80} autoComplete="organization" autoFocus />
        </Field>
        <Field label="Your name">
          <input value={f.name} onChange={set("name")} maxLength={80} autoComplete="name" />
        </Field>
        <Field label="Email">
          <input type="email" value={f.email} onChange={set("email")} required autoComplete="email" />
        </Field>
        <Field label="Password" hint="At least 12 characters. A short sentence is easy to remember.">
          <input type="password" value={f.password} onChange={set("password")} required minLength={12} autoComplete="new-password" />
        </Field>
        {/* People never see this field; form-filling bots do. */}
        <div className="honeypot" aria-hidden="true">
          <label>
            Website
            <input tabIndex={-1} autoComplete="off" value={f.website} onChange={set("website")} />
          </label>
        </div>
        <ErrorBox error={act.error} />
        <button className="primary" type="submit" disabled={act.busy} style={{ width: "100%" }}>
          {act.busy ? "Creating…" : "Create account"}
        </button>
      </form>
      <p className="hint" style={{ textAlign: "center" }}>
        Already have an account? <Link to="/login">Sign in</Link>
      </p>
    </div>
  );
}

/** Confirms the signed-in person's email with the link's token, read from
 * the fragment (never sent to a server) and then dropped from the address
 * bar. */
export function VerifyEmail() {
  const [token] = useState(() => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "");
  const { refresh } = useAuth();
  const onboarding = useOnboarding();
  const [done, setDone] = useState(false);
  const act = useAction();
  const tried = useRef(false);
  useEffect(() => {
    if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
    if (!token || tried.current) return;
    tried.current = true;
    void act.run(async () => {
      await post("/v1/me/email/verify", { token });
      setDone(true);
      onboarding.reload();
      await refresh();
    });
  }, [token]);
  return (
    <div className="card" style={{ maxWidth: 560 }}>
      <h1>Confirm your email</h1>
      {!token && <p className="error">This page needs the link from your email.</p>}
      {act.busy && <p>Confirming…</p>}
      {done && <p role="status">Your email is confirmed. You can now invite people and make API keys.</p>}
      <ErrorBox error={act.error} />
      <p>
        <Link to="/start">Back to Get started</Link>
      </p>
    </div>
  );
}
