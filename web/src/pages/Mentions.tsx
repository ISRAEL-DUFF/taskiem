import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type RefObject,
} from "react";
import { get } from "../api";
import { Link } from "react-router-dom";
import { Icon, TaskiemMark } from "../icons";
import { usePresence } from "../kit";
import { initialsOf } from "./ChatAvatar";
import {
  groupSuggestions,
  insertMention,
  mentionParts,
  mentionQuery,
  stillNamed,
  suggest,
  unlinkedSuggestion,
  refStatusWords,
  REF_LABEL,
  type Mention,
  type MentionQuery,
  type RefKind,
  type Suggestion,
  type SuggestionGroup,
} from "../lib/mentions";
import { formatPrice } from "../lib/storefront";
import { exploreLogo, type ExploreCard, type ExploreResult } from "../lib/explore";
import { BusinessCardSheet } from "./BusinessCardSheet";

// Mentions in the composer (business links L4, §5.5): typing "@" offers
// linked businesses, "#" items with their unit and price. Enter or Tab
// takes the first, the arrows move through them, Escape closes them. The
// text box stays a plain text box; the suggestions are buttons.

export type Mentions = ReturnType<typeof useMentions>;

export function useMentions(
  text: string,
  setText: (v: string) => void,
  box: RefObject<HTMLTextAreaElement | null>,
  sources: {
    "@": Suggestion[];
    "#": Suggestion[];
    services?: Suggestion[];
    /** The categories "@" offers, when not the items' own. */
    categories?: { name: string; count: number }[];
    /** What "/invoice", "/product", "/order" and "/customer" offer. */
    refs?: Partial<Record<RefKind, Suggestion[]>>;
  },
  /** Chat tools (chat-tools.md §3): "@" tags things and businesses, in
   * groups, with the Taskiem row when it may be called; a category picked
   * opens its items (onCategory). */
  grouped?: { taskiem: boolean; onCategory?: (name: string) => void },
) {
  const [q, setQ] = useState<MentionQuery | null>(null);
  const [picked, setPicked] = useState<Suggestion[]>([]);
  const list = useRef<HTMLElement>(null);
  // Chat tools phase 6: "@" with a few letters also asks Explore, so a
  // business we are not linked with can be found; picking one shows its
  // card, with Connect.
  const words = q && grouped && q.mark === "@" ? q.query.trim() : "";
  const [found, setFound] = useState<{ q: string; list: Suggestion[] }>();
  const [card, setCard] = useState<{ card: ExploreCard; from?: DOMRect }>();
  useEffect(() => {
    if (words.length < 2) return;
    let live = true;
    const t = window.setTimeout(() => {
      get<ExploreResult>(`/v1/explore?q=${encodeURIComponent(words)}`).then(
        (r) =>
          live &&
          setFound({
            q: words,
            list: r.results
              .map(unlinkedSuggestion)
              .filter((x): x is Suggestion => x !== null),
          }),
        () => live && setFound({ q: words, list: [] }),
      );
    }, 250);
    return () => {
      live = false;
      window.clearTimeout(t);
    };
  }, [words]);
  const unlinked = found && words.length >= 2 && found.q === words ? found.list : [];
  const groups: SuggestionGroup[] =
    q && grouped && q.mark === "@"
      ? groupSuggestions(
          q.query,
          {
            items: sources["#"],
            services: sources.services,
            businesses: sources["@"],
            unlinked,
            categories: sources.categories,
          },
          grouped.taskiem,
        )
      : q?.mark === "/" && q.kind
        ? [
            {
              label: REF_LABEL[q.kind],
              items: suggest(sources.refs?.[q.kind] ?? [], q.query),
            },
          ]
        : q && q.mark !== "/"
          ? [{ label: "", items: suggest(sources[q.mark], q.query) }]
          : [];
  const shown = groups.flatMap((g) => g.items);
  // What Enter takes: the first match. The Taskiem row, always offered,
  // only when "Taskiem" is what is being typed ("@4500" stays as typed).
  // A category is not a tag: Enter tags an item, a tap opens the category.
  const enterPick = shown.find(
    (s) =>
      (s.kind !== "taskiem" && s.kind !== "category") ||
      (q && q.query.length > 0 && "taskiem".startsWith(q.query.toLowerCase())),
  );
  const update = (value: string, caret: number | null) =>
    setQ(mentionQuery(value, caret ?? value.length));
  const pick = (s: Suggestion, from?: DOMRect) => {
    if (!q) return;
    const el = box.current;
    if (s.explore) {
      // Not linked: its card, with Connect; the words stay as typed.
      setCard({ card: s.explore, from });
      setQ(null);
      return;
    }
    const caret = el?.selectionStart ?? text.length;
    if (s.kind === "category") {
      // Its items, filtered: the words typed for it go.
      const end = Math.max(caret, q.start + 1 + q.query.length);
      setText(text.slice(0, q.start) + text.slice(end));
      setQ(null);
      grouped?.onCategory?.(s.name);
      return;
    }
    const next = insertMention(
      text,
      q,
      Math.max(caret, q.start + 1 + q.query.length),
      s.name,
    );
    setText(next.text);
    if (s.kind !== "taskiem")
      setPicked((cur) => [...cur, s]);
    setQ(null);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(next.caret, next.caret);
    });
  };
  /** Keys in the text box: true when a suggestion took them. */
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>): boolean => {
    if (shown.length === 0) return false;
    const first = enterPick;
    if ((e.key === "Enter" || e.key === "Tab") && !e.shiftKey && first) {
      e.preventDefault();
      pick(first);
      return true;
    }
    if (e.key === "Escape") {
      e.preventDefault();
      setQ(null);
      return true;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      list.current?.querySelector("button")?.focus();
      return true;
    }
    return false;
  };
  /** The mentions still named, as a message sends them. */
  const refs = () =>
    stillNamed(text, picked)
      // A customer is named in words only: never sent to another business.
      .filter((p) => p.kind !== "customer")
      .map((p) =>
        p.kind === "business"
          ? { kind: p.kind, link: p.id }
          : p.kind === "service"
            ? { kind: p.kind, service_id: p.id }
            : p.kind === "invoice" ||
                p.kind === "bill" ||
                p.kind === "order" ||
                p.kind === "purchase"
              ? { kind: p.kind, ref: p.id }
              : { kind: p.kind, item_id: p.id },
      );
  const reset = () => {
    setPicked([]);
    setQ(null);
  };
  return {
    q,
    shown,
    enterPick,
    groups,
    grouped: !!grouped,
    list,
    pick,
    onKeyDown,
    update,
    refs,
    reset,
    close: () => setQ(null),
    box,
    card,
    // The sheet asked, withdrew or answered: the row says so at once.
    cardChanged: (business: string, state: ExploreCard["state"]) =>
      setFound(
        (f) =>
          f && {
            ...f,
            list: f.list.flatMap((x) => {
              if (x.explore?.business !== business) return [x];
              const next = unlinkedSuggestion({ ...x.explore, state });
              return next ? [next] : [];
            }),
          },
      ),
    closeCard: () => {
      setCard(undefined);
      requestAnimationFrame(() => box.current?.focus());
    },
  };
}

