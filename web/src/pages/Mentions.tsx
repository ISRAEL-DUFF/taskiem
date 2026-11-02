import { useRef, useState, type KeyboardEvent, type RefObject } from "react";
import { Link } from "react-router-dom";
import {
  insertMention,
  mentionParts,
  mentionQuery,
  stillNamed,
  suggest,
  type Mention,
  type MentionQuery,
  type Suggestion,
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
  sources: { "@": Suggestion[]; "#": Suggestion[] },
) {
  const [q, setQ] = useState<MentionQuery | null>(null);
  const [picked, setPicked] = useState<Suggestion[]>([]);
  const list = useRef<HTMLUListElement>(null);
  const shown = q ? suggest(sources[q.mark], q.query) : [];
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

/** The suggestions under the text box. */
export function MentionList({ m }: { m: Mentions }) {
  if (!m.q || m.shown.length === 0) return null;
  const mark = m.q.mark;
  const move = (e: KeyboardEvent<HTMLButtonElement>) => {
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
  };
  return (
    <ul
      ref={m.list}
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
