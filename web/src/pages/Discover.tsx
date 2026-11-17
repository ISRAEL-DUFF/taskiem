import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { get, post } from "../api";
import { Icon } from "../icons";
import { KitField, Sheet } from "../kit";
import {
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

/** Discover's start, /you/discover. */
export function Discover() {
  const nav = useNavigate();
  const cats = useLoad(() => get<{ categories: DiscoverCategory[] }>("/v1/discover/categories"), []);
  const [q, setQ] = useState<DiscoverQuery>({ q: "", area: "", category: "" });
  // The search the results are for: the typing settles into it.
  const [asked, setAsked] = useState<DiscoverQuery>(q);
  // The person's place, for this page only: asked for when Near me is
  // tapped, gone when it is tapped again or the page is left.
  const [near, setNear] = useState<DiscoverLocation | null>(null);
  const [locating, setLocating] = useState(false);
  const [placeError, setPlaceError] = useState<string>();
  const [found, setFound] = useState<{ key: string; results: DiscoverResult[]; more: boolean }>();
  const [error, setError] = useState<unknown>();
  const [moreBusy, setMoreBusy] = useState(false);
  const seq = useRef(0);

  useEffect(() => {
    const t = setTimeout(() => setAsked(q), QUIET_MS);
    return () => clearTimeout(t);
  }, [q.q, q.area]);
  const pickCategory = (key: string) => {
    const next = { ...q, category: q.category === key ? "" : key };
    setQ(next);
    setAsked(next);
  };

  const key = JSON.stringify([asked, near]);
  useEffect(() => {
    const n = ++seq.current;
    setError(undefined);
    post<{ results: DiscoverResult[]; more: boolean }>("/v1/discover/search", searchBody(asked, near, 0)).then(
      (r) => n === seq.current && setFound({ key, results: r.results, more: r.more }),
      (e: unknown) => n === seq.current && setError(e),
    );
  }, [key]);
  const results = found?.key === key ? found : undefined;
  const loadMore = () => {
    if (!results || moreBusy) return;
    setMoreBusy(true);
    const n = seq.current;
    post<{ results: DiscoverResult[]; more: boolean }>("/v1/discover/search", searchBody(asked, near, results.results.length))
      .then(
        (r) => {
          if (n !== seq.current) return;
          const have = new Set(results.results.map((x) => x.business));
          setFound({ key, results: [...results.results, ...r.results.filter((x) => !have.has(x.business))], more: r.more });
        },
        (e: unknown) => n === seq.current && setError(e),
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
            onChange={(e) => setQ({ ...q, q: e.target.value })}
            data-testid="discover-search"
          />
        </label>
      </div>
      <div className="discover-where">
        <KitField label="Area">
          <input type="text" value={q.area} maxLength={80} placeholder="Yaba, Ikeja, Wuse" onChange={(e) => setQ({ ...q, area: e.target.value })} data-testid="discover-area" />
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
      {said ? (
        <p className="warning" role="alert" data-testid="discover-problem">
          {said}
        </p>
      ) : (
        <ErrorBox error={error ?? cats.error} />
      )}
      {!results && !error && <Skeleton rows={3} label="Finding businesses…" />}
      {results && results.results.length === 0 && <EmptyState icon="search">{nothingFound(asked, catName, !!near)}</EmptyState>}
      {results && results.results.length > 0 && (
        <section aria-labelledby="discover-found">
          <h2 id="discover-found" className="discover-count">
            {foundWords(results.results.length, results.more)}
          </h2>
          <Items label="Businesses found">
            {results.results.map((r) => (
              <Row
                key={r.business}
                icon="store"
                image={photoAt(r.photo, 160)}
                title={
                  <Link to={`/you/discover/${encodeURIComponent(r.business)}`} className="discover-name">
                    {r.name}
                  </Link>
                }
                side={<span className={`badge sf-status ${r.open_now ? "ok" : "muted"}`}>{openNow(r.open_now)}</span>}
                sub={
                  <span className="sub">
                    {resultLine(r)}
                    {r.line && <span className="block">{r.line}</span>}
                  </span>
                }
                testid="discover-result"
              >
                {r.can_chat && (
                  <button type="button" className="small primary" onClick={() => nav(chatPath({ business: r.business }).to)}>
                    Chat<span className="sr-only"> with {r.name}</span>
                  </button>
                )}
                <Link className="button small" to={`/you/discover/${encodeURIComponent(r.business)}`}>
                  See more<span className="sr-only"> about {r.name}</span>
                </Link>
              </Row>
            ))}
          </Items>
          {results.more && (
            <button type="button" className="discover-more" disabled={moreBusy} onClick={loadMore} data-testid="discover-more">
              {moreBusy ? "Loading…" : "Show more businesses"}
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
        <Link to="/you/discover" aria-label="Back to Discover">
          Discover
        </Link>
      </p>
      {!p && !view.error && <Skeleton rows={4} label="Loading the business…" />}
      {gone ? (
        <section className="card" data-testid="discover-gone">
          <h1>Not in Discover</h1>
          <p>{gone}</p>
          <Link to="/you/discover">Back to Discover</Link>
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