/** The suggestions over the text box. */
export function MentionList({ m }: { m: Mentions }) {
  if (!m.q || m.shown.length === 0) return null;
  const mark = m.q.mark;
  // The grouped picker floats over the chat from inside the composer.
  if (m.grouped && (mark === "@" || mark === "/")) return null;
  return <PlainList m={m} />;
}

/** The picker of chat tools, springing up and leaving with a little
 * motion: while it leaves it shows what it last showed. */
export function GroupedPicker({ m }: { m: Mentions }) {
  const open =
    !!m.grouped &&
    (m.q?.mark === "@" || m.q?.mark === "/") &&
    m.shown.length > 0;
  const p = usePresence(open, 150);
  const last = useRef<{
    query: string;
    groups: SuggestionGroup[];
    kind?: RefKind;
  }>({
    query: "",
    groups: [],
  });
  if (open)
    last.current = {
      query: m.q?.query ?? "",
      groups: m.groups,
      kind: m.q?.kind,
    };
  return (
    <>
      {p.mounted && (
        <Picker
          m={m}
          query={last.current.query}
          groups={last.current.groups}
          kind={last.current.kind}
          leaving={p.leaving}
        />
      )}
      {m.card && (
        <BusinessCardSheet
          key={m.card.card.business}
          card={m.card.card}
          from={m.card.from}
          onClose={m.closeCard}
          onChanged={m.cardChanged}
        />
      )}
    </>
  );
}

