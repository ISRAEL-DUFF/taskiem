import { BrowserRouter, Navigate, NavLink, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth";
import { Login } from "./pages/Login";
import { Workflows } from "./pages/Workflows";
import { Editor } from "./pages/Editor";
import { Runs } from "./pages/Runs";
import { RunPage } from "./pages/Run";
import { Approvals } from "./pages/Approvals";
import { Connections } from "./pages/Connections";
import { Settings } from "./pages/Settings";
import { Audit } from "./pages/Audit";
import { Alerts } from "./pages/Alerts";
import { Dashboard } from "./pages/Dashboard";
import { Members } from "./pages/Members";
import { Policies } from "./pages/Policies";
import { Account, Passkeys } from "./pages/Account";
import { useState } from "react";
import { ErrorBox, useAction } from "./ui";
import { Reports } from "./pages/Reports";
import { ForgotPassword, ResetPassword } from "./pages/Password";

export function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Shell />
      </BrowserRouter>
    </AuthProvider>
  );
}

/** An administrator held to passkeys, signed in with a password, adds one
 * and then signs in with it; nothing else is open to them until then. */
function EnrolPasskey() {
  const { logout, loginWithPasskey } = useAuth();
  const [added, setAdded] = useState(false);
  const act = useAction();
  return (
    <div className="login card" style={{ maxWidth: 640 }}>
      <h1>Add a passkey</h1>
      <p>Administrators sign in to Taskiem with a passkey: your device's fingerprint, face or PIN. Add one to continue.</p>
      {added ? (
        <>
          <p>Passkey added. Sign in with it now.</p>
          <ErrorBox error={act.error} />
          <button className="primary" disabled={act.busy} onClick={() => void act.run(loginWithPasskey)}>
            Sign in with your passkey
          </button>
        </>
      ) : (
        <Passkeys onAdded={() => setAdded(true)} />
      )}
      <p>
        <button onClick={() => void logout()}>Sign out</button>
      </p>
    </div>
  );
}

function Shell() {
  const { me, can, logout } = useAuth();
  const loc = useLocation();
  // Password recovery works signed in or out.
  if (loc.pathname === "/forgot-password") return <ForgotPassword />;
  if (loc.pathname === "/reset-password") return <ResetPassword />;
  if (me === undefined) return <div className="empty">Loading…</div>;
  if (me === null) {
    return loc.pathname === "/login" ? <Login /> : <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />;
  }
  if (me.enrol_passkey) return <EnrolPasskey />;
  if (loc.pathname === "/login") return <Navigate to={new URLSearchParams(loc.search).get("next") || "/workflows"} replace />;
  const links: [string, string, boolean][] = [
    ["/dashboard", "Dashboard", can("run.read")],
    ["/workflows", "Workflows", can("workflow.read")],
    ["/runs", "Runs", can("run.read")],
    ["/approvals", "Approvals", can("approval.decide") || can("workflow.publish")],
    ["/policies", "Approval policies", can("workflow.read")],
    ["/connections", "Connections", can("connection.manage")],
    ["/settings", "Secrets & settings", can("secret.manage") || can("workflow.read")],
    ["/alerts", "Alerts", can("alert.manage")],
    ["/audit", "Audit log", can("audit.read")],
    ["/reports", "Reports", can("audit.read")],
    ["/members", "Members & keys", can("member.manage")],
  ];
  return (
    <div className="shell">
      <nav className="nav" aria-label="Main">
        <div className="brand">Taskiem</div>
        {links
          .filter(([, , ok]) => ok)
          .map(([to, label]) => (
            <NavLink key={to} to={to}>
              {label}
            </NavLink>
          ))}
        <div className="spacer" />
        <div className="who">
          <NavLink to="/account">{me.user?.email ?? "API key"}</NavLink>
          <br />
          {me.roles.join(", ")}
        </div>
        <button onClick={() => void logout()}>Sign out</button>
      </nav>
      <main className="main">
        <Routes>
          <Route path="/" element={<Navigate to="/workflows" replace />} />
          <Route path="/workflows" element={<Workflows />} />
          <Route path="/workflows/:id" element={<Editor />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/runs" element={<Runs />} />
          <Route path="/runs/:id" element={<RunPage />} />
          <Route path="/approvals" element={<Approvals />} />
          <Route path="/connections" element={<Connections />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/alerts" element={<Alerts />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/members" element={<Members />} />
          <Route path="/policies" element={<Policies />} />
          <Route path="/account" element={<Account />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="*" element={<div className="empty">Not found</div>} />
        </Routes>
      </main>
    </div>
  );
}
