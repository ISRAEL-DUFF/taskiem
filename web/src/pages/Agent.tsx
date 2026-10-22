// The field agents' app (/agent, docs/agents.md): an agent signs a business
// up with its owner beside them, works on its page in a session the owner
// agreed to, and records the visit. Made for cheap phones on slow networks:
// one column, big buttons, few requests.

import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Link, Navigate, NavLink, Route, Routes, useLocation, useNavigate, useParams, type NavigateFunction } from "react-router-dom";
import { ApiError, currentAgentSession, get, post, setAgentSession, subscribeAgentSession, uploadFile } from "../api";
import { AgentPermissions, useAuth } from "../auth";
import { AGENT_PERMISSIONS, attestPath, consentChannels, endReason, fitWithin, formatLeft, msLeft, type AgentSession, type ConsentChannel } from "../lib/agent";
import { Badge, ErrorBox, Field, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Storefront } from "./Storefront";
import { Look } from "./Look";
import { TablePage, Tables } from "./Tables";

interface AgentMe {
  id: string;
  code: string;
  name: string;
  status: string;
  region: string;
}

interface SessionRow {
  id: string;
  tenant_id: string;
  business: string;
  kind: "onboarding" | "support";
  status: "pending" | "active" | "ended";
  consent_at?: string | null;
  expires_at: string;
  ended_at?: string | null;
  end_reason?: string | null;
  created_at?: string;
}

interface Consent {
  code: string;
  text: string;
  link?: string;
  /** The link as a QR code (a PNG data URL). */
  qr?: string;
  whatsapp_number?: string;
  /** Opens Taskiem's Telegram bot with the code; the bot asks the owner to share their number. */
  telegram_link?: string;
  /** The Telegram link as a QR code (a PNG data URL). */
  telegram_qr?: string;
  expires_at: string;
}

/** What the agent home shows on arrival: why the agent came back. */
interface HomeState {
  message?: string;
  handedOver?: string;
}

export function useAgentSession(): AgentSession | null {
  return useSyncExternalStore(subscribeAgentSession, currentAgentSession);
}

/** The time now, ticking every `every` milliseconds. */
function useNow(every: number): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), every);
    return () => clearInterval(t);
  }, [every]);
  return now;
}

// Visits recorded from this phone, so the banner stops asking for one.
const ATTESTED_KEY = "taskiem.agent-attested";

function attested(id: string): boolean {
  try {
    return (JSON.parse(localStorage.getItem(ATTESTED_KEY) ?? "[]") as string[]).includes(id);
  } catch {
    return false;
  }
}

function markAttested(id: string) {
  try {
    const ids = (JSON.parse(localStorage.getItem(ATTESTED_KEY) ?? "[]") as string[]).filter((x) => x !== id).slice(-49);
    localStorage.setItem(ATTESTED_KEY, JSON.stringify([...ids, id]));
  } catch {
    // storage blocked: the banner keeps reminding
  }
}

/** Opens the business's page in the session, if the session is live. */
async function enterSession(id: string, navigate: NavigateFunction) {
  const { session } = await get<{ session: SessionRow }>(`/v1/agent/sessions/${id}`);
  if (session.status !== "active") throw new Error(session.status === "pending" ? "The owner has not agreed yet." : "This session has ended.");
  setAgentSession({ id: session.id, business: session.business, kind: session.kind, expires_at: session.expires_at });
  navigate("/storefront");
}

/** The agent's own pages: home, a new business, the owner's consent and the visit. */
export function AgentApp() {
  const { logout } = useAuth();
  // On the agent's own pages they are not in any business.
  useEffect(() => setAgentSession(null), []);
  return (
    <div className="agent-app">
      <header className="agent-top">
        <Link to="/agent" className="brand">
          <img className="brand-mark" src="/brand/taskiem-mark.svg" alt="" width={26} height={26} />
          Taskiem agent
        </Link>
        <button onClick={() => void logout()}>Sign out</button>
      </header>
      <main id="main">
        <Routes>
          <Route path="/agent" element={<AgentHome />} />
          <Route path="/agent/new" element={<NewBusiness />} />
          <Route path="/agent/sessions/:id" element={<SessionPage />} />
          <Route path="/agent/sessions/:id/visit" element={<RecordVisit />} />
          <Route path="*" element={<Navigate to="/agent" replace />} />
        </Routes>
      </main>
    </div>
  );
}

