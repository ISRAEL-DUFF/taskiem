// The operator console (/ops, docs/operator-console.md): Taskiem's own
// operators, signed in with a passkey (or single sign-on) to a session of
// their own. Its own minimal layout over the same design tokens.

import { useCallback, useEffect, useState } from "react";
import { BrowserRouter, Navigate, NavLink, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { ApiError } from "../api";
import { Icon } from "../icons";
import { passkeysSupported } from "../passkeys";
import { ErrorBox, Field, useAction } from "../ui";
import { opsEnrol, opsGet, opsPasskeySignIn, opsPost, type OpsMe } from "./client";
import { Queue, Submission } from "./Review";
import { Audit, Publishers, Reviewers, Status, Tenant, Tenants } from "./Pages";

export function OpsApp() {
  return (
    <BrowserRouter>
      <OpsShell />
    </BrowserRouter>
  );
}

function OpsShell() {
  const loc = useLocation();
  const [me, setMe] = useState<OpsMe | null | undefined>(undefined);
  const refresh = useCallback(async () => {
    try {
      setMe(await opsGet<OpsMe>("/me"));
    } catch (e) {
      if (e instanceof ApiError && (e.status === 401 || e.status === 404)) setMe(null);
      else throw e;
    }
  }, []);
  useEffect(() => {
    void refresh();
    const onOut = () => setMe(null);
    window.addEventListener("taskiem:ops-unauthorised", onOut);
    return () => window.removeEventListener("taskiem:ops-unauthorised", onOut);
  }, [refresh]);

  if (loc.pathname === "/ops/enrol") return <Enrol onDone={refresh} />;
  if (me === undefined) return <div className="empty">Loading…</div>;
  if (me === null) {
    return loc.pathname === "/ops/login" ? <OpsLogin onDone={refresh} /> : <Navigate to="/ops/login" replace />;
  }
  if (loc.pathname === "/ops/login") return <Navigate to="/ops" replace />;
  return <SignedIn me={me} onSignOut={() => setMe(null)} />;
}

function OpsLogin({ onDone }: { onDone: () => Promise<void> }) {
  const act = useAction();
  const [config, setConfig] = useState<{ passkeys: boolean; sso: boolean; sso_name?: string; session_minutes: number } | null>(null);
  const ssoError = new URLSearchParams(window.location.search).get("sso_error");
  useEffect(() => {
    opsGet<{ passkeys: boolean; sso: boolean; sso_name?: string; session_minutes: number }>("/auth/config").then(setConfig, () => setConfig(null));
  }, []);
  return (
    <div className="login card">
      <h1>Taskiem operator console</h1>
      <p className="hint">For Taskiem's own operators. Organisations sign in at <a href="/login">/login</a>.</p>
      {ssoError && <div className="error">{ssoError}</div>}
      <ErrorBox error={act.error} />
      {passkeysSupported() && (
        <button
          className="primary"
          style={{ width: "100%" }}
          disabled={act.busy}
          onClick={() =>
            void act.run(async () => {
              await opsPasskeySignIn();
              await onDone();
            })
          }
        >
          Sign in with your passkey
        </button>
      )}
      {config?.sso && (
        <p>
          <button style={{ width: "100%" }} onClick={() => window.location.assign("/v1/ops/auth/sso/start")}>
            Continue with {config.sso_name || "single sign-on"}
          </button>
        </p>
      )}
      {config && (
        <p className="hint">Sessions last {config.session_minutes} minutes. Every change asks for your passkey again.</p>
      )}
    </div>
  );
}

/** Enrolment from the one-time link `taskiem operators add|enrol` prints
 * (the token is in the URL fragment, never sent to a server by itself). */
function Enrol({ onDone }: { onDone: () => Promise<void> }) {
  const navigate = useNavigate();
  const [token] = useState(() => window.location.hash.replace(/^#/, ""));
  const [name, setName] = useState("Passkey");
  const act = useAction();
  useEffect(() => {
    // Take the token out of the address bar and history.
    if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
  }, []);
  return (
    <div className="login card">
      <h1>Enrol your passkey</h1>
      <p>This link adds a passkey to your Taskiem operator account. It works once.</p>
      {!token && <div className="error">This page needs the link from your enrolment message (it ends with #…).</div>}
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            await opsEnrol(token, name);
            await onDone();
            navigate("/ops", { replace: true });
          });
        }}
      >
        <Field label="Name for this passkey">
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={64} required />
        </Field>
        <ErrorBox error={act.error} />
        <button className="primary" type="submit" disabled={act.busy || !token || !passkeysSupported()} style={{ width: "100%" }}>
          Create passkey
        </button>
      </form>
    </div>
  );
}

function SignedIn({ me, onSignOut }: { me: OpsMe; onSignOut: () => void }) {
  const links: [string, string, string][] = [
    ["/ops/catalogue", "Review queue", "catalogue"],
    ["/ops/publishers", "Publishers", "approvals"],
    ["/ops/reviewers", "Reviewers", "members"],
    ["/ops/status", "Status page", "alerts"],
    ["/ops/tenants", "Tenants", "dashboard"],
    ["/ops/audit", "Platform audit", "audit"],
  ];
  const signOut = async () => {
    try {
      await opsPost("/auth/logout");
    } finally {
      onSignOut();
    }
  };
  const who = me.operator.email;
  return (
    <div className="shell">
      <nav className="nav" aria-label="Operator console">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">
            T
          </span>
          Operator console
        </div>
        <div className="section" aria-hidden="true">
          Platform
        </div>
        {links.map(([to, label, icon]) => (
          <NavLink key={to} to={to}>
            <Icon name={icon} />
            {label}
          </NavLink>
        ))}
        <div className="spacer" />
        <div className="who">
          <span className="avatar" aria-hidden="true">
            {who.slice(0, 2)}
          </span>
          <div>
            <span style={{ color: "#fff", fontWeight: 500 }}>{who}</span>
            <div className="roles">
              {me.reviewer ? "reviewer · " : ""}until {new Date(me.operator.expires_at).toLocaleTimeString()}
            </div>
          </div>
        </div>
        <button className="signout" onClick={() => void signOut()}>
          Sign out
        </button>
      </nav>
      <main className="main">
        <Routes>
          <Route path="/ops" element={<Navigate to="/ops/catalogue" replace />} />
          <Route path="/ops/catalogue" element={<Queue />} />
          <Route path="/ops/catalogue/:id" element={<Submission me={me} />} />
          <Route path="/ops/publishers" element={<Publishers />} />
          <Route path="/ops/reviewers" element={<Reviewers me={me} />} />
          <Route path="/ops/status" element={<Status />} />
          <Route path="/ops/tenants" element={<Tenants />} />
          <Route path="/ops/tenants/:id" element={<Tenant />} />
          <Route path="/ops/audit" element={<Audit />} />
          <Route path="*" element={<Navigate to="/ops/catalogue" replace />} />
        </Routes>
      </main>
    </div>
  );
}
