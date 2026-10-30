import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { del, get, getBlob, post, put } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import {
  cardAction,
  cardSub,
  findableLine,
  groupLinks,
  matchLinked,
  roleLine,
  ROLE_CHOICES,
  stateWords,
  type BlindLinkRequest,
  type BusinessLink,
  type LinkAnswer,
  type LinkCard,
  type LinkRole,
  type LinkSearch,
  type LinkSettings,
  type LinksView,
} from "../lib/links";
import { EmptyState, ErrorBox, fmtTime, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";

// Business links, L0 (docs/spec/business-links.md §5.1): the Chat page's
// list of businesses and its search, and Linked businesses under Secrets
// & settings. Until the conversation (L3) comes, a business opens its
// card here, not a chat.

function StateBadge({ state }: { state: Parameters<typeof stateWords>[0] }) {
  const w = stateWords(state);
  return w ? <span className={`badge sf-status ${w.tone}`}>{w.label}</span> : null;
}

/** Who a request goes to: a business a search listed, one by the link
 * code it printed, or whoever uses a phone number. */
type Asking = { business: string; name: string } | { code: string; name: string } | { phone: string; shown: string };

/** The sheet that asks a business to link: how we trade with them, then Send. */
function RequestSheet({ asking, onClose, onSent }: { asking: Asking; onClose: () => void; onSent: () => void }) {
  const [role, setRole] = useState<LinkRole>("supplier");
  const act = useAction();
  const blind = "phone" in asking;
  const send = () =>
    void act.run(async () => {
      const body = blind ? { phone: asking.phone, role } : "code" in asking ? { code: asking.code, role } : { business: asking.business, role };
      const out = await post<{ message: string }>("/v1/links", body);
      onSent();
      return out;
    }, blind ? "Request sent" : `Asked ${asking.name} to link`).then((ok) => ok && onClose());
  return (
    <Modal title={blind ? `Ask ${asking.shown} to link` : `Ask ${asking.name} to link`} onClose={onClose}>
      <p className="hint">
        {blind
          ? "If a business on Taskiem uses this number, it gets your request. You hear only if it accepts."
          : "Linked, your invoices and orders reach each other by themselves. Nothing is shared until they accept."}
      </p>
      <fieldset className="choice">
        <legend>How do you trade with them?</legend>
        {ROLE_CHOICES.map((c) => (
          <label key={c.role} className="inline">
            <input type="radio" name="link-role" checked={role === c.role} onChange={() => setRole(c.role)} />
            {c.label}
          </label>
        ))}
      </fieldset>
      <ErrorBox error={act.error} />
      <div className="row-actions">
        <button type="button" className="primary" disabled={act.busy} onClick={send}>
          Send request
        </button>
        <button type="button" onClick={onClose}>
          Not now
        </button>
      </div>
    </Modal>
  );
}

/** Accept, Decline and Block on a request made to us. A decline and a
 * block are never told to the business that asked. */
function AnswerButtons({ link, answer, busy }: { link: BusinessLink; answer: (l: BusinessLink, a: LinkAnswer, done: string) => void; busy: boolean }) {
  const who = <span className="sr-only"> {link.name}</span>;
  return (
    <>
      <button type="button" className="primary small" disabled={busy} onClick={() => answer(link, "accept", `Linked with ${link.name}`)}>
        Accept{who}
      </button>
      <button type="button" className="small" disabled={busy} onClick={() => answer(link, "decline", "Declined. They are not told.")}>
        Decline{who}
      </button>
      <button
        type="button"
        className="small danger"
        disabled={busy}
        onClick={() => confirm(`Block ${link.name}? Their requests will not reach you, and they are not told.`) && answer(link, "block", "Blocked")}
      >
        Block{who}
      </button>
    </>
  );
}

function useLinks() {
  const view = useLoad(() => get<LinksView>("/v1/links"), []);
  const act = useAction();
  const answer = (l: BusinessLink | BlindLinkRequest, a: LinkAnswer | "remove", done: string) =>
    void act.run(async () => {
      await (a === "remove" ? del(`/v1/links/sent/${l.id}`) : post(`/v1/links/${l.id}/${a}`));
      view.reload();
    }, done);
  return { view, act, answer };
}

/**
 * Chat (business links, L0): the businesses on Taskiem this one trades
 * with, and the search that finds others, as WhatsApp's search finds
 * contacts. A phone number typed is never searched: it offers the one row
 * that sends a request to it.
 */
export function ChatPage() {
  const { can } = useAuth();
  const manage = can("link.manage");
  const { view, act, answer } = useLinks();
  const settings = useLoad(() => get<LinkSettings>("/v1/link-settings"), []);
  const [q, setQ] = useState("");
  const [found, setFound] = useState<LinkSearch>();
  const [searchError, setSearchError] = useState<unknown>();
  const [asking, setAsking] = useState<Asking>();
  const query = q.trim();
  useEffect(() => {
    if (query.length < 2) {
      setFound(undefined);
      return;
    }
    let live = true;
    const t = setTimeout(() => {
      get<LinkSearch>(`/v1/links/search?q=${encodeURIComponent(query)}`).then(
        (r) => live && (setFound(r), setSearchError(undefined)),
        (e: unknown) => live && setSearchError(e),
      );
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [query]);
  const g = groupLinks(view.data);
  const searching = query.length >= 2;
  const chats = searching ? matchLinked(view.data?.links ?? [], query) : [];
  const blind = found?.blind;
  const others = (found?.results ?? []).filter((c) => !chats.some((l) => l.business === c.business));
  return (
    <>
      <PageHeader
        title="Chat"
        description="The businesses on Taskiem you trade with. Find another by its name, what it sells or its area, and ask to link."
        actions={
          <Link className="button" to="/settings/links">
            Linked businesses
          </Link>
        }
      />
      <div className="filters" role="search" aria-label="Find a business">
        <label className="search">
          <span className="sr-only">Find a business</span>
          <Icon name="search" />
          <input type="search" placeholder="Cement, catering in Ikeja, Dangote…" value={q} onChange={(e) => setQ(e.target.value)} data-testid="link-search" />
        </label>
      </div>
      <ErrorBox error={view.error ?? act.error ?? searchError} />
      {manage && settings.data && !settings.data.asked_at && !searching && (
        <FindableQuestion
          settings={settings.data}
          busy={act.busy}
          answer={(findable) =>
            void act.run(async () => (await put("/v1/link-settings", { findable }), settings.reload()), findable ? "Other businesses can find you" : false)
          }
        />
      )}
      {searching ? (
        <section aria-label="Search results" data-testid="link-results">
          {chats.length > 0 && (
            <>
              <h2>Chats</h2>
              <Items label="Linked businesses found">
                {chats.map((l) => (
                  <Row key={l.id} letter={l.name} title={l.name} side={<StateBadge state={l.state} />} sub={<span className="sub">{roleLine(l.role)}</span>} testid="link-chat" />
                ))}
              </Items>
            </>
          )}
          <h2>Businesses on Taskiem</h2>
          {blind ? (
            <Items label="Send a request">
              <Row icon="chat" title={`Send a link request to ${blind.shown}`} sub={<span className="sub">If a business on Taskiem uses this number, it gets your request.</span>} testid="link-blind">
                {manage && (
                  <button type="button" className="small" onClick={() => setAsking({ phone: query, shown: blind.shown })}>
                    Send request
                  </button>
                )}
              </Row>
            </Items>
          ) : !found ? (
            <Skeleton rows={2} label="Searching businesses…" />
          ) : others.length === 0 ? (
            <EmptyState icon="search">No business that lets others find it matches “{query}”.</EmptyState>
          ) : (
            <Items label="Businesses on Taskiem">
              {others.map((c) => (
                <FoundRow key={c.business} card={c} manage={manage} onAsk={() => setAsking({ business: c.business, name: c.name })} />
              ))}
            </Items>
          )}
        </section>
      ) : !view.data ? (
        <Skeleton rows={3} label="Loading your linked businesses…" />
      ) : g.asked.length + g.linked.length + g.waiting.length + g.sent.length === 0 ? (
        <EmptyState icon="chat">No linked businesses yet. Search for a supplier or a customer that is on Taskiem, and ask to link.</EmptyState>
      ) : (
        <>
          {g.asked.length > 0 && (
            <section aria-label="Asks to link">
              <h2>Asks to link</h2>
              <Items label="Asks to link">
                {g.asked.map((l) => (
                  <Row key={l.id} letter={l.name} tone="warn" title={l.name} side={<StateBadge state={l.state} />} sub={<span className="sub">{roleLine(l.role)} · asked {fmtTime(l.requested_at)}</span>} testid="link-asked">
                    {manage ? <AnswerButtons link={l} answer={answer} busy={act.busy} /> : <span className="hint">An owner or admin answers this.</span>}
                  </Row>
                ))}
              </Items>
            </section>
          )}
          {g.linked.length > 0 && (
            <section aria-label="Linked">
              <h2>Linked</h2>
              <Items label="Linked businesses">
                {g.linked.map((l) => (
                  <Row key={l.id} letter={l.name} tone="ok" title={l.name} side={<StateBadge state={l.state} />} sub={<span className="sub">{roleLine(l.role)}</span>} testid="link-linked" />
                ))}
              </Items>
            </section>
          )}
          {g.waiting.length + g.sent.length > 0 && (
            <section aria-label="Waiting for an answer">
              <h2>Waiting for an answer</h2>
              <Items label="Waiting for an answer">
                {g.waiting.map((l) => (
                  <Row key={l.id} letter={l.name} tone="info" title={l.name} side={<StateBadge state={l.state} />} sub={<span className="sub">Asked {fmtTime(l.requested_at)}</span>} testid="link-waiting" />
                ))}
                {g.sent.map((b) => (
                  <Row key={b.id} icon="chat" tone="info" title={b.shown} side={<StateBadge state="sent" />} sub={<span className="sub">Sent {fmtTime(b.created_at)}</span>} testid="link-sent" />
                ))}
              </Items>
            </section>
          )}
        </>
      )}
      {asking && <RequestSheet asking={asking} onClose={() => setAsking(undefined)} onSent={() => (view.reload(), setQ(""))} />}
    </>
  );
}

function FoundRow({ card, manage, onAsk }: { card: LinkCard; manage: boolean; onAsk: () => void }) {
  const action = cardAction(card);
  return (
    <Row letter={card.name} title={card.name} side={<StateBadge state={card.state} />} sub={<span className="sub">{cardSub(card)}</span>} testid="link-found">
      {action === "request" &&
        (manage ? (
          <button type="button" className="small" onClick={onAsk}>
            Request link<span className="sr-only"> with {card.name}</span>
          </button>
        ) : (
          <span className="hint">An owner or admin can link with this business.</span>
        ))}
      {action === "answer" && <span className="hint">They asked you: answer under Asks to link.</span>}
    </Row>
  );
}

/**
 * Linked businesses (under Secrets & settings): whether other businesses
 * find this one, and every link and request with what can be done to it.
 */
export function LinkedBusinessesPage() {
  const { can } = useAuth();
  const manage = can("link.manage");
  const { view, act, answer } = useLinks();
  const settings = useLoad(() => get<LinkSettings & { url?: string }>("/v1/link-settings"), []);
  const g = groupLinks(view.data);
  const setFindable = (on: boolean) => void act.run(async () => (await put("/v1/link-settings", { findable: on }), settings.reload()), "Saved");
  const all = [...g.asked, ...g.linked, ...g.waiting, ...g.past];
  return (
    <>
      <p className="crumbs">
        <Link to="/chat" aria-label="Back to Chat">
          Chat
        </Link>
      </p>
      <PageHeader title="Linked businesses" description="Businesses on Taskiem you have linked with, and requests either way. Each side keeps its own records." />
      <ErrorBox error={view.error ?? settings.error ?? act.error} />
      <section className="card" aria-label="Being found" data-testid="link-findable">
        <h2>Let other businesses find us</h2>
        {settings.data ? (
          <>
            <label className="switch-line">
              <input type="checkbox" aria-label="Let other businesses find us" checked={settings.data.findable_now} disabled={!manage || act.busy} onChange={(e) => setFindable(e.target.checked)} />
              <span>{findableLine(settings.data)}</span>
            </label>
            <p className="hint">A search shows only what your storefront already shows the public. Someone with your phone number or page address can always ask.</p>
          </>
        ) : (
          <Skeleton rows={1} label="Loading the setting…" />
        )}
      </section>
      {settings.data?.url && <CodeCard url={settings.data.url} />}
      <section aria-label="Links">
        {!view.data ? (
          <Skeleton rows={3} label="Loading your linked businesses…" />
        ) : all.length + g.sent.length === 0 ? (
          <EmptyState icon="chat" action={<Link className="button" to="/chat">Find a business</Link>}>
            No links yet.
          </EmptyState>
        ) : (
          <Items label="Linked businesses and requests">
            {all.map((l) => (
              <Row
                key={l.id}
                letter={l.name}
                tone={stateWords(l.state)?.tone}
                title={l.name}
                side={<StateBadge state={l.state} />}
                sub={
                  <span className="sub">
                    {roleLine(l.role)}
                    <span className="block">{l.state === "active" && l.answered_at ? `Linked ${fmtTime(l.answered_at)}` : `Asked ${fmtTime(l.requested_at)}`}</span>
                  </span>
                }
                testid="link-row"
                attrs={{ "data-state": l.state }}
              >
                {manage && <LinkActions link={l} answer={answer} busy={act.busy} />}
              </Row>
            ))}
            {g.sent.map((b) => (
              <Row key={b.id} icon="chat" tone="info" title={b.shown} side={<StateBadge state="sent" />} sub={<span className="sub">Sent {fmtTime(b.created_at)}</span>} testid="link-row" attrs={{ "data-state": "sent" }}>
                {manage && (
                  <button type="button" className="small" disabled={act.busy} onClick={() => answer(b, "remove", "Taken off the list")}>
                    Take off the list<span className="sr-only"> {b.shown}</span>
                  </button>
                )}
              </Row>
            ))}
          </Items>
        )}
      </section>
    </>
  );
}

function LinkActions({ link, answer, busy }: { link: BusinessLink; answer: (l: BusinessLink, a: LinkAnswer, done: string) => void; busy: boolean }) {
  const who = <span className="sr-only"> {link.name}</span>;
  switch (link.state) {
    case "asked_us":
      return <AnswerButtons link={link} answer={answer} busy={busy} />;
    case "active":
      return (
        <button
          type="button"
          className="small danger"
          disabled={busy}
          onClick={() => confirm(`End the link with ${link.name}? What passed between you stays on both sides.`) && answer(link, "end", "Link ended")}
        >
          End link{who}
        </button>
      );
    case "requested":
    case "not_accepted":
      return (
        <button type="button" className="small" disabled={busy} onClick={() => answer(link, "cancel", "Request withdrawn")}>
          {link.state === "requested" ? "Withdraw request" : "Take off the list"}
          {who}
        </button>
      );
    case "blocked":
      return (
        <button type="button" className="small" disabled={busy} onClick={() => answer(link, "unblock", "Unblocked")}>
          Unblock{who}
        </button>
      );
    default:
      return null;
  }
}

// A data: URL, not a blob: one: the console's Content Security Policy allows only data: images besides its own.
function dataURL(b: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () => reject(r.error ?? new Error("could not read the QR code"));
    r.readAsDataURL(b);
  });
}

/** The business's link code: the address that opens a request to it, and
 * its QR code, to print on a counter card or share. */
function CodeCard({ url }: { url: string }) {
  const [qr, setQr] = useState<string>();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    let live = true;
    getBlob("/v1/link-settings/qr")
      .then(dataURL)
      .then(
        (u) => live && setQr(u),
        () => undefined,
      );
    return () => {
      live = false;
    };
  }, [url]);
  return (
    <section className="card" aria-labelledby="link-code" data-testid="link-code">
      <h2 id="link-code">Link with us on Taskiem</h2>
      <p className="hint">A business on Taskiem that opens this or scans the code can ask to link with you, whether or not others find you by search.</p>
      <div className="sf-share">
        {qr ? <img className="sf-qr" src={qr} alt={`QR code for ${url}`} width={160} height={160} /> : <Skeleton rows={1} label="Loading the QR code…" />}
        <div>
          <p className="mono" data-testid="link-code-url">
            {url}
          </p>
          <div className="toolbar">
            <button type="button" onClick={() => void navigator.clipboard?.writeText(url).then(() => setCopied(true))}>
              {copied ? "Copied" : "Copy the link"}
            </button>
            <a className="button" href="/v1/link-settings/qr?format=png" download="link-with-us-qr.png">
              Download the QR code
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}

/**
 * The page a link code opens (/l/<code>): the business behind it, our
 * link with it, and Request link. A code printed on an invoice or a
 * counter card reaches a business whether or not it lets search find it.
 */
export function LinkCodePage() {
  const { code = "" } = useParams();
  const { can } = useAuth();
  const navigate = useNavigate();
  const card = useLoad(() => get<{ business: string; name: string; state: LinkCard["state"] }>(`/v1/links/code/${encodeURIComponent(code)}`), [code]);
  const [asking, setAsking] = useState(false);
  const c = card.data;
  return (
    <>
      <p className="crumbs">
        <Link to="/chat" aria-label="Back to Chat">
          Chat
        </Link>
      </p>
      <PageHeader title={c ? c.name : "Link with a business"} />
      {card.error ? (
        <EmptyState icon="chat" action={<Link className="button" to="/chat">Find a business</Link>}>
          This link does not open a business you can link with. It may be your own, or mistyped.
        </EmptyState>
      ) : !c ? (
        <Skeleton rows={2} label="Loading the business…" />
      ) : (
        <section className="card" data-testid="link-code-card">
          <h2>
            {c.name} <StateBadge state={c.state} />
          </h2>
          {cardAction({ ...c, items: [] }) === "request" ? (
            can("link.manage") ? (
              <>
                <p className="hint">Linked, your invoices and orders reach each other by themselves. Nothing is shared until they accept.</p>
                <button type="button" className="primary" onClick={() => setAsking(true)}>
                  Request link
                </button>
              </>
            ) : (
              <p className="hint">An owner or admin can link with this business.</p>
            )
          ) : c.state === "asked_us" ? (
            <p className="hint">
              They asked you already: answer under <Link to="/chat">Chat</Link>.
            </p>
          ) : (
            <p className="hint">Nothing to do here. Your linked businesses are under <Link to="/settings/links">Linked businesses</Link>.</p>
          )}
        </section>
      )}
      {asking && c && <RequestSheet asking={{ code, name: c.name }} onClose={() => setAsking(false)} onSent={() => navigate("/chat")} />}
    </>
  );
}

/** A link code opened signed out: sign in, or sign the business up first. */
export function LinkCodeSignedOut() {
  const next = encodeURIComponent(window.location.pathname);
  return (
    <div className="login card">
      <h1>Link with a business on Taskiem</h1>
      <p>A business on Taskiem shared this so you can link: your invoices and orders then reach each other by themselves. Sign in to your business to ask.</p>
      <p>
        <Link className="button primary" to={`/login?next=${next}`}>
          Sign in
        </Link>
      </p>
      <p className="hint">
        Not on Taskiem yet? <Link to="/signup">Sign up your business</Link>, then open the link again.
      </p>
    </div>
  );
}

/** Asked once, of whoever may answer for the business's links: whether
 * other businesses find it by search. Not now keeps the default (on with a
 * published storefront). */
function FindableQuestion({ settings, busy, answer }: { settings: LinkSettings; busy: boolean; answer: (findable: boolean | null) => void }) {
  return (
    <section className="card" aria-labelledby="link-findable-ask" data-testid="link-findable-ask">
      <h2 id="link-findable-ask">Let other businesses on Taskiem find you?</h2>
      <p className="hint">
        They would find you by your name, what you sell and your area, from what your storefront already shows. {findableLine(settings)}
      </p>
      <div className="row-actions">
        <button type="button" className="primary" disabled={busy} onClick={() => answer(true)}>
          Yes, let them find us
        </button>
        <button type="button" disabled={busy} onClick={() => answer(null)}>
          Not now
        </button>
      </div>
    </section>
  );
}
