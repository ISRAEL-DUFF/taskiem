import { useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Link, useNavigate } from "react-router-dom";
import { get, post, put } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { photoSrc, type BusinessProfile } from "../lib/businessProfile";
import { blindRowLine, type BusinessLink, type LinkSettings } from "../lib/links";
import {
  CATEGORY_NAMES,
  cardLine,
  cardOffer,
  chipOrder,
  popularLine,
  recentlyViewed,
  rememberViewed,
  replaceViewed,
  exploreLogo,
  explorePhoto,
  pendingTo,
  slideAt,
  slideNext,
  type ExploreCard,
  type ExploreCategory,
  type ExploreResult,
} from "../lib/explore";
import type { Conversation } from "../lib/linkchat";
import { EmptyState, ErrorBox, Skeleton, useAction, useFocusTrap } from "../ui";
import { AskCategories } from "./CategoryPicker";
import { ChatAvatar } from "./ChatAvatar";

// Explore: finding a business by browsing, on a screen of its own over the
// chats. A search box, then cards (the business's logo and name, a line, a
// swipe of its photos with dots, and Connect). Connect asks the business to
// link at once, as supplier and customer both; nothing is shared until it
// accepts. Only businesses that chose to be found are here.

const reduced = () =>
  typeof window.matchMedia === "function" &&
  window.matchMedia("(prefers-reduced-motion: reduce)").matches;

