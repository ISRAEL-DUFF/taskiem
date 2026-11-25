import { useEffect, useRef, useState } from "react";
import { Link, Navigate, NavLink, Route, Routes, useLocation, useNavigate, useParams } from "react-router-dom";
import { post, type Business, type InvitationAccepted, type Lock } from "../api";
import { useAuth } from "../auth";
import { brandFile } from "../brand";
import { Icon } from "../icons";
import { useWorkerGo } from "../lib/pushOpen";
import { ErrorBox, PageHeader, Skeleton, useAction } from "../ui";
import { Appearance, Invitations } from "./Account";
import { PhoneSetting } from "./ChangeNumber";
import { ChatLinkPage } from "./ChatLinkPage";
import { Discover, DiscoverBusiness } from "./Discover";
import { PersonChatList, PersonChatPage, StartChat } from "./PersonChats";
import { BlockedPage, ContactsPage, PayoutPage, PeoplePrivacyPage, PersonLinkPage, RequestsPage, YouPeopleSettings } from "./People";
import { PeopleChatPage } from "./PeopleChat";
import { GroupChatPage } from "./GroupChat";
import { GroupInfoPage, GroupLinkPage } from "./Groups";
import { PeopleLiveProvider } from "./PeopleLive";
import { PeopleSearchPage, PeopleStarredPage } from "./PeopleSearch";
import { TermsBanner } from "./Signup";
import { StartSomething } from "./StartSomething";
import { ProofStep, SwitchList, Switcher, SwitchStep as SwitchStepHere, useSwitch } from "./Switch";
import { StandingNotice, YouStandingPage } from "./YouStanding";
import { DeleteAccountPage, YouNotificationsCard, YouPhoto, YouPrivacyLinks, YouPrivacyPage } from "./YouAccount";
import { SavedAddresses, YouOrder, YouOrders } from "./YouOrders";
import { SavedCard } from "./Saved";
import { SignInWayCallout } from "./SignInWay";

// You (docs/spec/taskiem-for-everyone.md §7): a person's own side, where a
// session with no business lands. Its tabs: Chats (chats with businesses,
// Start something, invitations waiting), Orders (§13), Discover (§10) and
// the person's own settings (with their saved addresses). A person in a
// business switches to it from the top.

/** The frame of the person's own side: the top bar, the tabs and the page. */
export function YouApp() {
  const { me } = useAuth();
  const nav = useNavigate();
  const loc = useLocation();
  // A notification tapped while this window is open: the worker says where.
  useWorkerGo();
  if (!me) return null;
  const inBusiness = (me.businesses?.length ?? 0) > 0;
  // A conversation fills the window, as the chat does (§9).
  const inChat = /^\/you\/chats\/(?!new\b)[^/]+/.test(loc.pathname) || inPeopleChat(loc.pathname) || inGroup(loc.pathname);
  return (
    <PeopleLiveProvider on={!!me.user}>
    <div className={`you-shell${inChat ? " you-chat" : ""}`}>
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <header className="you-top">
        <img className="brand-mark" src={brandFile("mark.svg")} alt="" width={26} height={26} />
        {inBusiness ? <Switcher onStart={() => nav("/you/start")} /> : <span className="you-name">Taskiem</span>}
        <nav className="tabs you-tabs" aria-label="You">
          <NavLink to="/you" end>
            Chats
          </NavLink>
          <NavLink to="/you/orders">Orders</NavLink>
          <NavLink to="/you/discover">Discover</NavLink>
          <NavLink to="/you/settings">Settings</NavLink>
        </nav>
      </header>
      <main className="you-main" id="main" tabIndex={-1}>
        <TermsBanner />
        <StandingNotice />
        <Routes>
          <Route path="/you" element={<YouHome />} />
          <Route path="/you/orders" element={<YouOrders />} />
          <Route path="/you/orders/:business/:reference" element={<YouOrder />} />
          <Route path="/you/discover" element={<Discover />} />
          <Route path="/you/discover/:business" element={<DiscoverBusiness />} />
          <Route path="/you/settings" element={<YouSettings />} />
          <Route path="/you/settings/privacy" element={<YouPrivacyPage />} />
          <Route path="/you/settings/delete" element={<DeleteAccountPage />} />
          <Route path="/you/settings/standing" element={<YouStandingPage />} />
          <Route path="/you/start" element={<StartSomething />} />
          <Route path="/you/chats/new" element={<StartChat />} />
          <Route path="/you/chats/:id" element={<PersonChatRoute />} />
          <Route path="/you/people/requests" element={<RequestsPage />} />
          <Route path="/you/people/contacts" element={<ContactsPage />} />
          <Route path="/you/people/search" element={<PeopleSearchPage />} />
          <Route path="/you/people/starred" element={<PeopleStarredPage />} />
          <Route path="/you/people/requests/:id" element={<PeopleChatRoute />} />
          <Route path="/you/people/:id" element={<PeopleChatRoute />} />
          <Route path="/you/groups/invites" element={<Navigate to="/you/people/requests" replace />} />
          <Route path="/you/groups/:id/info" element={<GroupInfoRoute />} />
          <Route path="/you/groups/:id" element={<GroupChatRoute />} />
          <Route path="/g/:code" element={<GroupLinkPage />} />
          <Route path="/you/settings/people" element={<PeoplePrivacyPage />} />
          <Route path="/you/settings/blocked" element={<BlockedPage />} />
          <Route path="/you/settings/payout" element={<PayoutPage />} />
          <Route path="/m/:code" element={<ChatLinkPage />} />
          <Route path="/p/:code" element={<PersonLinkPage />} />
          <Route path="/second-proof" element={<SecondProofPage />} />
          <Route path="*" element={<Navigate to="/you" replace />} />
        </Routes>
      </main>
    </div>
    </PeopleLiveProvider>
  );
}

