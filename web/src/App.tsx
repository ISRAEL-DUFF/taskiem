import { BrowserRouter, Navigate, NavLink, Route, Routes, useLocation } from "react-router-dom";
import { AuthProvider, useAuth } from "./auth";
import { PageBoundary } from "./PageBoundary";
import { Login } from "./pages/Login";
import { Workflows } from "./pages/Workflows";
import { NewWorkflow } from "./pages/NewWorkflow";
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
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "./icons";
import { EmptyState, ErrorBox, Skeleton, useAction, useFocusTrap, useScrollableTables } from "./ui";
import { Reports } from "./pages/Reports";
import { ForgotPassword, ResetPassword } from "./pages/Password";
import { AcceptInvitation } from "./pages/AcceptInvitation";
import { Handoff } from "./pages/Handoff";
import { Templates } from "./pages/Templates";
import { Billing, BillingBanner } from "./pages/Billing";
import { Signup, VerifyEmail } from "./pages/Signup";
import { Guide, Start } from "./pages/Start";
import { OnboardingProvider, useOnboarding } from "./onboarding";
import { Keys } from "./pages/Keys";
import { TablePage, Tables } from "./pages/Tables";
import { Storefront } from "./pages/Storefront";
import { Look } from "./pages/Look";
import { StorefrontOrder, StorefrontOrders } from "./pages/StorefrontOrders";
import { RidersPayPage, StorefrontDeliveries } from "./pages/Dispatch";
import { StorefrontRiders } from "./pages/StorefrontRiders";
import { StorefrontStock } from "./pages/StorefrontStock";
import { StorefrontBookings } from "./pages/StorefrontBookings";
import { invoiceAccess } from "./lib/invoices";
import { InvoiceEdit, InvoiceMatch, InvoiceNew, InvoicePage, InvoiceSettingsPage, Invoices } from "./pages/Invoices";
import { MoneyEntries, MoneyFees, MoneyLeaks, MoneyOverview, MoneyStatements } from "./pages/Money";
import { SchoolClasses, SchoolFamilies, SchoolImport, SchoolPromotionPage, SchoolSettingsPage, SchoolStudentNew, SchoolStudentPage, Schools } from "./pages/Schools";
import { SchoolTermFees, SchoolTermInvoices } from "./pages/SchoolFees";
import { SchoolAccountsPage } from "./pages/SchoolAccounts";
import { SchoolPaymentsPage } from "./pages/SchoolPayments";
import { SchoolReceiptsPage } from "./pages/SchoolReceipts";
import { SchoolDebtorsPage } from "./pages/SchoolDebtors";
import { GateHome, SchoolClearancePage, SchoolGatePage } from "./pages/SchoolClearance";
import { SchoolOverviewPage } from "./pages/SchoolOverview";
import { showSchools } from "./lib/schools";
import { FormSubmissions, Forms } from "./pages/Forms";
import { FormRequestPage, FormRequests, MemberForm, type FormRequest } from "./pages/FormFill";
import { get } from "./api";
import { AgentApp, AgentWorkspace, useAgentSession } from "./pages/Agent";
import { Home } from "./pages/Home";
import { Bell, useBellCount } from "./Bell";
import { Toaster } from "./toast";
import { NotificationSettings } from "./pages/NotificationSettings";
import { PaymentsSettings } from "./pages/ReceiveMoney";
import { brandFile } from "./brand";
import { standalone, useInstall } from "./install";
import { PULL_AT, pullRefreshes, pullShown } from "./lib/pull";

