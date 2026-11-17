import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { get, post } from "../api";
import { Icon } from "../icons";
import { KitField, Sheet } from "../kit";
import { PageCache } from "../lib/pagecache";
import {
  addResults,
  canShowMore,
  chatPath,
  discoverProblem,
  DISCOVER_REPORT_REASONS,
  foundWords,
  nothingFound,
  openNow,
  photoAt,
  placeProblem,
  resultLine,
  searchBody,
  searchFromParams,
  searchKey,
  searchToParams,
  type DiscoverCategory,
  type DiscoverLocation,
  type DiscoverProfile,
  type DiscoverQuery,
  type DiscoverResult,
} from "../lib/discover";
import { hoursLines, type ChatLinkCard } from "../lib/personchat";
import { EmptyState, ErrorBox, Items, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { ChatAvatar } from "./ChatAvatar";

// Discover (docs/spec/taskiem-for-everyone.md §10), on You: the public
// finding businesses that chose to be found. A search box (a name or what
// they want), an area, the categories as chips, and "Near me", which asks
// the browser where the person is only when tapped and sends it in the
// search's body, never in an address, and keeps it nowhere. Results are
// rows; a business's profile has its photos, hours, address, Their shop,
// Book and Chat. A sibling of Explore (businesses finding businesses) in
// its look, and apart from it: its own setting, its own list.

/** How long the typing rests before the search runs. */
const QUIET_MS = 350;

/** A search's results as received: the rows, whether more wait, and how
 * many rows the pages held (the next page's offset). */
interface Found {
  results: DiscoverResult[];
  more: boolean;
  received: number;
}

/** Searches already made in this tab, so Back returns to them (not those
 * made with the person's place: that is kept nowhere). */
const kept = new PageCache<Found>();

/** The last search's address, for a profile's way back to it. */
let lastSearch = "";
const discoverHome = () => `/you/discover${lastSearch ? `?${lastSearch}` : ""}`;

/** One business found, as a row: its photo, its name (a link to its
 * profile), open now or closed now, its category, area and distance, the
 * hours line, Chat and See more. The listing's preview in Settings >
 * Discover draws the same row (`preview`: its buttons do nothing). */
export function DiscoverRow({ r, onChat, preview = false }: { r: DiscoverResult; onChat?: () => void; preview?: boolean }) {
  const to = `/you/discover/${encodeURIComponent(r.business)}`;
  return (
    <Row
      icon="store"
      image={photoAt(r.photo, 160)}
      title={
        preview ? (
          <span className="discover-name">{r.name}</span>
        ) : (
          <Link to={to} className="discover-name">
            {r.name}
          </Link>
        )
      }
      side={<span className={`badge sf-status ${r.open_now ? "ok" : "muted"}`}>{openNow(r.open_now)}</span>}
      sub={
        <span className="sub">
          {resultLine(r) || (preview ? "No category or area yet" : "")}
          {r.line && <span className="block">{r.line}</span>}
        </span>
      }
      testid={preview ? "discover-preview-row" : "discover-result"}
    >
      {r.can_chat && (
        <button type="button" className="small primary" disabled={preview} onClick={onChat} data-testid={preview ? "discover-preview-chat" : undefined}>
          Chat<span className="sr-only"> with {r.name}</span>
        </button>
      )}
      {preview ? (
        <button type="button" className="small" disabled>
          See more<span className="sr-only"> about {r.name}</span>
        </button>
      ) : (
        <Link className="button small" to={to}>
          See more<span className="sr-only"> about {r.name}</span>
        </Link>
      )}
    </Row>
  );
}

/** Discover's start, /you/discover. The search lives in the address
 * (?q=&area=&category=), so Back from a profile returns to it, with the
 * pages that were open. */
export function Discover() {
  const nav = useNavigate();
  const [params, setParams] = useSearchParams();
  const cats = useLoad(() => get<{ categories: DiscoverCategory[] }>("/v1/discover/categories"), []);
  // The search the results are for: the address's.
  const asked = searchFromParams(params);
  lastSearch = new URLSearchParams(searchToParams(asked)).toString();
  // What is typed: it settles into the address after a rest.
  const [q, setQ] = useState<DiscoverQuery>(asked);
  const typed = useRef(q);
  typed.current = q;
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  // The person's place, for this page only: asked for when Near me is
  // tapped, gone when it is tapped again or the page is left. Never in
  // the address, never kept.
  const [near, setNear] = useState<DiscoverLocation | null>(null);
  const [locating, setLocating] = useState(false);
  const [placeError, setPlaceError] = useState<string>();
  const key = searchKey(asked);
  const [found, setFound] = useState<{ key: string; near: DiscoverLocation | null } & Found>(() => {
    const k = kept.get(key);
    return k ? { key, near: null, ...k.value } : { key: "", near: null, results: [], more: false, received: 0 };
  });
  const [error, setError] = useState<unknown>();
  const [moreError, setMoreError] = useState<unknown>();
  const [moreBusy, setMoreBusy] = useState(false);
  // Try again: a new count asks the same search again.
  const [tries, setTries] = useState(0);
  const seq = useRef(0);

  const settle = (s: DiscoverQuery) => setParams(searchToParams(s), { replace: true });
  const type = (s: DiscoverQuery) => {
    setQ(s);
    clearTimeout(timer.current);
    // The latest typing when the rest ends, whatever was typed since.
    timer.current = setTimeout(() => {
      timer.current = undefined;
      settle(typed.current);
    }, QUIET_MS);
  };
  useEffect(() => () => clearTimeout(timer.current), []);
  // The address changed by itself (Back, Discover's tab): the boxes follow.
  useEffect(() => {
    if (timer.current === undefined && searchKey(typed.current) !== key) setQ(asked);
  }, [key]);
  const pickCategory = (cat: string) => {
    // At once, with what is typed now: a search still waiting to settle
    // would otherwise put the old category back.
    clearTimeout(timer.current);
    const next = { ...typed.current, category: typed.current.category === cat ? "" : cat };
    setQ(next);
    settle(next);
  };

  // Back to where the person was: the kept results' place on the page.
  const restored = useRef(found.key === key && !!kept.get(key));
  useEffect(() => {
    if (!restored.current) return;
    const at = kept.get(key)?.scroll ?? 0;
    requestAnimationFrame(() => window.scrollTo(0, at));
  }, []);
  // Leaving: where the list was scrolled to is kept with it.
  const keyNow = useRef(key);
  keyNow.current = near ? "" : key;
  useEffect(() => () => void (keyNow.current && kept.scrolled(keyNow.current, window.scrollY)), []);

  const nearKey = near ? `${near.lat},${near.lng}` : "";
  useEffect(() => {
    if (restored.current) {
      restored.current = false;
      return;
    }
    const n = ++seq.current;
    setError(undefined);
    setMoreError(undefined);
    post<{ results: DiscoverResult[]; more: boolean }>("/v1/discover/search", searchBody(asked, near, 0)).then(
      (r) => {
        if (n !== seq.current) return;
        const f = { results: r.results, more: r.more, received: r.results.length };
        setFound({ key, near, ...f });
        if (!near) kept.set(key, f);
      },
      (e: unknown) => n === seq.current && setError(e),
    );
  }, [key, nearKey, tries]);
  const results = found.key === key && found.near === near ? found : undefined;
  const loadMore = () => {
    if (!results || moreBusy) return;
    setMoreBusy(true);
    setMoreError(undefined);
    const n = seq.current;
    // The next page starts after every row the pages held.
    post<{ results: DiscoverResult[]; more: boolean }>("/v1/discover/search", searchBody(asked, near, results.received))
      .then(
        (r) => {
          if (n !== seq.current) return;
          const f = { results: addResults(results.results, r.results), more: r.more, received: results.received + r.results.length };
          setFound({ key, near, ...f });
          if (!near) kept.set(key, f, window.scrollY);
        },
        (e: unknown) => n === seq.current && setMoreError(e),
      )
      .finally(() => setMoreBusy(false));
  };

  const nearMe = () => {
    setPlaceError(undefined);
    if (near) return setNear(null);
    if (!("geolocation" in navigator)) return setPlaceError(placeProblem(2));
    setLocating(true);
    navigator.geolocation.getCurrentPosition(
      (p) => {
        setLocating(false);
        setNear({ lat: p.coords.latitude, lng: p.coords.longitude });
      },
      (e) => {
        setLocating(false);
        setPlaceError(placeProblem(e.code));
      },
      { enableHighAccuracy: false, timeout: 15000, maximumAge: 300000 },
    );
  };

  const categories = cats.data?.categories ?? [];
  const catName = categories.find((c) => c.key === asked.category)?.name;
  const said = discoverProblem(error, "search");
  const moreSaid = discoverProblem(moreError, "search");
  return (
    <div className="tk-x discover" data-testid="discover">
      <PageHeader title="Discover" description="Find businesses near you, chat with them, order and book. Only businesses that chose to be found are here." />
      <div className="tk-x-search discover-search" role="search">
        <label>
          <span className="sr-only">Search businesses</span>
          <Icon name="search" />
          <input
            type="search"
            value={q.q}
            maxLength={100}
            placeholder="A name, or what you want"
            onChange={(e) => type({ ...q, q: e.target.value })}
            data-testid="discover-search"
          />
        </label>
      </div>
      <div className="discover-where">
        <KitField label="Area">
          <input type="text" value={q.area} maxLength={80} placeholder="Yaba, Ikeja, Wuse" onChange={(e) => type({ ...q, area: e.target.value })} data-testid="discover-area" />
        </KitField>
        <button type="button" className="tk-x-chip discover-near" aria-pressed={!!near} disabled={locating} onClick={nearMe} data-testid="discover-near">
          <Icon name="pin" />
          {locating ? "Finding you…" : "Near me"}
        </button>
      </div>
      {placeError && (
        <p className="hint" role="status" data-testid="discover-place-problem">
          {placeError}
        </p>
      )}
      {near && (
        <p className="hint" data-testid="discover-near-line">
          Nearest first. Your place is used for this search only and is not kept.
        </p>
      )}
      {categories.length > 0 && (
        <div className="tk-x-chips" role="group" aria-label="Categories" data-testid="discover-categories">
          {categories.map((c) => (
            <button key={c.key} type="button" className="tk-x-chip" aria-pressed={c.key === q.category} onClick={() => pickCategory(c.key)}>
              {c.name}
            </button>
          ))}
        </div>
      )}
      {error ? (
        <div className="warning discover-problem" role="alert">
          <span data-testid="discover-problem">{said ?? (error instanceof Error ? error.message : "The search did not work.")}</span>{" "}
          <button type="button" className="small" onClick={() => setTries((t) => t + 1)} data-testid="discover-again">
            Try again
          </button>
        </div>
      ) : (
        <ErrorBox error={cats.error} />
      )}
      {!results && !error && <Skeleton rows={3} label="Finding businesses…" />}
      {results && results.results.length === 0 && <EmptyState icon="search">{nothingFound(asked, catName, !!near)}</EmptyState>}
      {results && results.results.length > 0 && (
        <section aria-labelledby="discover-found">
          <h2 id="discover-found" className="discover-count">
            {foundWords(results.results.length, canShowMore(results.more, results.received))}
          </h2>
          <Items label="Businesses found">
            {results.results.map((r) => (
              <DiscoverRow key={r.business} r={r} onChat={() => nav(chatPath({ business: r.business }).to)} />
            ))}
          </Items>
          {moreError ? (
            <p className="warning" role="alert" data-testid="discover-more-problem">
              {moreSaid ?? (moreError instanceof Error ? moreError.message : "More could not be loaded.")}
            </p>
          ) : null}
          {canShowMore(results.more, results.received) && (
            <button type="button" className="discover-more" disabled={moreBusy} onClick={loadMore} data-testid="discover-more">
              {moreBusy ? "Loading…" : moreError ? "Try again" : "Show more businesses"}
            </button>
          )}
        </section>
      )}
    </div>
  );
}

/** A business's profile in Discover, /you/discover/:business. */
export function DiscoverBusiness() {
  const { business = "" } = useParams();
  const nav = useNavigate();
  const view = useLoad(() => get<DiscoverProfile>(`/v1/discover/${encodeURIComponent(business)}`), [business]);
  const [reporting, setReporting] = useState(false);
  const p = view.data;
  const open = (tool?: "shop" | "book") => {
    if (!p) return;
    const to = chatPath(p, tool);
    nav(to.to, to.tool ? { state: { tool: to.tool } } : undefined);
  };
  const gone = discoverProblem(view.error, "profile");
  return (
    <>
      <p className="crumbs">
        <Link to={discoverHome()} aria-label="Back to Discover">
          Discover
        </Link>
      </p>
      {!p && !view.error && <Skeleton rows={4} label="Loading the business…" />}
      {gone ? (
        <section className="card" data-testid="discover-gone">
          <h1>Not in Discover</h1>
          <p>{gone}</p>
          <Link to={discoverHome()}>Back to Discover</Link>
        </section>
      ) : (
        <ErrorBox error={view.error} />
      )}
      {p && (
        <>
          <section className="card discover-profile" aria-labelledby="discover-business" data-testid="discover-profile">
            <div className="discover-who">
              <ChatAvatar name={p.name} logo={photoAt(p.photo?.urls, 320)} className="chat-link-avatar" />
              <div className="grow">
                <h1 id="discover-business">{p.name}</h1>
                {p.tagline && <p className="discover-tagline">{p.tagline}</p>}
                <p className="hint">{[p.category_name, p.area].filter(Boolean).join(" · ")}</p>
                {p.line && (
                  <p className="discover-open">
                    <span className={`badge sf-status ${p.open_now ? "ok" : "muted"}`}>{openNow(p.open_now)}</span> <span className="hint">{p.line}</span>
                  </p>
                )}
              </div>
            </div>
            <div className="row-actions discover-acts">
              {p.can_chat ? (
                <>
                  <button type="button" className="primary" onClick={() => open()} data-testid="discover-chat">
                    <Icon name="chat" /> Chat
                  </button>
                  {p.has_shop && (
                    <button type="button" onClick={() => open("shop")} data-testid="discover-shop">
                      <Icon name="store" /> Their shop
                    </button>
                  )}
                  {p.has_book && (
                    <button type="button" onClick={() => open("book")} data-testid="discover-book">
                      <Icon name="calendar" /> Book
                    </button>
                  )}
                </>
              ) : (
                <p className="notice" role="status" data-testid="discover-no-chat">
                  {p.name} is not taking chats in Taskiem right now.{p.shop ? " You can still visit their page." : ""}
                </p>
              )}
            </div>
          </section>
          {p.photos.length > 0 && (
            <section className="card" aria-labelledby="discover-photos">
              <h2 id="discover-photos">Photos</h2>
              <ul className="discover-photos" aria-label={`Photos of ${p.name}`} tabIndex={0}>
                {p.photos.map((ph, i) => {
                  const src = photoAt(ph.urls, 640);
                  return src ? (
                    <li key={i}>
                      <img src={src} alt={ph.caption || `Photo ${i + 1} of ${p.name}`} loading={i === 0 ? "eager" : "lazy"} />
                      {ph.caption && <span className="discover-caption">{ph.caption}</span>}
                    </li>
                  ) : null;
                })}
              </ul>
            </section>
          )}
          <ProfileHours p={p} />
          {(p.address || p.map_link || p.shop) && (
            <section className="card" aria-labelledby="discover-where">
              <h2 id="discover-where">Where they are</h2>
              {p.address && <p data-testid="discover-address">{p.address}</p>}
              <div className="row-actions">
                {p.map_link && (
                  <a className="button" href={p.map_link} target="_blank" rel="noopener noreferrer">
                    <Icon name="pin" /> Open in your map
                  </a>
                )}
                {p.shop && (
                  <a className="button" href={p.shop} target="_blank" rel="noopener noreferrer">
                    Visit their page
                  </a>
                )}
              </div>
            </section>
          )}
          <p className="discover-report">
            <button type="button" className="link" onClick={() => setReporting(true)} data-testid="discover-report">
              Report {p.name}
            </button>
          </p>
          {reporting && <ReportListing business={p.business} name={p.name} onClose={() => setReporting(false)} />}
        </>
      )}
    </>
  );
}

function ProfileHours({ p }: { p: DiscoverProfile }) {
  const lines = hoursLines(p.hours as ChatLinkCard["hours"]);
  if (lines.length === 0) return null;
  const note = (p.hours as { note?: string }).note;
  return (
    <section className="card" aria-labelledby="discover-hours">
      <h2 id="discover-hours">Opening hours</h2>
      <ul className="discover-hours" data-testid="discover-hours">
        {lines.map((h) => (
          <li key={h}>{h}</li>
        ))}
      </ul>
      {note && <p className="hint">{note}</p>}
      <p className="hint">Lagos time.</p>
    </section>
  );
}

/** Report this business: why, and a note. Three a day per business. */
function ReportListing({ business, name, onClose }: { business: string; name: string; onClose: () => void }) {
  const [reason, setReason] = useState<(typeof DISCOVER_REPORT_REASONS)[number]["key"]>();
  const [note, setNote] = useState("");
  const [said, setSaid] = useState<string>();
  const act = useAction();
  const problem = discoverProblem(act.error, "report");
  return (
    <Sheet
      title={said ? "Reported" : `Report ${name}`}
      onClose={onClose}
      testid="discover-report-sheet"
      footer={
        said ? undefined : (
          <button
            type="button"
            className="tk-cta"
            disabled={!reason || act.busy}
            data-testid="discover-report-send"
            onClick={() =>
              void act.run(async () => {
                const r = await post<{ reported: boolean; words: string }>(`/v1/discover/${encodeURIComponent(business)}/report`, { reason, ...(note.trim() ? { note: note.trim() } : {}) });
                setSaid(r.words);
              }, false)
            }
          >
            {act.busy ? "Sending…" : "Send report"}
          </button>
        )
      }
    >
      {said ? (
        <div className="pc-done">
          <p role="status" data-testid="discover-reported">
            {said}
          </p>
        </div>
      ) : (
        <div className="pc-check">
          <fieldset className="pc-choice">
            <legend>What is wrong?</legend>
            {DISCOVER_REPORT_REASONS.map((r) => (
              <label key={r.key} className="switch-line">
                <input type="radio" name="discover-report-reason" checked={reason === r.key} onChange={() => setReason(r.key)} /> <span>{r.words}</span>
              </label>
            ))}
          </fieldset>
          <KitField label="Tell us more" hint="Optional">
            <textarea rows={3} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
          </KitField>
          <p className="hint">Taskiem's moderators look at every report. {name} is not told who sent it.</p>
          {problem ? (
            <p className="error" role="alert">
              {problem}
            </p>
          ) : (
            <ErrorBox error={act.error} />
          )}
        </div>
      )}
    </Sheet>
  );
}