/** A conversation, made afresh for each chat: nothing of one (its messages,
 * a sheet, a send under way) is shown in the next. */
function PersonChatRoute() {
  const { id = "" } = useParams();
  return <PersonChatPage key={id} />;
}

/** A chat with a person (or a request) fills the window, as a business's does. */
export const inPeopleChat = (path: string) => /^\/you\/people\/(requests\/)?(?!requests$|contacts$|search$|starred$)[^/]+$/.test(path);

/** A group fills the window too (not its info). */
export const inGroup = (path: string) => /^\/you\/groups\/(?!invites$)[^/]+$/.test(path);

/** A group, made afresh for each group. */
function GroupChatRoute() {
  const { id = "" } = useParams();
  return <GroupChatPage key={id} />;
}

function GroupInfoRoute() {
  const { id = "" } = useParams();
  return <GroupInfoPage key={id} />;
}

/** A chat with a person, made afresh for each chat. */
function PeopleChatRoute() {
  const { id = "" } = useParams();
  return <PeopleChatPage key={id} />;
}

/** /you/chats/{id} opened in a business's session (a tapped notification
 * for the person's own chat): the chat is on You, so the person moves there,
 * by their own tap. */
export function ChatOnYou() {
  const { id = "" } = useParams();
  const sw = useSwitch(`/you/chats/${encodeURIComponent(id)}`);
  return (
    <section className="card" data-testid="chat-on-you">
      <h1>A chat of your own</h1>
      <p>This chat is between you and a business, on your own side of Taskiem (You), not in this business.</p>
      <button type="button" className="primary" disabled={sw.busy} onClick={() => sw.go("you")} data-testid="open-on-you">
        Open it on You
      </button>
      <ErrorBox error={sw.error} />
    </section>
  );
}

/** The chat list's place, Start something, and what waits for the person. */
function YouHome() {
  const { me, refresh } = useAuth();
  const [proving, setProving] = useState(false);
  // An invitation accepted whose business asks for a step first (§5.4).
  const [joining, setJoining] = useState<{ business: Business; lock: Lock; accepted: InvitationAccepted } | null>(null);
  const first = me?.user?.name?.trim().split(/\s+/)[0];
  return (
    <>
      <PageHeader
        title={first ? `Hello, ${first}` : "You"}
        actions={
          <Link className="button primary" to="/you/start" data-testid="start-something">
            <Icon name="plus" /> Start something
          </Link>
        }
      />
      {me?.second_proof?.required && (
        <div className="notice" role="status" data-testid="businesses-locked">
          You signed in on a new device, so your businesses are locked until you prove it's you.{" "}
          <button type="button" className="small" onClick={() => setProving(true)}>
            Prove it's you
          </button>
        </div>
      )}
      {proving && me?.second_proof && (
        <ProofStep
          ways={me.second_proof.ways}
          onClose={() => setProving(false)}
          onProved={async () => {
            setProving(false);
            await refresh();
          }}
        />
      )}
      <Invitations
        onJoined={(r, inv) => {
          const lock = r.sign_in && r.sign_in !== "session_ended" ? r.sign_in : null;
          // Moved into the business: it opens on its start ("/" knows a workspace's).
          if (!lock) return window.location.assign(r.sign_in === "session_ended" ? "/login" : "/");
          setJoining({ business: { id: inv.tenant_id, name: inv.tenant }, lock, accepted: r });
          void refresh();
        }}
      />
      {joining && (
        <SwitchStepHere
          asked={{
            business: joining.business,
            lock: joining.lock,
            ways: joining.accepted.ways ?? me?.second_proof?.ways ?? { passkey: false, email: false, password: false },
          }}
          dest="/"
          onClose={() => setJoining(null)}
        />
      )}
      <YouSignInWay />
      <PersonChatList />
      <YourBusinesses />
    </>
  );
}