function AgentHome() {
  const navigate = useNavigate();
  const state = (useLocation().state ?? {}) as HomeState;
  const me = useLoad(() => get<{ agent: AgentMe }>("/v1/agent/me"), []);
  const sessions = useLoad(() => get<{ sessions: SessionRow[] }>("/v1/agent/sessions"), []);
  const act = useAction();
  if (me.error instanceof ApiError && me.error.status === 403) {
    return (
      <section className="card">
        <h1>Not a field agent</h1>
        <p>{me.error.message.charAt(0).toUpperCase() + me.error.message.slice(1)}.</p>
        <p className="hint">Field agents are registered by Taskiem after an in-person check. If you work for a business, use the main app instead.</p>
        <Link className="button" to="/workflows">
          Go to the main app
        </Link>
      </section>
    );
  }
  if (me.error) return <ErrorBox error={me.error} />;
  if (!me.data) return <Skeleton rows={3} label="Loading…" />;
  const a = me.data.agent;
  const list = sessions.data?.sessions ?? [];
  return (
    <>
      {state.handedOver && (
        <div className="notice agent-done" role="status">
          <strong>Handed over.</strong> {state.handedOver} is live. The owner has a WhatsApp message with the page's address and how to change it.
        </div>
      )}
      {state.message && (
        <div className="notice" role="status">
          {state.message}
        </div>
      )}
      <section className="card">
        <h1 style={{ marginBottom: 4 }}>{a.name}</h1>
        <p className="hint" style={{ margin: 0 }}>
          Agent <strong>{a.code}</strong>
          {a.region ? ` · ${a.region}` : ""}
        </p>
      </section>
      <Link className="button primary agent-big" to="/agent/new">
        New business
      </Link>
      <h2>My businesses</h2>
      <ErrorBox error={sessions.error ?? act.error} />
      {!sessions.data && !sessions.error && <Skeleton rows={2} />}
      {sessions.data && list.length === 0 && <p className="hint">None yet. Sign up your first business with its owner beside you.</p>}
      <ul className="agent-list">
        {list.map((s) => (
          <li key={s.id} className="card">
            <div className="agent-row">
              <strong>{s.business}</strong>
              <Badge value={s.status} />
            </div>
            <div className="hint">
              {s.kind === "support" ? "Support visit" : "Sign-up"} ·{" "}
              {s.status === "ended" ? `${endReason(s.end_reason)}, ${fmtTime(s.ended_at ?? s.expires_at)}` : `until ${fmtTime(s.expires_at)}`}
            </div>
            {s.status === "pending" && (
              <Link className="button" to={`/agent/sessions/${s.id}`}>
                Show the owner's code
              </Link>
            )}
            {s.status === "active" && (
              <div className="toolbar" style={{ margin: "8px 0 0" }}>
                <button className="primary" disabled={act.busy} onClick={() => void act.run(() => enterSession(s.id, navigate), false)}>
                  Work on the page
                </button>
                {s.kind === "onboarding" && (
                  <Link className="button" to={`/agent/sessions/${s.id}/visit`}>
                    Record visit
                  </Link>
                )}
              </div>
            )}
          </li>
        ))}
      </ul>
    </>
  );
}

function NewBusiness() {
  const navigate = useNavigate();
  const [name, setName] = useState("");
  const [owner, setOwner] = useState("");
  const [phone, setPhone] = useState("");
  const act = useAction();
  return (
    <section className="card">
      <h1>New business</h1>
      <p className="hint">Fill this in with the owner beside you. They agree from their own WhatsApp next.</p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            const out = await post<{ session: SessionRow; consent: Consent }>("/v1/agent/businesses", { name: name.trim(), owner_name: owner.trim(), owner_phone: phone.trim() });
            navigate(`/agent/sessions/${out.session.id}`, { state: { consent: out.consent } });
          }, false);
        }}
      >
        <Field label="Business name">
          <input value={name} onChange={(e) => setName(e.target.value)} maxLength={100} required autoComplete="off" />
        </Field>
        <Field label="Owner's name">
          <input value={owner} onChange={(e) => setOwner(e.target.value)} maxLength={100} required autoComplete="off" />
        </Field>
        <Field label="Owner's WhatsApp number" hint="Such as 0803 123 4567.">
          <input type="tel" inputMode="tel" value={phone} onChange={(e) => setPhone(e.target.value)} required autoComplete="off" />
        </Field>
        <ErrorBox error={act.error} />
        <button type="submit" className="primary agent-big" disabled={act.busy}>
          {act.busy ? "Signing up…" : "Sign up"}
        </button>
      </form>
      <Link to="/agent">Back</Link>
    </section>
  );
}