export function App() {
  return (
    <AuthProvider>
      <BrowserRouter>
        <Shell />
        <Toaster />
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
          <button className="primary" disabled={act.busy} onClick={() => void act.run(loginWithPasskey, false)}>
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
  const agentSession = useAgentSession();
  // Signed-out pages are a single card; it is the page's main landmark.
  // Signed out: the brand on pine beside (on a phone, above) one card.
  const alone = (page: ReactNode) => (
    <div className="signed-out">
      <aside className="auth-side">
        <div className="auth-brand">
          <img src={brandFile("mark.svg")} alt="" width={40} height={40} />
          <img className="auth-wordmark" src={brandFile("wordmark-reversed.svg")} alt="Taskiem" height={22} />
        </div>
        <p className="auth-line">Orders, payments and approvals for your business, in one place.</p>
        <ul className="auth-points">
          <li>Send invoices and see when they are paid</li>
          <li>Take orders and bookings on your own page</li>
          <li>Approve payments from your phone</li>
        </ul>
      </aside>
      <main className="auth-main">{page}</main>
    </div>
  );
  // Password recovery works signed in or out.
  if (loc.pathname === "/forgot-password") return alone(<ForgotPassword />);
  if (loc.pathname === "/reset-password") return alone(<ResetPassword />);
  // An emailed invitation link, for someone with no account yet.
  if (loc.pathname === "/accept-invitation") return alone(<AcceptInvitation />);
  if (me === undefined) return alone(<Skeleton rows={3} />);
  if (me === null) {
    if (loc.pathname === "/signup") return alone(<Signup />);
    return loc.pathname === "/login" ? alone(<Login />) : <Navigate to={`/login?next=${encodeURIComponent(loc.pathname + loc.search + loc.hash)}`} replace />;
  }
  if (me.invitations_only) return alone(<InviteeHome />);
  if (me.enrol_passkey) return alone(<EnrolPasskey />);
  // The gate role: one screen, whatever the address; the server refuses every other route.
  if (me.gate_only) return <GateHome />;
  if (loc.pathname === "/login") return <Navigate to={new URLSearchParams(loc.search).get("next") || (me.field_agent ? "/agent" : "/home")} replace />;
  // A field agent's home is the agent app, not the workspace's workflows (N6).
  if (me.field_agent && !agentSession && (loc.pathname === "/" || loc.pathname === "/home" || loc.pathname === "/workflows")) return <Navigate to="/agent" replace />;
  if (loc.pathname === "/signup") return <Navigate to="/start" replace />;
  // Field agents (docs/agents.md): their own pages, and a business's page
  // and tables while they work in it.
  if (loc.pathname === "/agent" || loc.pathname.startsWith("/agent/")) return <AgentApp />;
  if (agentSession) return <AgentWorkspace session={agentSession} />;
  return (
    <OnboardingProvider>
      <SignedIn />
    </OnboardingProvider>
  );
}

function SignedIn() {
  const { me, can, logout } = useAuth();
  const onboarding = useOnboarding().data;
  // The drawer (narrow screens): closes on navigation and on Escape, keeps
  // Tab inside while open, and gives focus back to the menu button.
  const [drawer, setDrawer] = useState(false);
  const navRef = useRef<HTMLElement>(null);
  const mainRef = useRef<HTMLElement>(null);
  useScrollableTables(mainRef);
  const { pathname } = useLocation();
  useEffect(() => setDrawer(false), [pathname]);
  // A row of tabs wider than a phone scrolls sideways: bring the open one into view.
  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      for (const tabs of Array.from(mainRef.current?.querySelectorAll<HTMLElement>(".tabs") ?? [])) {
        const on = tabs.querySelector<HTMLElement>('.active, [aria-current="page"], [aria-selected="true"], [aria-checked="true"]');
        if (!on || tabs.scrollWidth <= tabs.clientWidth) continue;
        const box = tabs.getBoundingClientRect();
        const at = on.getBoundingClientRect();
        tabs.scrollLeft += at.left - box.left - (box.width - at.width) / 2;
      }
    });
    return () => cancelAnimationFrame(frame);
  }, [pathname]);
  // Widening the window past the drawer's breakpoint closes it.
  useEffect(() => {
    const wide = window.matchMedia("(min-width: 861px)");
    const close = () => wide.matches && setDrawer(false);
    wide.addEventListener("change", close);
    return () => wide.removeEventListener("change", close);
  }, []);
  useFocusTrap(navRef, drawer, () => setDrawer(false));
  // More's links stay as the reader left them, in this browser; until then
  // open for a business with Advanced on.
  const [moreOpen, setMoreOpenState] = useState(() => {
    try {
      const v = localStorage.getItem("taskiem-nav-more");
      return v ? v === "open" : !!me?.advanced;
    } catch {
      return !!me?.advanced;
    }
  });
  const setMoreOpen = (open: boolean) => {
    setMoreOpenState(open);
    try {
      localStorage.setItem("taskiem-nav-more", open ? "open" : "closed");
    } catch {
      // Storage blocked: open for this visit only.
    }
  };
  // Form steps waiting for someone with my roles: an entry while there are any.
  const [toFill, setToFill] = useState(0);
  useEffect(() => {
    let live = true;
    get<{ requests: FormRequest[] }>("/v1/form-requests").then(
      (r) => live && setToFill(r.requests.length),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [pathname]);
  // The bell: a person's notifications and approvals waiting (N12).
  const bell = useBellCount(!!me?.user, pathname);
  const install = useInstall();
  // Pulling down from the top of a page draws it again with fresh data.
  const [fresh, setFresh] = useState(0);
  if (!me) return null;
  // The sidebar: what a business uses every day, then everything else
  // under More (open while one of its pages is). [path, label, icon, visible, count].
  type Item = [string, string, string, boolean, string?];
  const daily: Item[] = [
    // For organisations that signed themselves up, until the checklist is
    // done or hidden (it stays at /start).
    ["/start", `Get started (${onboarding?.done ?? 0}/${onboarding?.total ?? 0})`, "start", !!onboarding?.self_serve && !onboarding.complete && !onboarding.dismissed],
    ["/home", "Home", "home", true],
    ["/workflows", "Workflows", "workflows", can("workflow.read")],
    ["/storefront/orders", "Orders", "orders", can("storefront.manage")],
    ["/invoices", "Invoices", "invoices", invoiceAccess(can).see],
    ["/money", "Money", "money", invoiceAccess(can).see],
    ["/schools", "Schools", "schools", showSchools(me.schools, can)],
    ["/storefront", "Storefront", "storefront", can("storefront.manage")],
    ["/forms", "Forms", "forms", can("workflow.read")],
    ["/forms/requests", "To fill in", "inbox", toFill > 0, String(toFill)],
    ["/runs", "Activity", "activity", can("run.read")],
    ["/tables", "Tables", "tables", can("table.read")],
    ["/approvals", "Approvals", "approvals", can("approval.decide") || can("workflow.publish"), bell.approvals > 0 ? String(bell.approvals) : undefined],
    ["/connections", "Connections", "connections", can("connection.manage")],
  ];
  const more: Item[] = [
    ["/dashboard", "Dashboard", "dashboard", can("run.read")],
    ["/templates", "Templates", "templates", true],
    ["/members", "Members & keys", "members", can("member.manage")],
    ["/settings/billing", "Billing", "billing", can("billing.manage")],
    ["/settings/payments", "Payments", "money", can("invoice.read") || me.roles.includes("owner")],
    ["/catalogue", "Connector catalogue", "catalogue", can("connector.manage") || can("workflow.read")],
    ["/settings", "Secrets & settings", "settings", can("secret.manage") || can("workflow.read")],
    ["/policies", "Approval policies", "policies", can("workflow.read")],
    ["/settings/notifications", "Notifications", "bell", !!me.user],
    ["/alerts", "Alerts", "alerts", can("alert.manage")],
    ["/audit", "Audit log", "audit", can("audit.read")],
    ["/reports", "Reports", "reports", can("audit.read")],
    ["/settings/keys", "Encryption keys", "keys", can("key.manage")],
  ].filter(([, , , ok]) => ok) as Item[];
  const inMore = more.some(([to]) => pathname === to || (to !== "/settings" && pathname.startsWith(to + "/")));
  const link = ([to, label, icon, , count]: Item) => (
    <NavLink key={to} to={to} end={to === "/settings" || to === "/forms" || to === "/storefront"}>
      <Icon name={icon} />
      {label}
      {count && <span className="count alert">{count}</span>}
    </NavLink>
  );
  // The tab bar on a phone: what an owner opens most, four at most, then Menu.
  const quick = (
    [
      ["/home", "Home", "home", true],
      ["/money", "Money", "money", invoiceAccess(can).see],
      ["/storefront/orders", "Orders", "orders", can("storefront.manage")],
      ["/workflows", "Workflows", "workflows", can("workflow.read")],
      ["/runs", "Activity", "activity", can("run.read")],
    ] as Item[]
  ).filter(([, , , ok]) => ok);
  const name = me.user?.name || me.user?.email || "API key";
  const business = me.tenant_name || "Taskiem";
  // The workflow builder fills the window: the menu shrinks to a rail of
  // icons on a computer, and the top and tab bars give way on a phone.
  const building = /^\/workflows\/[^/]+$/.test(pathname) && pathname !== "/workflows/new";
  return (
    <div className={building ? "shell builder-mode" : "shell"}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      {/* Narrow screens: a top bar, a tab bar, and the sidebar as a drawer. */}
      <header className="topbar" inert={drawer || undefined}>
        <span className="brand">
          <img className="brand-mark" src={brandFile("mark.svg")} alt="" width={26} height={26} />
          <span>{business}</span>
        </span>
        {me.user && <Bell count={bell} onChange={bell.reload} />}
      </header>
      {drawer && <div className="nav-scrim" aria-hidden="true" onClick={() => setDrawer(false)} />}
      <nav id="main-nav" ref={navRef} className={`nav drawer${drawer ? " open" : ""}`} aria-label="Main" tabIndex={-1}>
        <div className="brand">
          <img className="brand-mark" src={brandFile("mark.svg")} alt="" width={26} height={26} />
          <span className="brand-name">
            <strong>{business}</strong>
            <span>Taskiem</span>
          </span>
          {me.user && (
            <span className="nav-bell">
              <Bell count={bell} onChange={bell.reload} />
            </span>
          )}
          <button className="icon-button nav-close" aria-label="Close menu" onClick={() => setDrawer(false)}>
            <Icon name="close" />
          </button>
        </div>
        {daily.filter(([, , , ok]) => ok).map(link)}
        {more.length > 0 && (
          <>
            <button type="button" className="nav-toggle" aria-expanded={moreOpen || inMore} aria-controls="nav-more" onClick={() => setMoreOpen(!(moreOpen || inMore))}>
              <Icon name="more" />
              More
              <svg className="chev" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
                <path d="M9 6l6 6-6 6" />
              </svg>
            </button>
            <div id="nav-more" hidden={!(moreOpen || inMore)} style={{ display: moreOpen || inMore ? "contents" : undefined }}>
              {more.map(link)}
            </div>
          </>
        )}
        {install && (
          <button type="button" className="nav-toggle nav-install" onClick={() => void install()}>
            <Icon name="plus" />
            Install the app
          </button>
        )}
        <div className="spacer" />
        <div className="who">
          <span className="avatar" aria-hidden="true">
            {initials(name)}
          </span>
          <div>
            <NavLink to="/account">{name}</NavLink>
            <div className="roles">{me.roles.join(", ")}</div>
          </div>
          <button className="signout" onClick={() => void logout()}>
            Sign out
          </button>
        </div>
      </nav>
      <main className={building ? "main wide" : "main"} id="main" tabIndex={-1} ref={mainRef} inert={drawer || undefined}>
        <BillingBanner />
        <div className="page" key={`${pathname}#${fresh}`}>
        <PageBoundary>
        <Routes>
          <Route path="/" element={<Navigate to="/home" replace />} />
          <Route path="/home" element={<Home />} />
          <Route path="/workflows" element={<Workflows />} />
          <Route path="/workflows/new" element={<NewWorkflow />} />
          <Route path="/workflows/:id" element={<Editor />} />
          <Route path="/templates" element={<Templates />} />
          <Route path="/tables" element={<Tables />} />
          <Route path="/tables/:id" element={<TablePage />} />
          <Route path="/storefront" element={<Storefront />} />
          <Route path="/storefront/look" element={<Look />} />
          <Route path="/storefront/orders" element={<StorefrontOrders />} />
          <Route path="/storefront/orders/:reference" element={<StorefrontOrder />} />
          <Route path="/storefront/bookings" element={<StorefrontBookings />} />
          <Route path="/storefront/riders" element={<StorefrontRiders />} />
          <Route path="/storefront/riders/pay" element={<RidersPayPage />} />
          <Route path="/storefront/deliveries" element={<StorefrontDeliveries />} />
          <Route path="/storefront/stock" element={<StorefrontStock />} />
          <Route path="/invoices" element={<Invoices />} />
          <Route path="/invoices/new" element={<InvoiceNew />} />
          <Route path="/invoices/settings" element={<InvoiceSettingsPage />} />
          <Route path="/invoices/match" element={<InvoiceMatch />} />
          <Route path="/invoices/:id" element={<InvoicePage />} />
          <Route path="/invoices/:id/edit" element={<InvoiceEdit />} />
          <Route path="/money" element={<MoneyOverview />} />
          <Route path="/money/leaks" element={<MoneyLeaks />} />
          <Route path="/money/statements" element={<MoneyStatements />} />
          <Route path="/money/entries" element={<MoneyEntries />} />
          <Route path="/money/fees" element={<MoneyFees />} />
          <Route path="/schools" element={<Schools />} />
          <Route path="/schools/import" element={<SchoolImport />} />
          <Route path="/schools/students/new" element={<SchoolStudentNew />} />
          <Route path="/schools/students/:id" element={<SchoolStudentPage />} />
          <Route path="/schools/families" element={<SchoolFamilies />} />
          <Route path="/schools/classes" element={<SchoolClasses />} />
          <Route path="/schools/sessions/:id/promotion" element={<SchoolPromotionPage />} />
          <Route path="/schools/terms/:id/fees" element={<SchoolTermFees />} />
          <Route path="/schools/terms/:id/invoices" element={<SchoolTermInvoices />} />
          <Route path="/schools/accounts" element={<SchoolAccountsPage />} />
          <Route path="/schools/payments" element={<SchoolPaymentsPage />} />
          <Route path="/schools/receipts" element={<SchoolReceiptsPage />} />
          <Route path="/schools/debtors" element={<SchoolDebtorsPage />} />
          <Route path="/schools/clearance" element={<SchoolClearancePage />} />
          <Route path="/schools/overview" element={<SchoolOverviewPage />} />
          <Route path="/schools/gate" element={<SchoolGatePage />} />
          <Route path="/settings/schools" element={<SchoolSettingsPage />} />
          <Route path="/forms" element={<Forms />} />
          <Route path="/forms/requests" element={<FormRequests />} />
          <Route path="/forms/requests/:id" element={<FormRequestPage />} />
          <Route path="/forms/:id" element={<MemberForm />} />
          <Route path="/forms/:id/submissions" element={<FormSubmissions />} />
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
          <Route path="/settings/payments" element={<PaymentsSettings />} />
          <Route path="/settings/keys" element={<Keys />} />
          <Route path="/settings/notifications" element={<NotificationSettings />} />
          <Route path="/alerts" element={<Alerts />} />
          <Route path="/audit" element={<Audit />} />
          <Route path="/members" element={<Members />} />
          <Route path="/policies" element={<Policies />} />
          <Route path="/account" element={<Account />} />
          <Route path="/handoff" element={<Handoff />} />
          <Route path="/reports" element={<Reports />} />
          <Route path="*" element={<EmptyState icon="search">There is no page at this address.</EmptyState>} />
        </Routes>
        </PageBoundary>
        </div>
      </main>
      <PullToRefresh
        onRefresh={() => {
          setFresh((n) => n + 1);
          bell.reload();
        }}
      />
      <nav className="tabbar" aria-label="Quick" inert={drawer || undefined}>
        {quick.slice(0, 4).map(([to, label, icon]) => (
          <NavLink key={to} to={to}>
            <span className="tab-icon">
              <Icon name={icon} />
            </span>
            {label}
          </NavLink>
        ))}
        <button type="button" aria-label="Open menu" aria-expanded={drawer} aria-controls="main-nav" onClick={() => setDrawer(true)}>
          <span className="tab-icon">
            <Icon name="menu" />
          </span>
          Menu
        </button>
      </nav>
    </div>
  );
}

/** Up to two initials for an avatar: "Ada Obi" is AO, "ada@x.ng" is AD. */
export function initials(name: string): string {
  const [a = "", b = ""] = name.includes("@") ? [] : name.split(/\s+/).filter(Boolean);
  return (a && b ? a.charAt(0) + b.charAt(0) : name.slice(0, 2)).toUpperCase();
}

/** Pull to refresh, in the installed app on a phone (a browser tab has its
 * own): pulling down from the top of a page past the mark draws the page
 * again, which loads its data afresh. */
function PullToRefresh({ onRefresh }: { onRefresh: () => void }) {
  const [on, setOn] = useState(false);
  const [shown, setShown] = useState(0);
  const [busy, setBusy] = useState(false);
  const refresh = useRef(onRefresh);
  refresh.current = onRefresh;
  useEffect(() => {
    if (!standalone() || typeof window.matchMedia !== "function" || !window.matchMedia("(pointer: coarse)").matches) return;
    setOn(true);
    let start: number | null = null;
    let dy = 0;
    const down = (e: TouchEvent) => {
      // Only from the very top, one finger, and not over a dialog or the canvas.
      const t = e.target instanceof Element ? e.target : null;
      start = window.scrollY <= 0 && e.touches.length === 1 && !t?.closest(".modal, .nav.drawer, .canvas, .bell-panel") ? (e.touches[0]?.clientY ?? null) : null;
      dy = 0;
    };
    const move = (e: TouchEvent) => {
      const y = e.touches[0]?.clientY;
      if (start === null || y === undefined) return;
      dy = y - start;
      if (dy <= 0 || window.scrollY > 0) {
        start = null;
        setShown(0);
        return;
      }
      setShown(pullShown(dy));
    };
    const up = () => {
      if (start !== null && pullRefreshes(dy)) {
        setBusy(true);
        refresh.current();
        window.setTimeout(() => setBusy(false), 700);
      }
      start = null;
      setShown(0);
    };
    window.addEventListener("touchstart", down, { passive: true });
    window.addEventListener("touchmove", move, { passive: true });
    window.addEventListener("touchend", up);
    window.addEventListener("touchcancel", up);
    return () => {
      window.removeEventListener("touchstart", down);
      window.removeEventListener("touchmove", move);
      window.removeEventListener("touchend", up);
      window.removeEventListener("touchcancel", up);
    };
  }, []);
  // Nothing in a browser tab; in the installed app, a status for "Refreshing".
  if (!on) return null;
  if (!busy && shown === 0) return <span className="sr-only" role="status" />;
  const at = busy ? PULL_AT : shown;
  return (
    <>
      <div className={`pull${busy ? " busy" : at >= PULL_AT ? " ready" : ""}`} aria-hidden="true" style={{ transform: `translateY(${at - 40}px)`, opacity: Math.min(1, at / PULL_AT) }}>
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={2} strokeLinecap="round" style={{ transform: busy ? undefined : `rotate(${(at / PULL_AT) * 270}deg)` }}>
          <path d="M20 12a8 8 0 11-2.3-5.6M20 4v4h-4" />
        </svg>
      </div>
      <span className="sr-only" role="status">
        {busy ? "Refreshing" : ""}
      </span>
    </>
  );
}
