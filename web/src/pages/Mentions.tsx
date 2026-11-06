import { useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../icons";
import { initialsOf } from "./ChatAvatar";
import {
  groupSuggestions,
  insertMention,
  mentionParts,
  mentionQuery,
  stillNamed,
  suggest,
  type Mention,
  type MentionQuery,
  type Suggestion,
  type SuggestionGroup,
} from "../lib/mentions";
import { formatPrice } from "../lib/storefront";

// Mentions in the composer (business links L4, §5.5): typing "@" offers
// linked businesses, "#" items with their unit and price. Enter or Tab
// takes the first, the arrows move through them, Escape closes them. The
// text box stays a plain text box; the suggestions are buttons.

export type Mentions = ReturnType<typeof useMentions>;

export function useMentions(
  text: string,
  setText: (v: string) => void,
  box: RefObject<HTMLTextAreaElement | null>,
  sources: { "@": Suggestion[]; "#": Suggestion[]; services?: Suggestion[] },
  /** Chat tools (chat-tools.md §3): "@" tags things and businesses, in
   * groups, with the Taskiem row when it may be called. */
  grouped?: { taskiem: boolean },
) {
  const [q, setQ] = useState<MentionQuery | null>(null);
  const [picked, setPicked] = useState<Suggestion[]>([]);
  const list = useRef<HTMLElement>(null);
  const groups: SuggestionGroup[] =
    q && grouped && q.mark === "@"
      ? groupSuggestions(
          q.query,
          { items: sources["#"], services: sources.services, businesses: sources["@"] },
          grouped.taskiem,
        )
      : q
        ? [{ label: "", items: suggest(sources[q.mark], q.query) }]
        : [];
  const shown = groups.flatMap((g) => g.items);
  const update = (value: string, caret: number | null) =>
    setQ(mentionQuery(value, caret ?? value.length));
  const pick = (s: Suggestion) => {
    if (!q) return;
    const el = box.current;
    const caret = el?.selectionStart ?? text.length;
    const next = insertMention(
      text,
      q,
      Math.max(caret, q.start + 1 + q.query.length),
      s.name,
    );
    setText(next.text);
    if (s.kind === "business" || s.kind === "item")
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
    const first = shown[0];
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
    stillNamed(text, picked).map((p) =>
      p.kind === "business"
        ? { kind: p.kind, link: p.id }
        : { kind: p.kind, item_id: p.id },
    );
  const reset = () => {
    setPicked([]);
    setQ(null);
  };
  return {
    q,
    shown,
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
  };
}

/** The suggestions over the text box. */
export function MentionList({ m }: { m: Mentions }) {
  if (!m.q || m.shown.length === 0) return null;
  const mark = m.q.mark;
  if (m.grouped && mark === "@") return <Picker m={m} />;
  return <PlainList m={m} />;
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
  if (s.kind === "business")
    return <span className="pick-icon round lime" aria-hidden="true">{initialsOf(s.name)}</span>;
  if (s.kind === "taskiem")
    return (
      <span className="pick-icon round pine" aria-hidden="true">
        <Icon name="sparkle" />
      </span>
    );
  return (
    <span className={`pick-icon ${s.kind === "service" ? "blue" : "soft"}`} aria-hidden="true">
      <Icon name={s.kind === "service" ? "clock" : "box"} />
    </span>
  );
}

/** The picker of the designs: matches in groups, the first ready for
 * Enter, springing up from the composer. */
function Picker({ m }: { m: Mentions }) {
  const query = m.q?.query ?? "";
  let n = 0;
  return (
    <section
      ref={m.list}
      className="tk-picker"
      aria-label="Tag something"
      data-testid="mention-list"
    >
      <div className="tk-picker-head">
        <span>
          {query ? (
            <>
              Matches for <b>{query}</b>
            </>
          ) : (
            "Tag a thing or a business"
          )}
        </span>
        {query && <span>Tag a thing or a business</span>}
      </div>
      {m.groups.map((g) => (
        <div key={g.label || "taskiem"} role="group" aria-label={g.label || "Taskiem"}>
          {g.label && <div className="tk-picker-label">{g.label}</div>}
          {g.items.map((s) => {
            const first = n++ === 0;
            return (
              <button
                key={`${s.kind}:${s.id}`}
                type="button"
                className={`tk-pick${first ? " first" : ""}`}
                style={{ animationDelay: `calc(var(--t-stagger) * ${Math.min(n, 6)})` }}
                onClick={() => m.pick(s)}
                onKeyDown={(e) => moveIn(m, e)}
              >
                <PickIcon s={s} />
                <span className="pick-words">
                  <span className="pick-name">{s.name}</span>
                  {s.sub && <span className="pick-sub">{s.sub}</span>}
                </span>
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
      aria-label={mark === "@" ? "Businesses to mention" : "Items to mention"}
      data-testid="mention-list"
    >
      {m.shown.map((s) => (
        <li key={`${s.kind}:${s.id}`}>
          <button type="button" onClick={() => m.pick(s)} onKeyDown={move}>
            <span className="name">
              {mark}
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
