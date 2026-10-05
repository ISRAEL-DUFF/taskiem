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
import { Members } from "./pages/Members";

export function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Shell />
      </BrowserRouter>
    </AuthProvider>
  );
}

function Shell() {
  const { me, can, logout } = useAuth();
  const loc = useLocation();
  if (me === undefined) return <div className="empty">Loading…</div>;
  if (me === null) {
    return loc.pathname === "/login" ? <Login /> : <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />;
  }
  if (loc.pathname === "/login") return <Navigate to={new URLSearchParams(loc.search).get("next") || "/workflows"} replace />;
  const links: [string, string, boolean][] = [
    ["/workflows", "Workflows", can("workflow.read")],
    ["/runs", "Runs", can("run.read")],
    ["/approvals", "Approvals", can("approval.decide")],
    ["/connections", "Connections", can("connection.manage")],
    ["/settings", "Secrets & settings", can("secret.manage") || can("workflow.read")],
    ["/audit", "Audit log", can("audit.read")],
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
          {me.user?.email}
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
          <Route path="/runs" element={<Runs />} />
          <Route path="/runs/:id" element={<RunPage />} />
          <Route path="/approvals" element={<Approvals />} />
          <Route path="/connections" element={<Connections />} />
          <Route path="/settings" element={<Settings />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/members" element={<Members />} />
          <Route path="*" element={<div className="empty">Not found</div>} />
        </Routes>
      </main>
    </div>
  );
}
