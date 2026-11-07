import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Link, useNavigate } from "react-router-dom";
import { get, post } from "../api";
import { Icon } from "../icons";
import { photoSrc, type BusinessProfile } from "../lib/businessProfile";
import { blindRowLine, type BusinessLink } from "../lib/links";
import {
  cardLine,
  cardOffer,
  exploreLogo,
  explorePhoto,
  pendingTo,
  slideAt,
  slideNext,
  type ExploreCard,
  type ExploreResult,
} from "../lib/explore";
import type { Conversation } from "../lib/linkchat";
import { EmptyState, ErrorBox, Skeleton, useAction, useFocusTrap } from "../ui";
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
}: {
  business: string;
  name: string;
  count: number;
  v?: string;
  /** Each photo's words, shown on it (the Explore design). */
  captions?: string[];
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
        onScroll={(e) =>
          setAt(
            slideAt(
              e.currentTarget.scrollLeft,
              e.currentTarget.clientWidth,
              count,
            ),
          )
        }
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
  /** A list item in Explore; a block of its own in a sheet. */
  as?: "li" | "div";
}) {
  const offer = asked ? "requested" : cardOffer(card.state);
  const line = cardLine(card);
  const Tag = as;
  return (
    <Tag
      className="explore-card tk-x-card"
      data-testid="explore-card"
      aria-label={card.name}
    >
      <div className="explore-head">
        <ChatAvatar
          name={card.name}
          className="tk-x-logo"
          logo={card.logo ? exploreLogo(card.business, card.v) : undefined}
        />
        <div className="explore-who">
          <strong>{card.name}</strong>
          {line && <span className="hint">{line}</span>}
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
      {card.photos > 0 && (
        <Carousel
          business={card.business}
          name={card.name}
          count={card.photos}
          v={card.v}
          captions={card.captions}
        />
      )}
      {card.about && <p className="explore-about">{card.about}</p>}
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
  useFocusTrap(box, true, onClose);
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
      const out = await post<{ link?: BusinessLink }>("/v1/links", {
        business: card.business,
        role: "both",
      });
      setAsked((m) => ({ ...m, [card.business]: out.link ?? true }));
      onChanged();
    }, `Asked ${card.name} to link`);
  const withdraw = (card: ExploreCard, link: BusinessLink) =>
    void act.run(async () => {
      await post(`/v1/links/${link.id}/cancel`);
      setAsked((m) =>
        Object.fromEntries(
          Object.entries(m).filter(([k]) => k !== card.business),
        ),
      );
      onChanged();
      // The card says Connect again.
      setStart(
        (s) =>
          s && {
            ...s,
            near: mark(s.near, card.business),
            fresh: mark(s.fresh, card.business),
          },
      );
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
        await post(`/v1/links/${link.id}/${answer}`);
        const state: ExploreCard["state"] = answer === "accept" ? "active" : "none";
        const put = (cards: ExploreCard[]) =>
          cards.map((x) => (x.business === card.business ? { ...x, state } : x));
        setStart((s) => s && { ...s, near: put(s.near), fresh: put(s.fresh) });
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
  const loadMore = (section: "near" | "new") =>
    void act.run(async () => {
      setMore(true);
      try {
        const have =
          (section === "near" ? start?.near : start?.fresh)?.length ?? 0;
        const r = await get<ExploreResult>(
          `/v1/explore?section=${section}&offset=${have}`,
        );
        setStart(
          (s) =>
            s &&
            (section === "near"
              ? { ...s, near: [...s.near, ...r.near], near_more: r.near_more }
              : { ...s, fresh: [...s.fresh, ...r.fresh], more: r.more }),
        );
      } finally {
        setMore(false);
      }
    }, false);

  const cardOf = (c: ExploreCard) => {
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
        onMessage={() =>
          void (async () => {
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
              {results.results.map(cardOf)}
            </ul>
          )
        ) : !start ? (
          <Skeleton rows={3} label="Loading businesses…" />
        ) : start.near.length === 0 && start.fresh.length === 0 ? (
          <EmptyState icon="search">
            No businesses to show yet. Businesses appear here when they choose
            to be found.
          </EmptyState>
        ) : (
          <>
            {start.near.length > 0 && (
              <section aria-labelledby="explore-near">
                <div className="tk-x-sec">
                  <h3 id="explore-near">
                    Near you
                    {start.area && <span> · {start.area}</span>}
                  </h3>
                  {start.near_more && (
                    <button
                      type="button"
                      className="explore-more"
                      disabled={act.busy || more}
                      onClick={() => loadMore("near")}
                    >
                      Show more
                      <span className="sr-only"> businesses near you</span>
                    </button>
                  )}
                </div>
                <ul className="explore-list">{start.near.map(cardOf)}</ul>
              </section>
            )}
            {start.fresh.length > 0 && (
              <section aria-labelledby="explore-new">
                <div className="tk-x-sec">
                  <h3 id="explore-new">New on Taskiem</h3>
                  {start.more && (
                    <button
                      type="button"
                      className="explore-more"
                      disabled={act.busy || more}
                      onClick={() => loadMore("new")}
                    >
                      Show more
                      <span className="sr-only"> new businesses</span>
                    </button>
                  )}
                </div>
                <ul className="explore-list">{start.fresh.map(cardOf)}</ul>
              </section>
            )}
          </>
        )}
      </div>
    </div>,
    document.body,
  );
}

/** The cards with one business put back to "none": a withdrawn request. */
function mark(cards: ExploreCard[], business: string): ExploreCard[] {
  return cards.map((c) =>
    c.business === business ? { ...c, state: "none" } : c,
  );
}