/** The owner's consent: the code to send from their WhatsApp or Telegram, until they do. */
function SessionPage() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const loc = useLocation();
  const [consent, setConsent] = useState<Consent | undefined>((loc.state as { consent?: Consent } | null)?.consent);
  const [session, setSession] = useState<SessionRow>();
  const [error, setError] = useState<unknown>();
  const sawPending = useRef(false);
  const act = useAction();
  const now = useNow(1000);
  const [channel, setChannel] = useState<ConsentChannel>("whatsapp");

  // Ask every 3 seconds while the owner has not agreed.
  const pending = !session || session.status === "pending";
  useEffect(() => {
    if (!pending) return;
    let live = true;
    const load = () =>
      get<{ session: SessionRow }>(`/v1/agent/sessions/${id}`).then(
        (r) => {
          if (!live) return;
          setSession(r.session);
          setError(undefined);
        },
        (e: unknown) => live && setError(e),
      );
    void load();
    const t = setInterval(() => document.visibilityState === "visible" && void load(), 3000);
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [id, pending]);

  // Agreed while the agent watched: straight to the page.
  useEffect(() => {
    if (session?.status === "pending") sawPending.current = true;
    if (session?.status === "active" && sawPending.current) void act.run(() => enterSession(id, navigate), false);
    // Only on the status changing: once, on agreeing.
  }, [session?.status]);

  if (!session) return error ? <ErrorBox error={error} /> : <Skeleton rows={3} label="Loading…" />;

  const back = <Link to="/agent">Back to my businesses</Link>;
  if (session.status === "ended") {
    return (
      <section className="card">
        <h1>{session.business}</h1>
        <p>This session has ended.</p>
        {back}
      </section>
    );
  }
  if (session.status === "active") {
    return (
      <section className="card">
        <h1>{session.business}</h1>
        <div className="notice" role="status">
          The owner agreed. You may work on the page until {fmtTime(session.expires_at)}.
        </div>
        <ErrorBox error={act.error} />
        <button className="primary agent-big" disabled={act.busy} onClick={() => void act.run(() => enterSession(id, navigate), false)}>
          Work on the page
        </button>
        {back}
      </section>
    );
  }

  const left = consent ? msLeft(consent.expires_at, now) : 0;
  const channels = consent ? consentChannels(consent) : (["whatsapp"] as ConsentChannel[]);
  const shown: ConsentChannel = channels.includes(channel) ? channel : (channels[0] ?? "whatsapp");
  const newCode = () =>
    act.run(async () => {
      const out = await post<{ session: SessionRow; consent: Consent }>(`/v1/agent/sessions/${id}/code`);
      setConsent(out.consent);
      setSession(out.session);
    }, false);
  return (
    <section className="card">
      <h1>{session.business}</h1>
      {channels.length > 1 && (
        <div className="tabs agent-channels" role="group" aria-label="Where the owner agrees">
          {channels.map((c) => (
            <button key={c} type="button" className={shown === c ? "active" : ""} aria-pressed={shown === c} onClick={() => setChannel(c)}>
              {c === "whatsapp" ? "WhatsApp" : "Telegram"}
            </button>
          ))}
        </div>
      )}
      <p>
        {shown === "whatsapp"
          ? "Ask the owner to send this message from their own WhatsApp:"
          : "Ask the owner to open Taskiem on Telegram, on their own phone, with this code:"}
      </p>
      {consent && left > 0 ? (
        <>
          <div className="agent-code" aria-label={`Code ${consent.code.split("").join(" ")}`}>
            {consent.code}
          </div>
          {shown === "whatsapp" ? (
            <>
              <p className="agent-text">
                <span className="hint">Message</span>
                <strong>{consent.text}</strong>
                {consent.whatsapp_number && (
                  <>
                    <span className="hint">To</span>
                    <strong>{consent.whatsapp_number}</strong>
                  </>
                )}
              </p>
              {consent.qr && (
                <img className="agent-qr" src={consent.qr} width={240} height={240} alt="QR code: the owner scans it with their own phone to send the message" />
              )}
              {consent.link && (
                <>
                  <a className="button primary agent-big" href={consent.link} target="_blank" rel="noreferrer">
                    Open WhatsApp with the message
                  </a>
                  <p className="hint">Only on the owner's phone: the message must come from their number. Never read the code aloud for someone else to type.</p>
                </>
              )}
            </>
          ) : (
            <>
              {consent.telegram_qr && (
                <img className="agent-qr" src={consent.telegram_qr} width={240} height={240} alt="QR code: the owner scans it with their own phone to open Taskiem on Telegram" />
              )}
              {consent.telegram_link && (
                <a className="button primary agent-big" href={consent.telegram_link} target="_blank" rel="noreferrer">
                  Open Telegram
                </a>
              )}
              <p className="hint">
                The owner taps Start, then “Share my number”: Telegram sends their own number, which must be the one you entered. They can also send{" "}
                <strong>{consent.text}</strong> to the bot. Only on the owner's phone.
              </p>
            </>
          )}
          <p aria-live="off">
            Code works for <strong>{formatLeft(left)}</strong>
          </p>
        </>
      ) : (
        <div className="notice">{consent ? "This code has run out." : "Make a code to show the owner."}</div>
      )}
      <p className="hint" role="status">
        Waiting for the owner to agree…
      </p>
      <ErrorBox error={act.error ?? error} />
      <button className="agent-big" disabled={act.busy} onClick={() => void newCode()}>
        New code
      </button>
      {back}
    </section>
  );
}