/** Arrow keys between the suggestions, and back to the text box. */
function moveIn(m: Mentions, e: KeyboardEvent<HTMLButtonElement>) {
  const items = Array.from(m.list.current?.querySelectorAll("button") ?? []);
  const i = items.indexOf(e.currentTarget);
  if (e.key === "ArrowDown") {
    e.preventDefault();
    items[Math.min(i + 1, items.length - 1)]?.focus();
  } else if (e.key === "ArrowUp") {
    e.preventDefault();
    if (i <= 0) m.box.current?.focus();
    else items[i - 1]?.focus();
  } else if (e.key === "Escape") {
    e.preventDefault();
    m.close();
    m.box.current?.focus();
  }
}

/** A suggestion's picture: a product's box, a service's clock, a
 * business's initials, Taskiem's spark. */
function PickIcon({ s }: { s: Suggestion }) {
  if (s.kind === "business" && s.explore?.logo)
    return (
      <img
        className="pick-icon round"
        src={exploreLogo(s.explore.business, s.explore.v)}
        alt=""
      />
    );
  if (s.kind === "business")
    return (
      <span
        className={`pick-icon round ${s.explore ? "soft" : "lime"}`}
        aria-hidden="true"
      >
        {initialsOf(s.name)}
      </span>
    );
  if (s.kind === "taskiem")
    return (
      <span className="pick-icon round pine" aria-hidden="true">
        <TaskiemMark />
      </span>
    );
  if (s.kind === "category")
    return (
      <span className="pick-icon soft" aria-hidden="true">
        <Icon name="grid" />
      </span>
    );
  if (s.kind === "customer")
    return (
      <span className="pick-icon round soft" aria-hidden="true">
        {initialsOf(s.name)}
      </span>
    );
  if (
    s.kind === "invoice" ||
    s.kind === "bill" ||
    s.kind === "order" ||
    s.kind === "purchase"
  )
    return (
      <span
        className={`pick-icon ${s.kind === "bill" || s.kind === "purchase" ? "blue" : "soft"}`}
        aria-hidden="true"
      >
        <Icon
          name={
            s.kind === "invoice" ? "invoices" : s.kind === "bill" ? "money" : "orders"
          }
        />
      </span>
    );
  return (
    <span
      className={`pick-icon ${s.kind === "service" ? "blue" : "soft"}`}
      aria-hidden="true"
    >
      <Icon name={s.kind === "service" ? "clock" : "box"} />
    </span>
  );
}

const REF_WHAT: Record<RefKind, string> = {
  invoice: "an invoice or a bill",
  bill: "a bill",
  order: "an order",
  purchase: "an order to a supplier",
  product: "a product",
  customer: "a customer",
};

/** The picker of the designs: matches in groups, the first ready for
 * Enter, springing up from the composer. */