/** The reminder to add a way to sign in on a new phone (SignInWay.tsx):
 * on You nothing can be added, so its button opens the person's business
 * at Account, where they add it. */
function YouSignInWay() {
  const { me } = useAuth();
  const sw = useSwitch("/account#sign-in-way");
  const open = me?.businesses?.find((b) => !b.locked);
  if (!me?.user?.needs_sign_in_way) return null;
  return (
    <>
      <SignInWayCallout
        action={
          open && (
            <button type="button" className="small primary" disabled={sw.busy} onClick={() => sw.go(open)}>
              Add a way
            </button>
          )
        }
      />
      <ErrorBox error={sw.error} />
      {sw.asked && <SwitchStepHere asked={sw.asked} onClose={sw.close} />}
    </>
  );
}

/** The person's businesses as rows, the same list as the switcher's. */
function YourBusinesses() {
  const { me } = useAuth();
  const sw = useSwitch();
  if (!me?.businesses?.length) return null;
  return (
    <section className="card" aria-labelledby="you-businesses">
      <h2 id="you-businesses">Your businesses</h2>
      <SwitchList me={me} onPick={sw.go} busy={sw.busy} hideYou />
      <ErrorBox error={sw.error} />
      {sw.asked && <SwitchStepHere asked={sw.asked} onClose={sw.close} />}
    </section>
  );
}

/** The person's own settings: who they are, their phone, how it looks. */
function YouSettings() {
  const { me, logout } = useAuth();
  const email = me?.user?.email_placeholder ? null : me?.user?.email;
  return (
    <>
      <PageHeader title="Settings" />
      <section className="card" aria-labelledby="you-account">
        <h2 id="you-account">Your account</h2>
        <YouPhoto />
        <div className="setting-line">
          <div className="grow">
            <strong>Name</strong>
            <div className="sub">{me?.user?.name || "No name yet"}</div>
          </div>
        </div>
        <PhoneSetting />
        <div className="setting-line">
          <div className="grow">
            <strong>Email</strong>
            <div className="sub">
              {email ? `${email}${me?.user?.email_verified ? "" : " (not confirmed yet)"}` : "No email yet. You can add one once you start or join a business."}
            </div>
          </div>
        </div>
      </section>
      <SavedAddresses />
      <SavedCard mine />
      <YouPeopleSettings />
      <YouNotificationsCard />
      <Appearance />
      <section className="card">
        <h2>Signing out</h2>
        <p className="hint">Sign out of Taskiem on this device.</p>
        <button type="button" onClick={() => void logout()}>
          Sign out
        </button>
      </section>
      <YouPrivacyLinks />
    </>
  );
}

/** The link a second proof sends by email: it proves this device, then
 * the person's businesses open. Read from the fragment, never sent on. */
export function SecondProofPage() {
  const [token] = useState(() => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "");
  const { me, refresh } = useAuth();
  const [done, setDone] = useState(false);
  const act = useAction();
  const sw = useSwitch();
  const tried = useRef(false);
  useEffect(() => {
    if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
    if (!token || tried.current) return;
    tried.current = true;
    void act.run(async () => {
      await post("/v1/auth/second-proof/email/verify", { token });
      setDone(true);
      await refresh();
    }, false);
  }, [token]);
  return (
    <>
      <PageHeader title="Prove it's you" />
      <section className="card" data-testid="second-proof-page">
        {!token && <p className="error">This page needs the link from your email.</p>}
        {act.busy && <Skeleton rows={1} label="Checking the link…" />}
        <ErrorBox error={act.error} />
        {done && me && (
          <>
            <p role="status">Done: this device is yours now. Your businesses are open to you here.</p>
            <SwitchList me={me} onPick={sw.go} busy={sw.busy} hideYou={!me.personal} />
            <ErrorBox error={sw.error} />
            {sw.asked && <SwitchStepHere asked={sw.asked} onClose={sw.close} />}
          </>
        )}
      </section>
    </>
  );
}

/** The second proof's link opened where nobody is signed in: it proves
 * only the browser that asked for it, so it is not used here, and its
 * token is dropped from the address at once. */
export function SecondProofElsewhere() {
  useState(() => {
    if (window.location.hash) window.history.replaceState(null, "", window.location.pathname);
    return null;
  });
  return (
    <div className="login card" data-testid="second-proof-elsewhere">
      <h1>Prove it's you</h1>
      <p>Open this link in the browser where you asked for it.</p>
      <p className="hint">
        It confirms the device that asked for it. To sign in here, <Link to="/login">sign in with your phone number</Link>.
      </p>
    </div>
  );
}