/** The photos of a business: a row that swipes, with dots under it. */
export function Carousel({
  business,
  name,
  count,
  v,
  captions = [],
  onLook,
}: {
  business: string;
  name: string;
  count: number;
  v?: string;
  /** Each photo's words, shown on it (the Explore design). */
  captions?: string[];
  /** Another photo shown: the business was looked at (Recently viewed). */
  onLook?: () => void;
}) {
  const track = useRef<HTMLDivElement>(null);
  const [at, setAt] = useState(0);
  const [bad, setBad] = useState<Set<number>>(new Set());
  const go = (n: number) => {
    const el = track.current;
    if (!el) return;
    el.scrollTo({
      left: n * el.clientWidth,
      behavior: reduced() ? "auto" : "smooth",
    });
  };
  return (
    <div
      className="carousel"
      role="group"
      aria-roledescription="carousel"
      aria-label={`Photos of ${name}`}
    >
      <div
        ref={track}
        className="carousel-track"
        role="group"
        tabIndex={0}
        aria-label={`Photos of ${name}, swipe or use the arrow keys`}
        onScroll={(e) => {
          const n = slideAt(
            e.currentTarget.scrollLeft,
            e.currentTarget.clientWidth,
            count,
          );
          if (n !== at) {
            setAt(n);
            onLook?.();
          }
        }}
        onKeyDown={(e) => {
          if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
          e.preventDefault();
          go(slideNext(at, e.key === "ArrowLeft" ? -1 : 1, count));
        }}
      >
        {Array.from({ length: count }, (_, n) => (
          <div
            key={n}
            role="group"
            aria-roledescription="slide"
            aria-label={`${n + 1} of ${count}`}
            className={bad.has(n) ? "carousel-slide bad" : "carousel-slide"}
          >
            {!bad.has(n) && (
              <img
                src={explorePhoto(business, n, v)}
                alt={captions[n] || `Photo ${n + 1} of ${name}`}
                loading={n === 0 ? "eager" : "lazy"}
                draggable={false}
                onError={() => setBad((s) => new Set(s).add(n))}
              />
            )}
            {bad.has(n) && <Icon name="image" />}
            {captions[n] && (
              <span className="tk-x-cap" aria-hidden="true">
                {captions[n]}
              </span>
            )}
          </div>
        ))}
      </div>
      {count > 1 && (
        <div className="carousel-dots">
          {Array.from({ length: count }, (_, n) => (
            <button
              key={n}
              type="button"
              className="carousel-dot"
              aria-label={`Show photo ${n + 1} of ${count}`}
              aria-current={n === at ? "true" : undefined}
              onClick={() => go(n)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

/** One business: who it is and what can be done, its photos, its words. */
export function Card({
  card,
  manage,
  busy,
  asked,
  pending,
  onConnect,
  onWithdraw,
  onMessage,
  incoming,
  onAnswer,
  onLook,
  categories = [],
  featured = false,
  as = "li",
}: {
  card: ExploreCard;
  manage: boolean;
  busy: boolean;
  /** We asked it just now (the page has not heard back from the server's list yet). */
  asked: boolean;
  pending?: BusinessLink;
  onConnect: () => void;
  onWithdraw: () => void;
  onMessage: () => void;
  /** Their ask to us (state asked_us), and how it is answered. */
  incoming?: BusinessLink;
  onAnswer?: (l: BusinessLink, answer: "accept" | "decline") => void;
  /** Its photos swiped: it was looked at (Recently viewed). */
  onLook?: () => void;
  /** Its categories' names, under its line. */
  categories?: string[];
  /** The Featured row's wide card: its first photo as a cover; no swipe, no words about it. */
  featured?: boolean;
  /** A list item in Explore; a block of its own in a sheet. */
  as?: "li" | "div";
}) {
  const offer = asked ? "requested" : cardOffer(card.state);
  const line = cardLine(card);
  const popular = popularLine(card);
  const Tag = as;
  return (
    <Tag
      className="explore-card tk-x-card"
      data-testid="explore-card"
      aria-label={card.name}
    >
      {featured && (
        <div className="tk-x-cover">
          {card.photos > 0 ? (
            <img src={explorePhoto(card.business, 0, card.v)} alt={card.captions?.[0] || `Photo of ${card.name}`} loading="lazy" draggable={false} />
          ) : (
            <span aria-hidden="true">{card.name.slice(0, 1).toUpperCase()}</span>
          )}
        </div>
      )}
      <div className="explore-head">
        <ChatAvatar
          name={card.name}
          className="tk-x-logo"
          logo={card.logo ? exploreLogo(card.business, card.v) : undefined}
        />
        <div className="explore-who">
          <strong>{card.name}</strong>
          {line && <span className="hint">{line}</span>}
          {categories.length > 0 && (
            <span className="tk-x-cats">{categories.join(" · ")}</span>
          )}
          {popular && <span className="tk-x-links">{popular}</span>}
        </div>
        <div className="explore-act">
          {offer === "connect" &&
            (manage ? (
              <button
                type="button"
                className="tk-x-connect"
                disabled={busy}
                onClick={onConnect}
              >
                Connect<span className="sr-only"> with {card.name}</span>
              </button>
            ) : (
              <span className="tk-pill">Not linked</span>
            ))}
          {offer === "requested" && (
            <span className="tk-x-asked">
              <span className="tk-pill lime">Requested</span>
              {manage && (asked || pending) && (
                <button
                  type="button"
                  className="tk-x-withdraw"
                  disabled={busy || !pending}
                  onClick={onWithdraw}
                >
                  Withdraw
                  <span className="sr-only"> request to {card.name}</span>
                </button>
              )}
            </span>
          )}
          {offer === "message" && (
            <button type="button" className="tk-x-connect soft" onClick={onMessage}>
              Message<span className="sr-only"> {card.name}</span>
            </button>
          )}
          {offer === "asked" &&
            (manage && incoming && onAnswer ? (
              <span className="tk-x-asked">
                <button
                  type="button"
                  className="tk-x-connect"
                  disabled={busy}
                  onClick={() => onAnswer(incoming, "accept")}
                >
                  Accept<span className="sr-only"> {card.name}'s request to link</span>
                </button>
                <button
                  type="button"
                  className="tk-x-withdraw"
                  disabled={busy}
                  onClick={() => onAnswer(incoming, "decline")}
                >
                  Not now<span className="sr-only">: say no to {card.name}</span>
                </button>
              </span>
            ) : (
              <span className="tk-pill lime">Asked you</span>
            ))}
        </div>
      </div>
      {card.photos > 0 && !featured && (
        <Carousel
          business={card.business}
          name={card.name}
          count={card.photos}
          v={card.v}
          captions={card.captions}
          onLook={onLook}
        />
      )}
      {card.about && !featured && <p className="explore-about">{card.about}</p>}
      {!manage && (offer === "connect" || offer === "asked") && (
        <p className="tk-x-note">An owner or admin can connect.</p>
      )}
      {offer === "asked" && manage && (
        <p className="tk-x-note">
          {card.name} asked to link with you. Accept to message, share price
          lists and order.
        </p>
      )}
    </Tag>
  );
}

/**
 * The Explore screen: over the chat page, with a back arrow. Esc and the arrow
 * close it. With nothing typed it lists businesses near you and the newest;
 * typing searches (a moment after you stop).
 */
export function Explore({
  links,
  convs,
  manage,
  onClose,
  onChanged,
}: {
  links: BusinessLink[];
  convs: Conversation[];
  manage: boolean;
  onClose: () => void;
  onChanged: () => void;
}) {
  const box = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();
  // A dialog opened over it ("What do you sell?") owns the keys while open.
  useFocusTrap(box, true, onClose, () => !box.current?.querySelector(".modal"));
  // The page behind does not scroll.
  useEffect(() => {
    document.documentElement.classList.add("no-scroll");
    return () => document.documentElement.classList.remove("no-scroll");
  }, []);
  const [q, setQ] = useState("");
  const [start, setStart] = useState<ExploreResult>();
  const [found, setFound] = useState<{ q: string; r: ExploreResult }>();
  const [error, setError] = useState<unknown>();
  const [more, setMore] = useState(false);
  const act = useAction();
  const [blindSent, setBlindSent] = useState<string>();
  const [asked, setAsked] = useState<Record<string, BusinessLink | true>>({});
  const typed = q.trim();
  // Made findable here with no category: asked what it sells.
  const [askCats, setAskCats] = useState(false);
  const doneAsking = useCallback(() => setAskCats(false), []);
  // Recently viewed: this browser only, for the business signed in.
  // Per person and business: someone else signing in here does not see it.
  const me = useAuth().me;
  const owner = me?.tenant_id && me.user?.id ? `${me.user.id}:${me.tenant_id}` : undefined;
  const [recent, setRecent] = useState<ExploreCard[]>(() => recentlyViewed(owner));
  // Checked with the server once open: a business that stopped being
  // findable (or was suspended or reported) goes; the rest as they are now.
  // Offline, the list stays as it was.
  useEffect(() => {
    const was = recentlyViewed(owner);
    if (was.length === 0) return;
    let live = true;
    get<ExploreResult>(`/v1/explore?section=known&ids=${was.map((c) => c.business).join(",")}`).then(
      (r) => {
        // Only what was asked about: nothing looked at meanwhile is lost.
        if (!live || !r.known) return;
        const known = r.known;
        setRecent((now) => (now.length === was.length && now.every((c, i) => c.business === was[i]?.business) ? replaceViewed(owner, known) : now));
      },
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [owner]);
  const seen = (c: ExploreCard) =>
    setRecent((r) => (r[0]?.business === c.business ? r : rememberViewed(owner, c)));
  // A category chosen from the chips: its businesses in the sections' place.
  const [chosen, setChosen] = useState<string>();
  const [inCat, setInCat] = useState<ExploreResult>();
  // The category last asked for: an answer for another one comes too late.
  const wanted = useRef<string | undefined>(undefined);
  const pick = (key: string) => {
    const next = key === chosen ? undefined : key;
    wanted.current = next;
    setChosen(next);
    setInCat(undefined);
    if (!next) return;
    get<ExploreResult>(`/v1/explore?section=category&category=${encodeURIComponent(next)}`).then(
      (r) => wanted.current === next && setInCat({ ...r, in_category: r.in_category ?? [] }),
      (e: unknown) => {
        // Failed: said, and the chip let go, so a tap tries again.
        if (wanted.current !== next) return;
        setError(e);
        wanted.current = undefined;
        setChosen(undefined);
      },
    );
  };
  // A card kept in Recently viewed, with our link with it as it is now (it
  // was kept as it was then): the page's own copy when it has one, else
  // what the chats know.
  const current = (c: ExploreCard): ExploreCard => {
    const s = start;
    const shown = s
      ? [...s.near, ...(s.across ?? []), ...(s.featured ?? []), ...(s.trusted ?? []), ...(s.popular ?? []), ...(s.delivery ?? []), ...s.fresh].find((x) => x.business === c.business)
      : undefined;
    const state: ExploreCard["state"] =
      shown?.state ??
      (asked[c.business]
        ? "requested"
        : convs.some((x) => x.business === c.business)
          ? "active"
          : pendingTo(links, c.business)
            ? "requested"
            : links.some((l) => l.business === c.business && l.state === "asked_us")
              ? "asked_us"
              : c.state === "active" || c.state === "requested" || c.state === "asked_us"
                ? "none"
                : c.state);
    return state === c.state ? c : { ...c, state };
  };
  const nameOf = (key: string) =>
    start?.categories?.find((c) => c.key === key)?.name ?? CATEGORY_NAMES[key] ?? key;
  // Ours, for the way to "Your business in Explore".
  const [mine, setMine] = useState<BusinessProfile>();
  useEffect(() => {
    let live = true;
    get<BusinessProfile>("/v1/business-profile").then(
      (p) => live && setMine(p),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, []);

  // Whether other businesses can find us: said, with the switch, when
  // there is no one to show.
  const [settings, setSettings] = useState<LinkSettings>();
  useEffect(() => {
    let live = true;
    get<LinkSettings>("/v1/link-settings").then(
      (r) => live && setSettings(r),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => {
    let live = true;
    get<ExploreResult>("/v1/explore").then(
      (r) => live && setStart(r),
      (e: unknown) => live && setError(e),
    );
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => {
    if (!typed) return;
    let live = true;
    const t = setTimeout(() => {
      get<ExploreResult>(`/v1/explore?q=${encodeURIComponent(typed)}`).then(
        (r) => live && (setFound({ q: typed, r }), setError(undefined)),
        (e: unknown) => live && setError(e),
      );
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [typed]);

  const connect = (card: ExploreCard) =>
    void act.run(async () => {
      seen(card);
      const out = await post<{ link?: BusinessLink }>("/v1/links", {
        business: card.business,
        role: "both",
      });
      setAsked((m) => ({ ...m, [card.business]: out.link ?? true }));
      onChanged();
    }, `Asked ${card.name} to link`);
  const withdraw = (card: ExploreCard, link: BusinessLink) =>
    void act.run(async () => {
      seen(card);
      await post(`/v1/links/${link.id}/cancel`);
      setAsked((m) =>
        Object.fromEntries(
          Object.entries(m).filter(([k]) => k !== card.business),
        ),
      );
      onChanged();
      // The card says Connect again, in every section.
      const back = (cards: ExploreCard[]) => mark(cards, card.business);
      setStart((s) => s && everyList(s, back));
      setInCat((s) => s && everyList(s, back));
      setFound(
        (f) =>
          f && {
            ...f,
            r: { ...f.r, results: mark(f.r.results, card.business) },
          },
      );
    }, "Request withdrawn");
  // Their ask, answered here: linked (Message), or put back to Connect.
  const answerAsk = (
    card: ExploreCard,
    link: BusinessLink,
    answer: "accept" | "decline",
  ) =>
    void act.run(
      async () => {
        seen(card);
        await post(`/v1/links/${link.id}/${answer}`);
        const state: ExploreCard["state"] = answer === "accept" ? "active" : "none";
        const put = (cards: ExploreCard[]) =>
          cards.map((x) => (x.business === card.business ? { ...x, state } : x));
        setStart((s) => s && everyList(s, put));
        setInCat((s) => s && everyList(s, put));
        setFound((f) => f && { ...f, r: { ...f.r, results: put(f.r.results) } });
        onChanged();
      },
      answer === "accept" ? `Linked with ${card.name}` : "Request declined",
    );
  const blindSend = (shown: string) =>
    void act.run(async () => {
      await post("/v1/links", { phone: typed, role: "both" });
      setBlindSent(typed);
      onChanged();
    }, `Request sent to ${shown}`);
  const loadMore = (section: MoreSection) =>
    void act.run(async () => {
      setMore(true);
      try {
        const from = section === "category" ? inCat : start;
        const forCategory = chosen;
        const [list, flag] = MORE[section];
        const have = (from?.[list] as ExploreCard[] | undefined)?.length ?? 0;
        const r = await get<ExploreResult>(
          `/v1/explore?section=${section}&offset=${have}${section === "category" && chosen ? `&category=${encodeURIComponent(chosen)}` : ""}`,
        );
        // Each business once: a list's order can change between pages.
        const grow = (s: ExploreResult | undefined) => {
          if (!s) return s;
          const have = (s[list] as ExploreCard[] | undefined) ?? [];
          const fresh = ((r[list] as ExploreCard[] | undefined) ?? []).filter((c) => !have.some((x) => x.business === c.business));
          return { ...s, [list]: [...have, ...fresh], [flag]: !!r[flag] };
        };
        if (section === "category") {
          // Another category chosen meanwhile: this page is not its.
          if (wanted.current === forCategory) setInCat(grow);
        } else setStart(grow);
      } finally {
        setMore(false);
      }
    }, false);

  const cardOf = (c: ExploreCard, featured = false) => {
    const a = asked[c.business];
    return (
      <Card
        key={c.business}
        card={c}
        manage={manage}
        busy={act.busy}
        asked={!!a}
        pending={
          (a && a !== true ? a : undefined) ?? pendingTo(links, c.business)
        }
        onConnect={() => connect(c)}
        onWithdraw={() => {
          const l =
            (a && a !== true ? a : undefined) ?? pendingTo(links, c.business);
          if (l) withdraw(c, l);
        }}
        incoming={links.find(
          (l) => l.business === c.business && l.state === "asked_us",
        )}
        onAnswer={(l, answer) => answerAsk(c, l, answer)}
        onLook={() => seen(c)}
        featured={featured}
        categories={(c.categories ?? []).map(nameOf)}
        onMessage={() =>
          void (async () => {
            seen(c);
            // The chat list may not have heard yet that they accepted.
            let conv = convs.find((x) => x.business === c.business);
            if (!conv) {
              const r = await get<{ conversations: Conversation[] }>(
                "/v1/link-chats",
              ).catch(() => undefined);
              conv = r?.conversations.find((x) => x.business === c.business);
            }
            // The chat takes the place of Explore in the history.
            navigate(conv ? `/chat?with=${conv.link}` : "/chat", {
              replace: true,
            });
          })()
        }
      />
    );
  };

  const searching = typed.length > 0;
  const results = found && found.q === typed ? found.r : undefined;
  return createPortal(
    <div
      className="explore tk-x"
      role="dialog"
      aria-modal="true"
      aria-label="Find a business"
      ref={box}
      data-testid="explore"
    >
      <header className="tk-x-head">
        <button
          type="button"
          className="tk-x-back"
          aria-label="Back to the chats"
          onClick={onClose}
          data-testid="explore-back"
        >
          <Icon name="back" />
        </button>
        <h2 className="tk-x-title">Find a business</h2>
        <div className="tk-x-search" role="search">
          <label>
            <span className="sr-only">Search businesses</span>
            <Icon name="search" />
            {/* The focus trap gives it the focus as it starts listening
                for Esc, so an Esc at once is never missed. */}
            <input
              type="search"
              value={q}
              placeholder="A name, what they sell, or an area"
              onChange={(e) => setQ(e.target.value)}
              data-testid="explore-search"
            />
          </label>
        </div>
        <Link
          to="/settings/links/profile"
          className="tk-x-mine"
          data-testid="explore-mine"
        >
          <ChatAvatar
            name={mine?.name || "Your business"}
            logo={mine?.logo ? photoSrc(mine.logo) : undefined}
          />
          Your business in Explore
        </Link>
      </header>
      <div className="explore-body tk-x-body">
        {askCats && <AskCategories onClose={doneAsking} />}
        <ErrorBox error={error ?? act.error} />
        {searching ? (
          !results ? (
            <Skeleton rows={2} label="Searching businesses…" />
          ) : results.blind ? (
            <ul className="explore-list">
              <li className="explore-card tk-x-card" data-testid="explore-blind">
                <div className="explore-head">
                  <span className="chat-avatar" aria-hidden="true">
                    <Icon name="chat" />
                  </span>
                  <div className="explore-who">
                    <strong>
                      Send a link request to {results.blind.shown}
                    </strong>
                    <span className="hint">
                      {blindRowLine(results.blind.kind)}
                    </span>
                  </div>
                  {manage &&
                    (blindSent === typed ? (
                      <span className="explore-state">Request sent</span>
                    ) : (
                      <button
                        type="button"
                        className="tk-x-connect"
                        disabled={act.busy}
                        onClick={() => blindSend(results.blind?.shown ?? "")}
                      >
                        Send request
                      </button>
                    ))}
                </div>
              </li>
            </ul>
          ) : results.results.length === 0 ? (
            <EmptyState icon="search">
              No business found for “{typed}”. Only businesses that chose to be
              found are listed.
            </EmptyState>
          ) : (
            <ul className="explore-list" aria-label="Businesses found">
              {results.results.map((c) => cardOf(c))}
            </ul>
          )
        ) : !start ? (
          <Skeleton rows={3} label="Loading businesses…" />
        ) : (
          <>
            {(start.categories ?? []).length > 0 && (
              <Chips cats={start.categories ?? []} chosen={chosen} onPick={pick} />
            )}
            {chosen ? (
              <section aria-labelledby="explore-category">
                <SectionHead
                  id="explore-category"
                  title={nameOf(chosen)}
                  more={!!inCat?.category_more}
                  busy={act.busy || more}
                  moreWords={`businesses in ${nameOf(chosen)}`}
                  onMore={() => loadMore("category")}
                />
                {!inCat ? (
                  <Skeleton rows={2} label={`Loading ${nameOf(chosen)}…`} />
                ) : (inCat.in_category ?? []).length === 0 ? (
                  <EmptyState icon="search">
                    No business in {nameOf(chosen)} can be found yet. Businesses
                    choose their categories in Your business in Explore.
                  </EmptyState>
                ) : (
                  <ul className="explore-list" aria-label={nameOf(chosen)}>
                    {(inCat.in_category ?? []).map((c) => cardOf(c))}
                  </ul>
                )}
              </section>
            ) : (
              <>
                {nobody(start) && (
                  <EmptyState
                    icon="search"
                    action={
                      <div className="tk-x-empty-acts">
                        {manage && settings && !settings.findable_now && (
                          <button
                            type="button"
                            className="tk-x-connect"
                            disabled={act.busy}
                            onClick={() =>
                              void act
                                .run(async () => {
                                  setSettings(await put<LinkSettings>("/v1/link-settings", { findable: true }));
                                }, "Other businesses can find you")
                                .then((ok) => ok && setAskCats(true))
                            }
                          >
                            Let other businesses find me
                          </button>
                        )}
                        <Link to="/settings/links/profile" className="link">
                          Your business in Explore
                        </Link>
                      </div>
                    }
                  >
                    No other business can be found yet: businesses appear here
                    once they choose to be found. Be one of the first, so
                    suppliers and customers find you.
                  </EmptyState>
                )}
                {!nobody(start) && !start.area && start.near.length === 0 && (
                  <p className="hint tk-x-area" data-testid="explore-no-area">
                    Add your area in{" "}
                    <Link to="/settings/links/profile" className="link">
                      Your business in Explore
                    </Link>{" "}
                    to see the businesses near you.
                  </p>
                )}
                {(start.featured ?? []).length > 0 && (
                  <section aria-labelledby="explore-featured">
                    <SectionHead id="explore-featured" title="Featured" />
                    {/* It scrolls sideways: reachable by the keyboard too. */}
                    <ul
                      className="explore-list tk-x-featured"
                      aria-label="Featured businesses"
                      data-testid="explore-featured"
                      tabIndex={0}
                    >
                      {(start.featured ?? []).map((c) => cardOf(c, true))}
                    </ul>
                  </section>
                )}
                {(
                  [
                    ["near", "Near you", start.area, start.near, start.near_more, "businesses near you"],
                    // No one in our area (or none set): businesses from anywhere, in its place.
                    ["across", "Across Nigeria", undefined, start.near.length === 0 ? (start.across ?? []) : [], !!start.across_more, "businesses across Nigeria"],
                    ["trusted", "Trusted on Taskiem", undefined, start.trusted ?? [], !!start.trusted_more, "trusted businesses"],
                    ["popular", "Popular", undefined, start.popular ?? [], !!start.popular_more, "popular businesses"],
                    ["delivery", "Delivery", undefined, start.delivery ?? [], start.delivery_more, "delivery businesses"],
                    ["new", "New on Taskiem", undefined, start.fresh, start.more, "new businesses"],
                    ["recent", "Recently viewed", undefined, recent.map(current), false, ""],
                  ] as const
                )
                  .filter(([, , , cards]) => cards.length > 0)
                  .map(([key, title, sub, cards, hasMore, words]) => (
                    <section key={key} aria-labelledby={`explore-${key}`} data-testid={`explore-${key}`}>
                      <SectionHead
                        id={`explore-${key}`}
                        title={title}
                        sub={sub}
                        more={hasMore}
                        busy={act.busy || more}
                        moreWords={words}
                        onMore={key === "recent" ? undefined : () => loadMore(key)}
                      />
                      <ul className="explore-list">{cards.map((c) => cardOf(c))}</ul>
                    </section>
                  ))}
              </>
            )}
          </>
        )}
      </div>
    </div>,
    document.body,
  );
}

type MoreSection = "near" | "new" | "delivery" | "trusted" | "popular" | "category" | "across";

/** Each section with more to show: its list, and whether another page waits. */
const MORE: Record<MoreSection, [keyof ExploreResult, keyof ExploreResult]> = {
  near: ["near", "near_more"],
  new: ["fresh", "more"],
  delivery: ["delivery", "delivery_more"],
  trusted: ["trusted", "trusted_more"],
  popular: ["popular", "popular_more"],
  category: ["in_category", "category_more"],
  across: ["across", "across_more"],
};

/** Every list of cards in an answer, changed the same way. */
function everyList(s: ExploreResult, f: (cards: ExploreCard[]) => ExploreCard[]): ExploreResult {
  return {
    ...s,
    results: f(s.results),
    near: f(s.near),
    fresh: f(s.fresh),
    delivery: f(s.delivery ?? []),
    featured: s.featured && f(s.featured),
    trusted: s.trusted && f(s.trusted),
    popular: s.popular && f(s.popular),
    in_category: s.in_category && f(s.in_category),
    across: s.across && f(s.across),
  };
}

/** No business to show in any section (the categories are still there). */
function nobody(s: ExploreResult): boolean {
  return [s.near, s.across ?? [], s.fresh, s.delivery ?? [], s.featured ?? [], s.trusted ?? [], s.popular ?? []].every((l) => l.length === 0);
}

/** A section's title (what it is about after a dot), and Show more. */
function SectionHead({
  id,
  title,
  sub,
  more = false,
  busy = false,
  moreWords = "",
  onMore,
}: {
  id: string;
  title: string;
  sub?: string;
  more?: boolean;
  busy?: boolean;
  moreWords?: string;
  onMore?: () => void;
}) {
  return (
    <div className="tk-x-sec">
      <h3 id={id}>
        {title}
        {sub && <span> · {sub}</span>}
      </h3>
      {more && onMore && (
        <button type="button" className="explore-more" disabled={busy} onClick={onMore}>
          Show more
          <span className="sr-only"> {moreWords}</span>
        </button>
      )}
    </div>
  );
}

/** The categories in a row that scrolls, each with its count; the chosen one filled. */
function Chips({ cats, chosen, onPick }: { cats: ExploreCategory[]; chosen?: string; onPick: (key: string) => void }) {
  return (
    <div className="tk-x-chips" role="group" aria-label="Categories" data-testid="explore-categories">
      {chipOrder(cats).map((c) => (
        <button
          key={c.key}
          type="button"
          className="tk-x-chip"
          aria-pressed={c.key === chosen}
          aria-label={`${c.name} ${c.n} ${c.n === 1 ? "business" : "businesses"}`}
          onClick={() => onPick(c.key)}
        >
          {c.name}
          {c.n > 0 && " "}
          {c.n > 0 && (
            <span className="tk-x-chip-n">
              {c.n}
            </span>
          )}
        </button>
      ))}
    </div>
  );
}

/** The cards with one business put back to "none": a withdrawn request. */
function mark(cards: ExploreCard[], business: string): ExploreCard[] {
  return cards.map((c) =>
    c.business === business ? { ...c, state: "none" } : c,
  );
}

/** The first few businesses worth showing someone with no links yet (§2.3):
 * Featured first, then near them (or across Nigeria), then those that
 * deliver, then the newest, each once. */
export function firstPicks(r: ExploreResult, n = 3): ExploreCard[] {
  const seen = new Set<string>();
  const out: ExploreCard[] = [];
  for (const c of [...(r.featured ?? []), ...r.near, ...(r.across ?? []), ...(r.delivery ?? []), ...r.fresh]) {
    if (seen.has(c.business) || cardOffer(c.state) !== "connect") continue;
    seen.add(c.business);
    out.push(c);
    if (out.length === n) break;
  }
  return out;
}

/**
 * The chats with no linked business yet (docs/spec/mobile-app-explore-and-
 * offline.md §2.3): never a bare line. A few businesses to link with, each
 * with Connect; Find a business; and, when other businesses cannot find
 * this one, the switch that lets them.
 */
export function GetStarted({
  manage,
  onExplore,
  onChanged,
}: {
  manage: boolean;
  onExplore: () => void;
  onChanged: () => void;
}) {
  const [start, setStart] = useState<ExploreResult>();
  const [settings, setSettings] = useState<LinkSettings>();
  const [asked, setAsked] = useState<Record<string, true>>({});
  const act = useAction();
  // Made findable here with no category: asked what it sells.
  const [askCats, setAskCats] = useState(false);
  const doneAsking = useCallback(() => setAskCats(false), []);
  useEffect(() => {
    let live = true;
    get<ExploreResult>("/v1/explore").then(
      (r) => live && setStart(r),
      () => undefined,
    );
    get<LinkSettings>("/v1/link-settings").then(
      (r) => live && setSettings(r),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, []);
  const picks = start ? firstPicks(start) : [];
  const connect = (c: ExploreCard) =>
    void act.run(async () => {
      await post("/v1/links", { business: c.business, role: "both" });
      setAsked((a) => ({ ...a, [c.business]: true }));
      onChanged();
    }, `Request sent to ${c.name}`);
  return (
    <section className="tk-start" data-testid="chats-start" aria-labelledby="chats-start-title">
      <span className="empty-icon" aria-hidden="true">
        <Icon name="chat" />
      </span>
      <h2 id="chats-start-title">Link with the businesses you trade with</h2>
      <p className="hint">
        Linked, your orders, invoices and payments reach each other by
        themselves, and you chat here.
      </p>
      <ErrorBox error={act.error} />
      {picks.length > 0 && (
        <ul className="tk-start-picks" aria-label="Businesses you could link with">
          {picks.map((c) => (
            <li key={c.business}>
              <ChatAvatar name={c.name} logo={c.logo ? exploreLogo(c.business, c.v) : undefined} />
              <span className="tk-start-who">
                <strong>{c.name}</strong>
                {cardLine(c) && <span className="hint">{cardLine(c)}</span>}
              </span>
              {asked[c.business] ? (
                <span className="tk-pill">Requested</span>
              ) : manage ? (
                <button
                  type="button"
                  className="tk-x-connect"
                  disabled={act.busy}
                  onClick={() => connect(c)}
                  aria-label={`Connect with ${c.name}`}
                >
                  Connect
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
      {askCats && <AskCategories onClose={doneAsking} />}
      <button type="button" className="tk-cta" onClick={onExplore}>
        <Icon name="search" />
        Find a business
      </button>
      {settings && !settings.findable_now && !!settings.asked_at && manage && (
        <p className="tk-start-findable">
          Other businesses cannot find you yet.{" "}
          <button
            type="button"
            className="link"
            disabled={act.busy}
            onClick={() =>
              void act
                .run(async () => {
                  setSettings(await put<LinkSettings>("/v1/link-settings", { findable: true }));
                }, "Other businesses can find you")
                .then((ok) => ok && setAskCats(true))
            }
          >
            Let them find me
          </button>
        </p>
      )}
    </section>
  );
}
