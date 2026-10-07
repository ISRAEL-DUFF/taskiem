import { BrowserRouter, Navigate, NavLink, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth";
import { Login } from "./pages/Login";
import { Workflows } from "./pages/Workflows";
import { Editor } from "./pages/Editor";
import { Runs } from "./pages/Runs";
import { RunPage } from "./pages/Run";
import { Approvals } from "./pages/Approvals";
import { Catalogue } from "./pages/Catalogue";
import { Connections } from "./pages/Connections";
import { Settings } from "./pages/Settings";
import { Audit } from "./pages/Audit";
import { Alerts } from "./pages/Alerts";
import { Dashboard } from "./pages/Dashboard";
import { Members } from "./pages/Members";
import { Policies } from "./pages/Policies";
import { Account, Invitations, Passkeys } from "./pages/Account";
import { Fragment, useState } from "react";
import { Icon } from "./icons";
import { ErrorBox, useAction } from "./ui";
import { Reports } from "./pages/Reports";
import { ForgotPassword, ResetPassword } from "./pages/Password";
import { Handoff } from "./pages/Handoff";
import { Templates } from "./pages/Templates";
import { Billing, BillingBanner } from "./pages/Billing";
import { Signup, VerifyEmail } from "./pages/Signup";
import { Guide, Start } from "./pages/Start";
import { OnboardingProvider, useOnboarding } from "./onboarding";
import { Keys } from "./pages/Keys";

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

/** Someone with an account but no organisation, signed in to answer the
 * invitations waiting for them; nothing else is open to them. */
function InviteeHome() {
  const { me, logout, refresh } = useAuth();
  return (
    <div className="login card" style={{ maxWidth: 720 }}>
      <h1>Welcome back{me?.user?.name ? `, ${me.user.name}` : ""}</h1>
      <p>You do not belong to an organisation on Taskiem yet. Accept an invitation to join one.</p>
      <Invitations always onJoined={() => void refresh()} />
      <p>
        <button onClick={() => void logout()}>Sign out</button>
      </p>
    </div>
  );
}

function Shell() {
  const { me } = useAuth();
  const loc = useLocation();
  // Password recovery works signed in or out.
  if (loc.pathname === "/forgot-password") return <ForgotPassword />;
  if (loc.pathname === "/reset-password") return <ResetPassword />;
  if (me === undefined) return <div className="empty">Loading…</div>;
  if (me === null) {
    if (loc.pathname === "/signup") return <Signup />;
    return loc.pathname === "/login" ? <Login /> : <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search + loc.hash)}`} replace />;
  }
  if (me.invitations_only) return <InviteeHome />;
  if (me.enrol_passkey) return <EnrolPasskey />;
  if (loc.pathname === "/login") return <Navigate to={new URLSearchParams(loc.search).get("next") || "/workflows"} replace />;
  if (loc.pathname === "/signup") return <Navigate to="/start" replace />;
  return (
    <OnboardingProvider>
      <SignedIn />
    </OnboardingProvider>
  );
}

function SignedIn() {
  const { me, can, logout } = useAuth();
  const onboarding = useOnboarding().data;
  if (!me) return null;
  // Grouped as the sidebar shows them; [path, label, icon, visible].
  const groups: [string, [string, string, string, boolean][]][] = [
    [
      "Build",
      [
        // For organisations that signed themselves up, until the checklist is
        // done or hidden (it stays at /start).
        ["/start", `Get started (${onboarding?.done ?? 0}/${onboarding?.total ?? 0})`, "start", !!onboarding?.self_serve && !onboarding.complete && !onboarding.dismissed],
        ["/dashboard", "Dashboard", "dashboard", can("run.read")],
        ["/workflows", "Workflows", "workflows", can("workflow.read")],
        ["/templates", "Templates", "templates", true],
        ["/runs", "Runs", "runs", can("run.read")],
        ["/approvals", "Approvals", "approvals", can("approval.decide") || can("workflow.publish")],
      ],
    ],
    [
      "Connect",
      [
        ["/connections", "Connections", "connections", can("connection.manage")],
        ["/catalogue", "Connector catalogue", "catalogue", can("connector.manage") || can("workflow.read")],
        ["/settings", "Secrets & settings", "settings", can("secret.manage") || can("workflow.read")],
      ],
    ],
    [
      "Govern",
      [
        ["/policies", "Approval policies", "policies", can("workflow.read")],
        ["/alerts", "Alerts", "alerts", can("alert.manage")],
        ["/audit", "Audit log", "audit", can("audit.read")],
        ["/reports", "Reports", "reports", can("audit.read")],
      ],
    ],
    [
      "Organisation",
      [
        ["/members", "Members & keys", "members", can("member.manage")],
        ["/settings/billing", "Billing", "billing", can("billing.manage")],
        ["/settings/keys", "Encryption keys", "keys", can("key.manage")],
      ],
    ],
  ];
  const who = me.user?.email ?? "API key";
  return (
    <div className="shell">
      <nav className="nav" aria-label="Main">
        <div className="brand">
          <span className="brand-mark" aria-hidden="true">
            T
          </span>
          Taskiem
        </div>
        {groups.map(([section, links]) => {
          const shown = links.filter(([, , , ok]) => ok);
          if (shown.length === 0) return null;
          return (
            <Fragment key={section}>
              <div className="section" aria-hidden="true">
                {section}
              </div>
              {shown.map(([to, label, icon]) => (
                <NavLink key={to} to={to} end={to === "/settings"}>
                  <Icon name={icon} />
                  {label}
                </NavLink>
              ))}
            </Fragment>
          );
        })}
        <div className="spacer" />
        <div className="who">
          <span className="avatar" aria-hidden="true">
            {who.slice(0, 2)}
          </span>
          <div>
            <NavLink to="/account">{who}</NavLink>
            <div className="roles">{me.roles.join(", ")}</div>
          </div>
        </div>
        <button className="signout" onClick={() => void logout()}>
          Sign out
        </button>
      </nav>
      <main className="main">
        <BillingBanner />
        <Routes>
          <Route path="/" element={<Navigate to="/workflows" replace />} />
          <Route path="/workflows" element={<Workflows />} />
          <Route path="/workflows/:id" element={<Editor />} />
          <Route path="/templates" element={<Templates />} />
          <Route path="/start" element={<Start />} />
          <Route path="/start/guide" element={<Guide />} />
          <Route path="/verify-email" element={<VerifyEmail />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/runs" element={<Runs />} />
          <Route path="/runs/:id" element={<RunPage />} />
          <Route path="/approvals" element={<Approvals />} />
          <Route path="/connections" element={<Connections />} />
          <Route path="/catalogue" element={<Catalogue />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/settings/billing" element={<Billing />} />
          <Route path="/settings/keys" element={<Keys />} />
          <Route path="/alerts" element={<Alerts />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/members" element={<Members />} />
          <Route path="/policies" element={<Policies />} />
          <Route path="/account" element={<Account />} />
          <Route path="/handoff" element={<Handoff />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="*" element={<div className="empty">Not found</div>} />
        </Routes>
      </main>
    </div>
  );
}