function Picker({
  m,
  query,
  groups,
  kind,
  leaving,
}: {
  m: Mentions;
  query: string;
  groups: SuggestionGroup[];
  /** "/invoice" and the like: what is being picked. */
  kind?: RefKind;
  leaving: boolean;
}) {
  let n = 0;
  const what = kind ? `Tag ${REF_WHAT[kind]}` : "Tag a thing or a business";
  return (
    <section
      ref={leaving ? undefined : m.list}
      className={`tk-picker${leaving ? " leaving" : ""}`}
      aria-hidden={leaving || undefined}
      aria-label={kind ? what : "Tag something"}
      data-testid="mention-list"
    >
      <div className="tk-picker-head">
        <span>
          {query ? (
            <>
              Matches for <b>{query}</b>
            </>
          ) : (
            what
          )}
        </span>
        {query && <span>{what}</span>}
      </div>
      {groups.map((g) => (
        <div
          key={g.label || "taskiem"}
          role="group"
          aria-label={g.label || "Taskiem"}
        >
          {g.label && <div className="tk-picker-label">{g.label}</div>}
          {g.items.map((s) => {
            n++;
            const first = s === m.enterPick;
            return (
              <button
                key={`${s.kind}:${s.id}`}
                type="button"
                className={`tk-pick${first ? " first" : ""}`}
                style={{
                  animationDelay: `calc(20ms + var(--t-stagger) * ${Math.min(n, 6)})`,
                }}
                onClick={(e) =>
                  m.pick(s, e.currentTarget.getBoundingClientRect())
                }
                onKeyDown={(e) => moveIn(m, e)}
              >
                <PickIcon s={s} />
                <span className="pick-words">
                  <span className="pick-name">{s.name}</span>
                  {s.sub && <span className="pick-sub">{s.sub}</span>}
                </span>
                {s.explore && <span className="sr-only">: see their card</span>}
                {s.kind === "category" && (
                  <span className="sr-only">: show them</span>
                )}
                {s.badge && <span className="pick-badge">{s.badge}</span>}
                {first && (
                  <span className="pick-enter" aria-hidden="true">
                    Enter
                  </span>
                )}
              </button>
            );
          })}
        </div>
      ))}
      {/* On a computer, the keys (DeskSeller). */}
      <div className="tk-picker-keys" aria-hidden="true">
        <span>
          <kbd>↑</kbd> <kbd>↓</kbd> to move
        </span>
        <span>
          <kbd>Enter</kbd> to tag
        </span>
        <span>
          <kbd>/</kbd> for tools
        </span>
        <span>
          <kbd>Esc</kbd> to close
        </span>
      </div>
    </section>
  );
}

function PlainList({ m }: { m: Mentions }) {
  const mark = m.q?.mark ?? "@";
  const move = (e: KeyboardEvent<HTMLButtonElement>) => moveIn(m, e);
  return (
    <ul
      ref={m.list as RefObject<HTMLUListElement>}
      className="mention-list"
      aria-label={
        mark === "@"
          ? "Businesses to mention"
          : mark === "/"
            ? "Things to tag"
            : "Items to mention"
      }
      data-testid="mention-list"
    >
      {m.shown.map((s) => (
        <li key={`${s.kind}:${s.id}`}>
          <button type="button" onClick={() => m.pick(s)} onKeyDown={move}>
            <span className="name">
              {mark === "/" ? "#" : mark}
              {s.name}
            </span>
            {s.sub && <span className="hint">{s.sub}</span>}
          </button>
        </li>
      ))}
    </ul>
  );
}

/** A message's words with its mentions marked: a business opens its
 * conversation (ours), an item says its price. */
export function MentionText({
  text,
  mentions,
}: {
  text: string;
  mentions?: Mention[];
}) {
  return (
    <>
      {mentionParts(text, mentions).map((p, i) =>
        !p.mention ? (
          p.text
        ) : p.mention.kind === "business" && p.mention.link ? (
          <Link key={i} className="mention" to={`/chat?with=${p.mention.link}`}>
            {p.text}
          </Link>
        ) : (p.mention.kind === "invoice" ||
            p.mention.kind === "bill" ||
            p.mention.kind === "order" ||
            p.mention.kind === "purchase") &&
          p.mention.price_kobo ? (
          <strong key={i} className="mention">
            {p.text}
            <span className="hint">
              {" "}
              ({formatPrice(p.mention.price_kobo)}
              {p.mention.status ? ` · ${refStatusWords(p.mention.status)}` : ""})
            </span>
          </strong>
        ) : p.mention.kind === "item" && p.mention.price_kobo ? (
          <strong key={i} className="mention">
            {p.text}
            <span className="hint">
              {" "}
              ({formatPrice(p.mention.price_kobo)}
              {p.mention.unit ? ` a ${p.mention.unit}` : ""})
            </span>
          </strong>
        ) : (
          <strong key={i} className="mention">
            {p.text}
          </strong>
        ),
      )}
    </>
  );
}
