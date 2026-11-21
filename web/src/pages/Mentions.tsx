import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent,
  type RefObject,
} from "react";
import { get } from "../api";
import { Link } from "react-router-dom";
import { flushSync } from "react-dom";
import { Icon, TaskiemMark } from "../icons";
import { usePresence } from "../kit";
import { initialsOf } from "./ChatAvatar";
import {
  groupSuggestions,
  insertMention,
  insertWords,
  mentionParts,
  mentionQuery,
  stillNamed,
  suggest,
  unlinkedSuggestion,
  refStatusWords,
  REF_LABEL,
  CHIPS,
  CHIP_KINDS,
  chipOf,
  type Chip,
  type Mention,
  type MentionQuery,
  type RefKind,
  type Suggestion,
  type SuggestionGroup,
} from "../lib/mentions";
import { formatPrice } from "../lib/storefront";
import { exploreLogo, type ExploreCard, type ExploreResult } from "../lib/explore";
import { BusinessCardSheet } from "./BusinessCardSheet";
import { savedIcon } from "../lib/saved";

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
    /** What "@" offers, said to a screen reader ("Members to mention");
     * linked businesses by default. */
    atLabel?: string;
    services?: Suggestion[];
    /** The categories "@" offers, when not the items' own. */
    categories?: { name: string; count: number }[];
    /** What "/invoice", "/product", "/order" and "/customer" offer. */
    refs?: Partial<Record<RefKind, Suggestion[]>>;
    /** Saved things "@" offers (saved objects S1): a text's words go in the
     * box; any other is written as its @name. wantSaved is called when "@"
     * is typed, so they are read only then. */
    saved?: Suggestion[];
    /** The saved things were all read: Save "mum"… is offered only then. */
    savedComplete?: boolean;
    wantSaved?: () => void;
    /** A saved thing other than a text, picked where the chat shares it as
     * a card: what was typed for it goes, and the chat sends the card. */
    shareSaved?: (s: Suggestion) => void;
    /** A saved file picked: attached to the message as itself (saved
     * objects S1), what was typed for it taken out of the box. */
    attachSaved?: (s: Suggestion) => void;
    /** Save "mum"… when nothing saved has the name typed (saved objects
     * S1, part 4): the name, to save something as. */
    saveNamed?: (name: string) => void;
    /** Saved things other than texts kept as tags, sent with the message
     * (a business's chat with another business; saved objects S1). */
    savedAsTags?: boolean;
    /** Invoices, orders and customers searched on the server, every
     * record (menus-everywhere.md §7): the words, the kinds, where the
     * last page ended; the next page and where it ends ("" for no more). */
    searchRefs?: (words: string, kinds: string, cursor?: string) => Promise<{ list: Suggestion[]; cursor: string }>;
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
  // The picker's chips, Show more and the server's search (menus-
  // everywhere.md §7): another tag begun starts again on its own chip.
  const tagKey = q ? `${q.start}|${q.mark}|${q.kind ?? ""}` : "";
  const [chosen, setChosen] = useState<{ tag: string; chip: Chip; more: number }>();
  const chip: Chip = chosen?.tag === tagKey ? chosen.chip : q?.mark === "@" ? "all" : chipOf(q?.kind);
  const more = chosen?.tag === tagKey ? chosen.more : 0;
  // "/bill" and "/purchase" keep to their own kind until a chip is pressed.
  const narrow = chosen?.tag !== tagKey && (q?.kind === "bill" || q?.kind === "purchase") ? q.kind : undefined;
  const local = (k: Chip): Suggestion[] =>
    k === "product"
      ? q?.mark === "@"
        ? sources["#"]
        : (sources.refs?.product ?? sources["#"])
      : k === "service"
        ? (sources.services ?? [])
        : k === "invoice" || k === "order" || k === "customer"
          ? (sources.refs?.[narrow ?? k] ?? [])
          : k === "business"
            ? sources["@"]
            : k === "saved"
              ? (sources.saved ?? [])
              : [];
  const chips: Chip[] =
    grouped && q && (q.mark === "@" || (q.mark === "/" && q.kind))
      ? (Object.keys(CHIPS) as Chip[]).filter((k) => k === chip || (k === "all" ? q.mark === "@" : local(k).length > 0))
      : [];
  const searchWords = (q?.query ?? "").trim().toLowerCase();
  const servedKey = `${narrow ?? chip}|${searchWords}`;
  const [served, setServed] = useState<{ key: string; list: Suggestion[]; cursor: string }>();
  const [offline, setOffline] = useState(false);
  const kinds = chips.length > 0 ? (narrow ?? CHIP_KINDS[chip]) : undefined;
  useEffect(() => {
    const look = sources.searchRefs;
    if (!look || !kinds || served?.key === servedKey) return;
    let live = true;
    const t = window.setTimeout(() => {
      look(searchWords, kinds).then(
        (r) => {
          if (!live) return;
          setServed({ key: servedKey, list: r.list, cursor: r.cursor });
          setOffline(false);
        },
        () => live && setOffline(true),
      );
    }, 250);
    return () => {
      live = false;
      window.clearTimeout(t);
    };
  }, [servedKey, kinds]);
  const fromServer = served?.key === servedKey ? served : undefined;
  const chipAll =
    chips.length > 0 && chip !== "all" && q
      ? fromServer
        ? fromServer.list
        : suggest(local(chip), q.query, null)
      : [];
  const hasMore = chipAll.length > 6 * (more + 1) || (!!fromServer && fromServer.cursor !== "");
  const showMore = () => {
    setChosen({ tag: tagKey, chip, more: more + 1 });
    const look = sources.searchRefs;
    if (!look || !kinds || !fromServer || !fromServer.cursor || fromServer.list.length >= 6 * (more + 2)) return;
    look(searchWords, kinds, fromServer.cursor).then(
      (r) =>
        setServed((cur) =>
          cur && cur.key === fromServer.key ? { key: cur.key, list: [...cur.list, ...r.list], cursor: r.cursor } : cur,
        ),
      () => setOffline(true),
    );
  };
  const groups: SuggestionGroup[] =
    chips.length > 0 && chip !== "all"
      ? [{ label: CHIPS[chip], items: chipAll.slice(0, 6 * (more + 1)) }]
      :
    q && grouped && q.mark === "@"
      ? groupSuggestions(
          q.query,
          {
            items: sources["#"],
            services: sources.services,
            businesses: sources["@"],
            unlinked,
            categories: sources.categories,
            saved: sources.saved,
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
        : q?.mark === "/"
          ? // "/" and a name: the saved things by that name, if any (§15 1).
            sources.saved
            ? [{ label: "Saved", items: suggest(sources.saved, q.query).slice(0, 4) }].filter((g) => g.items.length > 0)
            : []
          : q
          ? [
              { label: "", items: suggest(sources[q.mark], q.query) },
              ...(q.mark === "@" && sources.saved ? [{ label: "Saved", items: suggest(sources.saved, q.query).slice(0, 4) }] : []),
            ].filter((g) => g.items.length > 0)
          : [];
  // Save "mum"… (saved objects S1, part 4): offered under Saved once the
  // saved things are read and none has the name typed.
  const newName = q?.mark === "@" ? q.query.toLowerCase() : "";
  if (
    sources.saveNamed &&
    sources.saved &&
    sources.savedComplete !== false &&
    /^[a-z0-9][a-z0-9_-]{0,31}$/.test(newName) &&
    !sources.saved.some((x) => x.name.toLowerCase() === newName) &&
    !groups.some((g) => g.items.some((x) => x.name.toLowerCase() === newName))
  ) {
    const row: Suggestion = { kind: "save-new", id: `save-new:${newName}`, name: newName, sub: "Save something by this name" };
    const saved = groups.find((g) => g.label === "Saved");
    if (saved) saved.items = [...saved.items, row];
    else groups.push({ label: "Saved", items: [row] });
  }
  const shown = groups.flatMap((g) => g.items);
  // What Enter takes: the first match. The Taskiem row, always offered,
  // only when "Taskiem" is what is being typed ("@4500" stays as typed).
  // A category is not a tag: Enter tags an item, a tap opens the category.
  const enterPick = shown.find(
    (s) =>
      (s.kind !== "taskiem" && s.kind !== "category" && s.kind !== "save-new") ||
      (q && q.query.length > 0 && "taskiem".startsWith(q.query.toLowerCase())),
  );
  const update = (value: string, caret: number | null) => {
    const next = mentionQuery(value, caret ?? value.length);
    if (next?.mark === "@" || (next?.mark === "/" && !next.kind)) sources.wantSaved?.();
    setQ(next);
  };
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
    const end = Math.max(caret, q.start + 1 + q.query.length);
    if (s.kind === "saved" && s.savedFile && sources.attachSaved) {
      setText(text.slice(0, q.start) + text.slice(end));
      setQ(null);
      sources.attachSaved(s);
      return;
    }
    if (s.kind === "save-new") {
      setText(text.slice(0, q.start) + text.slice(end));
      setQ(null);
      sources.saveNamed?.(s.name);
      return;
    }
    if (s.kind === "saved" && s.body === undefined && sources.shareSaved) {
      setText(text.slice(0, q.start) + text.slice(end));
      setQ(null);
      sources.shareSaved(s);
      return;
    }
    // A saved text's words in place of what was typed, to change before
    // sending; any other saved thing as its @name, which the words carry.
    const next =
      s.kind === "saved" && s.body !== undefined
        ? insertWords(text, q, end, s.body)
        : insertMention(text, q, end, s.name, s.kind);
    // The words and the caret move together, at once: a caret put back a
    // frame later would land the next key typed at the end of the box.
    flushSync(() => {
      setText(next.text);
      if (s.kind !== "taskiem" && (s.kind !== "saved" || (sources.savedAsTags && s.body === undefined)))
        setPicked((cur) => [...cur, s]);
      setQ(null);
    });
    el?.focus();
    el?.setSelectionRange(next.caret, next.caret);
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
        p.kind === "saved"
          ? { kind: p.kind, saved: { id: p.id, owner: p.savedOwner ?? "business", confirm: false } }
          : p.kind === "business"
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
  /** The members picked with "@" (their id with the name picked): a
   * group's mentions keep the one picked when names collide. */
  const pickedPeople = () => picked.filter((p) => p.kind === "customer").map((p) => ({ id: p.id, name: p.name }));
  const reset = () => {
    setPicked([]);
    setQ(null);
  };
  /** The search box typed in: its words in the text box, after the mark
   * (and "/invoice "), so both say the same. */
  const searchFor = (words: string) => {
    if (!q) return;
    const head = q.mark === "/" ? (/^[a-z]+[/ ]/i.exec(text.slice(q.start + 1))?.[0].length ?? 0) : 0;
    const from = q.start + 1 + head;
    const end = Math.min(from + q.query.length, text.length);
    const next = text.slice(0, from) + words + text.slice(end);
    setText(next);
    update(next, from + words.length);
    const el = box.current;
    requestAnimationFrame(() => el?.setSelectionRange(from + words.length, from + words.length));
  };
  return {
    /** What was picked, kept to give back to a message not sent. */
    picked: () => picked,
    /** Picks given back, with the words they were picked in. */
    restore: (ps: Suggestion[]) => setPicked(ps),
    // Only saved things to offer (a chat with no one else to name).
    atLabel: sources.atLabel ?? (sources["@"].length === 0 && sources.saved ? "Saved things" : undefined),
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
    pickedPeople,
    reset,
    close: () => setQ(null),
    box,
    chips,
    chip,
    pickChip: (k: Chip) => k !== chip && setChosen({ tag: tagKey, chip: k, more: 0 }),
    hasMore,
    showMore,
    searchFor,
    offline: offline && !!kinds,
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
    (m.shown.length > 0 || m.chips.length > 1);
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
  if (s.kind === "saved" || s.kind === "save-new")
    return (
      <span className="pick-icon soft" aria-hidden="true">
        <Icon name={savedIcon(s.savedKind)} />
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
      {/* The search box and the kinds (menus-everywhere.md §7). */}
      <div className="tk-picker-search">
        <Icon name="search" />
        <input
          type="search"
          aria-label="Search what to tag"
          placeholder={
            m.chip === "all"
              ? "Search things and businesses"
              : m.chip === "invoice"
                ? "Search invoices and bills"
                : `Search ${CHIPS[m.chip].toLowerCase()}`
          }
          value={query}
          onChange={(e) => m.searchFor(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && m.enterPick) {
              e.preventDefault();
              m.pick(m.enterPick);
            } else if (e.key === "ArrowDown") {
              e.preventDefault();
              m.list.current?.querySelector<HTMLButtonElement>(".tk-pick")?.focus();
            } else if (e.key === "Escape") {
              e.preventDefault();
              m.close();
              m.box.current?.focus();
            }
          }}
        />
      </div>
      {m.chips.length > 1 && (
        <div className="tk-chips tk-picker-chips" role="group" aria-label="Kinds">
          {m.chips.map((k) => (
            <button key={k} type="button" aria-pressed={k === m.chip} onClick={() => m.pickChip(k)}>
              {CHIPS[k]}
            </button>
          ))}
        </div>
      )}
      {m.offline && <p className="tk-picker-note">Searching what is on this phone</p>}
      {groups.every((g) => g.items.length === 0) && (
        <p className="tk-picker-note">{query ? `Nothing found for "${query}"` : "Nothing here yet"}</p>
      )}
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
                  <span className="pick-name">{s.kind === "save-new" ? `Save "@${s.name}"…` : s.name}</span>
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
      {!leaving && m.hasMore && (
        <div className="tk-picker-more">
          <button type="button" className="secondary" onClick={m.showMore}>
            Show more
          </button>
        </div>
      )}
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
          ? (m.atLabel ?? "Businesses to mention")
          : mark === "/"
            ? m.q?.kind ? "Things to tag" : "Saved things"
            : "Items to mention"
      }
      data-testid="mention-list"
    >
      {m.shown.map((s) => (
        <li key={`${s.kind}:${s.id}`}>
          <button type="button" onClick={() => m.pick(s)} onKeyDown={move}>
            <span className="name">
              {s.kind === "save-new" ? (
                `Save "@${s.name}"…`
              ) : (
                <>
                  {mark === "/" ? (s.kind === "saved" ? "@" : "#") : mark}
                  {s.name}
                </>
              )}
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