/** A photo, scaled down and as JPEG, to send on a slow network. */
async function shrink(file: File): Promise<Blob> {
  try {
    const bmp = await createImageBitmap(file);
    const { width, height } = fitWithin(bmp.width, bmp.height, 1600);
    const canvas = document.createElement("canvas");
    canvas.width = width;
    canvas.height = height;
    canvas.getContext("2d")?.drawImage(bmp, 0, 0, width, height);
    bmp.close();
    const blob = await new Promise<Blob | null>((resolve) => canvas.toBlob(resolve, "image/jpeg", 0.8));
    return blob && blob.size < file.size ? blob : file;
  } catch {
    return file; // the server checks it
  }
}

function dataURL(b: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error ?? new Error("The photo could not be read."));
    r.readAsDataURL(b);
  });
}

type Place = { lat: number; lng: number; accuracy: number };

/** The visit: a photo of the shop or stall and where it is. */
function RecordVisit() {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const [photo, setPhoto] = useState<Blob>();
  const [preview, setPreview] = useState<string>();
  const [place, setPlace] = useState<Place>();
  const [placeError, setPlaceError] = useState("");
  const [finding, setFinding] = useState(false);
  const [note, setNote] = useState("");
  const [done, setDone] = useState(false);
  const act = useAction();

  const locate = () => {
    if (!("geolocation" in navigator)) {
      setPlaceError("This phone cannot give its location to the browser.");
      return;
    }
    setFinding(true);
    setPlaceError("");
    navigator.geolocation.getCurrentPosition(
      (p) => {
        setFinding(false);
        setPlace({ lat: p.coords.latitude, lng: p.coords.longitude, accuracy: p.coords.accuracy });
      },
      (e) => {
        setFinding(false);
        setPlaceError(e.code === e.PERMISSION_DENIED ? "Allow location for this site in the browser's settings, then try again." : "The location could not be found. Go outside or near a window and try again.");
      },
      { enableHighAccuracy: true, timeout: 30_000, maximumAge: 60_000 },
    );
  };
  // Once, on arrival.
  useEffect(locate, []);

  if (done) {
    return (
      <section className="card">
        <h1>Visit recorded</h1>
        <div className="notice" role="status">
          The photo and place are saved. The business is now agent onboarded.
        </div>
        <ErrorBox error={act.error} />
        <button className="primary agent-big" disabled={act.busy} onClick={() => void act.run(() => enterSession(id, navigate), false)}>
          Back to the page
        </button>
        <Link to="/agent">My businesses</Link>
      </section>
    );
  }

  return (
    <section className="card">
      <h1>Record visit</h1>
      <p className="hint">Take a photo of the shop or stall from outside, standing at it, so its place is right.</p>
      <Field label="Photo of the shop or stall">
        <input
          type="file"
          accept="image/*"
          capture="environment"
          onChange={(e) => {
            const f = e.target.files?.[0];
            if (!f) return;
            void act.run(async () => {
              const b = await shrink(f);
              setPhoto(b);
              setPreview(await dataURL(b));
            }, false);
          }}
        />
      </Field>
      {preview && <img className="agent-photo" src={preview} alt="The photo to send" />}
      <div className="field">
        <span>Place</span>
        {place ? (
          <p style={{ margin: "4px 0" }}>
            Found, to within {Math.round(place.accuracy)} m.{" "}
            {place.accuracy > 100 && <span className="hint">That is rough: try again outside.</span>}
          </p>
        ) : (
          <p className="hint" style={{ margin: "4px 0" }}>
            {finding ? "Finding where you are…" : placeError || "Not found yet."}
          </p>
        )}
        <button type="button" disabled={finding} onClick={locate}>
          {place ? "Find again" : "Find my location"}
        </button>
      </div>
      <Field label="Note (optional)" hint={`${note.length}/200`}>
        <textarea value={note} onChange={(e) => setNote(e.target.value)} maxLength={200} rows={2} />
      </Field>
      <ErrorBox error={act.error} />
      <button
        className="primary agent-big"
        disabled={act.busy || !photo || !place}
        onClick={() =>
          void act.run(async () => {
            if (!photo || !place) return;
            await uploadFile(attestPath(id, place.lat, place.lng, note), photo);
            markAttested(id);
            setDone(true);
          }, false)
        }
      >
        {act.busy ? "Sending…" : "Send"}
      </button>
      <Link to="/agent">Back</Link>
    </section>
  );
}

/** The business's page and tables, in the agent's session, under a banner
 * saying whose business it is and how long is left. */
export function AgentWorkspace({ session: s }: { session: AgentSession }) {
  const navigate = useNavigate();
  const now = useNow(15_000);
  const left = msLeft(s.expires_at, now);
  const act = useAction();
  const recorded = s.kind === "support" || attested(s.id);

  const leave = (state: HomeState) => {
    setAgentSession(null);
    navigate("/agent", { state });
  };
  const leaveRef = useRef(leave);
  leaveRef.current = leave;

  // Publishing an onboarding's page hands it over and ends the session; the
  // page's next request may be refused before the check below answers.
  const handedOver = useRef(false);
  useEffect(() => {
    const ended = () =>
      leaveRef.current(
        handedOver.current ? { handedOver: s.business } : { message: `Your session with ${s.business} has ended: the owner ended it, or its time ran out.` },
      );
    const published = () => {
      if (s.kind !== "onboarding") return;
      handedOver.current = true;
      get<{ session: SessionRow }>(`/v1/agent/sessions/${s.id}`).then(
        (r) => r.session.status === "ended" && leaveRef.current({ handedOver: s.business }),
        () => undefined,
      );
    };
    window.addEventListener("taskiem:agent-session-ended", ended);
    window.addEventListener("taskiem:agent-published", published);
    return () => {
      window.removeEventListener("taskiem:agent-session-ended", ended);
      window.removeEventListener("taskiem:agent-published", published);
    };
  }, [s.id, s.business, s.kind]);

  useEffect(() => {
    if (left === 0) leaveRef.current({ message: `Your time with ${s.business} is up.` });
  }, [left, s.business]);

  return (
    <AgentPermissions allowed={AGENT_PERMISSIONS}>
      <div className="agent-work">
        <div className="agent-banner" role="region" aria-label="Agent session">
          <div className="agent-banner-text">
            <strong>{s.business}</strong>
            <span>
              Working as agent · {formatLeft(left)} left
            </span>
          </div>
          <div className="agent-banner-actions">
            {s.kind === "onboarding" && (
              <Link className="button" to={`/agent/sessions/${s.id}/visit`}>
                Record visit
              </Link>
            )}
            <button
              className="danger"
              disabled={act.busy}
              onClick={() =>
                void act.run(async () => {
                  await post(`/v1/agent/sessions/${s.id}/end`);
                  leave({ message: `You left ${s.business}.` });
                }, false)
              }
            >
              End session
            </button>
          </div>
        </div>
        <ErrorBox error={act.error} />
        {!recorded && (
          <div className="notice">
            Record the visit before you publish: publishing hands the page to the owner and ends this session.
          </div>
        )}
        <nav className="tabs" aria-label="Business">
          <NavLink to="/storefront" end>
            Page
          </NavLink>
          <NavLink to="/storefront/look">Look</NavLink>
          <NavLink to="/tables">Tables</NavLink>
          <Link to="/agent">My businesses</Link>
        </nav>
        <main className="main agent-main" id="main">
          <Routes>
            <Route path="/storefront" element={<Storefront />} />
            <Route path="/storefront/look" element={<Look />} />
            <Route path="/tables" element={<Tables />} />
            <Route path="/tables/:id" element={<TablePage />} />
            <Route path="*" element={<Navigate to="/storefront" replace />} />
          </Routes>
        </main>
      </div>
    </AgentPermissions>
  );
}
