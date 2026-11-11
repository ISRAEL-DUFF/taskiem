import {
  lazy,
  Suspense,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Icon } from "../icons";

// The full emoji list is loaded when the "+" is first pressed.
const EmojiPanel = lazy(() =>
  import("./EmojiPanel").then((m) => ({ default: m.EmojiPanel })),
);

/** A swipe this far right replies. */
const SWIPE_REPLY_PX = 60;
/** A finger held this long on a bubble opens its menu, as WhatsApp's. */
const HOLD_MS = 500;

/** True where some of the bubble's own text is selected (a right click then
 * keeps the browser's menu, to copy it); a selection elsewhere does not. */
function selectedIn(el: Element | null): boolean {
  const sel = window.getSelection();
  if (!el || !sel || sel.isCollapsed || sel.toString() === "") return false;
  return !!sel.anchorNode && el.contains(sel.anchorNode);
}

/**
 * The bubble of every conversation (a business's and Taskiem's): the shape,
 * the tail on the first of a run, our side and theirs, and what is done to
 * it: a finger held on a touch screen, a swipe right to reply, a right click
 * or the chevron on hover for the menu, Esc or a click elsewhere to close it
 * (Esc giving the focus back to the chevron). What is in it, and what its
 * menu offers, is given: `children` is the content up to the menu, `after`
 * what follows the chevron (the time and ticks, reactions).
 */
export function BubbleShell({
  id,
  mine,
  first,
  testid,
  className = "",
  what,
  menu,
  onReply,
  quickReact = false,
  children,
  after,
}: {
  id: string;
  mine: boolean;
  first: boolean;
  testid: string;
  /** More classes (a card, a flash, reactions). */
  className?: string;
  /** What the bubble holds, in a line, for the menu's name. */
  what: string;
  /** The menu: the reactions (a pill above the bubble) and the actions (a card by it), given the way to close it; none: no menu. */
  menu?: (
    close: () => void,
    more: { pick: () => void },
  ) => {
    reactions?: ReactNode;
    actions: ReactNode;
    /** An emoji chosen from the full picker (the "+"). */
    onPick?: (emoji: string) => void;
  };
  /** Replying to it (the swipe, and the menu's entry if it has one). */
  onReply?: () => void;
  /** A smiley beside the bubble on hover, for a quick reaction. */
  quickReact?: boolean;
  children: ReactNode;
  after?: ReactNode;
}) {
  // The menu: closed, the reactions alone (the smiley on hover), or whole.
  const [more, setMoreState] = useState<false | "react" | "full" | "pick">(
    false,
  );
  const setMore = (v: boolean | "react" | "full" | "pick") =>
    setMoreState(v === true ? "full" : v);
  const bubbleEl = useRef<HTMLDivElement>(null);
  const chevron = useRef<HTMLButtonElement>(null);
  const hasMenu = !!menu;
  const canReply = !!onReply;
  // Swipe right to reply, and a finger held to open the menu, on a touch
  // screen, as WhatsApp's; the hold is let go by a move or a scroll.
  const drag = useRef<{ x: number; y: number; id: number } | null>(null);
  const hold = useRef<number>(undefined);
  const held = useRef(false);
  const [dx, setDx] = useState(0);
  const endHold = () => {
    window.clearTimeout(hold.current);
    hold.current = undefined;
  };
  const touch = {
    onPointerDown: (e: React.PointerEvent) => {
      if (e.pointerType === "mouse") return;
      if (canReply)
        drag.current = { x: e.clientX, y: e.clientY, id: e.pointerId };
      if (!hasMenu) return;
      endHold();
      hold.current = window.setTimeout(() => {
        hold.current = undefined;
        held.current = true;
        drag.current = null;
        setDx(0);
        setMore(true);
      }, HOLD_MS);
    },
    onPointerMove: (e: React.PointerEvent) => {
      const d = drag.current;
      if (d && e.pointerId === d.id) {
        const x = e.clientX - d.x;
        if (Math.abs(x) > 10 || Math.abs(e.clientY - d.y) > 10) endHold();
        if (Math.abs(e.clientY - d.y) > 30 && x < 20) {
          drag.current = null;
          setDx(0);
          return;
        }
        setDx(Math.max(0, Math.min(x, 80)));
      } else if (e.pointerType !== "mouse") endHold();
    },
    onPointerUp: () => {
      endHold();
      if (drag.current && dx >= SWIPE_REPLY_PX) onReply?.();
      drag.current = null;
      setDx(0);
    },
    onPointerCancel: () => {
      endHold();
      drag.current = null;
      setDx(0);
    },
    // A long press on a touch screen also asks for the browser's own menu.
    onContextMenu: (e: React.MouseEvent) => {
      if (held.current) {
        e.preventDefault();
        held.current = false;
        return;
      }
      // A right click opens the menu the hover's chevron opens, as on
      // WhatsApp Web. A link, a player or a selection keeps the browser's own
      // menu (copy, open in a new tab), and so does Shift + right click.
      const t = e.target as Element;
      if (
        !hasMenu ||
        e.shiftKey ||
        t.closest?.("a, audio, video, input, textarea") ||
        selectedIn(bubbleEl.current)
      )
        return;
      e.preventDefault();
      setMore(true);
      requestAnimationFrame(() =>
        bubbleEl.current
          ?.querySelector<HTMLElement>(
            '[data-testid="chat-more"] .pop-card button',
          )
          ?.focus(),
      );
    },
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === "Escape" && more) {
        e.preventDefault();
        setMore(false);
        chevron.current?.focus();
      }
    },
  };
  // An open menu closes on a click elsewhere, and on Esc from anywhere.
  useEffect(() => {
    if (!more) return;
    const away = (e: MouseEvent) =>
      !bubbleEl.current?.contains(e.target as Node) && setMore(false);
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      e.preventDefault();
      setMore(false);
    };
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [more]);
  // Where the menu floats: the reactions above the bubble, the actions
  // below it, on the bubble's own side, kept on the screen. A scroll or a
  // resize closes it (the bubble moves from under it).
  const pill = useRef<HTMLDivElement>(null);
  const card = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{
    pill?: { top: number; left: number };
    card?: { top: number; left: number };
  }>({});
  useLayoutEffect(() => {
    if (!more || !bubbleEl.current) return;
    const bubble = bubbleEl.current;
    const placeAll = () => {
      const r = bubble.getBoundingClientRect();
      const vw = window.innerWidth;
      const vh = window.innerHeight;
      const gap = 6;
      const edge = 8;
      const place = (w: number) =>
        Math.max(edge, Math.min(mine ? r.right - w : r.left, vw - w - edge));
      const next: typeof pos = {};
      let below = r.bottom + gap;
      if (pill.current) {
        const w = pill.current.offsetWidth;
        const h = pill.current.offsetHeight;
        let top = r.top - h - gap;
        if (top < edge) {
          top = r.bottom + gap; // no room above: under the bubble
          below = top + h + gap;
        }
        next.pill = { top, left: place(w) };
      }
      if (card.current) {
        const w = card.current.offsetWidth;
        const h = card.current.offsetHeight;
        let top = below;
        if (top + h > vh - edge) top = Math.max(edge, vh - h - edge);
        next.card = { top, left: place(w) };
      }
      setPos(next);
    };
    placeAll();
    // Placed again as what floats changes size: the emoji panel loads after
    // its placeholder is placed, and would otherwise grow over the bubble.
    if (typeof ResizeObserver === "undefined") return;
    const sizes = new ResizeObserver(placeAll);
    if (pill.current) sizes.observe(pill.current);
    if (card.current) sizes.observe(card.current);
    return () => sizes.disconnect();
  }, [more, mine]);
  useEffect(() => {
    if (!more) return;
    const shut = () => setMore(false);
    window.addEventListener("resize", shut);
    document.addEventListener("scroll", shut, true);
    return () => {
      window.removeEventListener("resize", shut);
      document.removeEventListener("scroll", shut, true);
    };
  }, [more]);
  const parts =
    more && menu
      ? menu(() => setMore(false), { pick: () => setMore("pick") })
      : undefined;
  const arrows = (e: React.KeyboardEvent) => {
    const keys = ["ArrowDown", "ArrowUp", "Home", "End"];
    if (!keys.includes(e.key)) return;
    const all = Array.from(
      e.currentTarget.querySelectorAll<HTMLElement>("button:not(:disabled)"),
    );
    if (all.length === 0) return;
    e.preventDefault();
    const at = all.indexOf(document.activeElement as HTMLElement);
    const to =
      e.key === "Home"
        ? 0
        : e.key === "End"
          ? all.length - 1
          : e.key === "ArrowDown"
            ? (at + 1) % all.length
            : (at - 1 + all.length) % all.length;
    all[to]?.focus();
  };
  return (
    <div
      ref={bubbleEl}
      className={`bubble${mine ? " mine" : ""}${first ? " first" : ""}${className ? ` ${className}` : ""}`}
      data-msg={id}
      data-testid={testid}
      data-mine={mine}
      style={dx ? { transform: `translateX(${dx}px)` } : undefined}
      {...touch}
    >
      {children}
      {parts && (
        <div
          className="bubble-pop"
          data-testid="chat-more"
          role="group"
          aria-label={`Options for ${what}`}
          onKeyDown={arrows}
        >
          {parts.reactions && more !== "pick" && (
            <div className="pop-pill" ref={pill} style={pos.pill}>
              {parts.reactions}
            </div>
          )}
          {more === "pick" && (
            <div className="pop-picker" ref={pill} style={pos.pill}>
              <Suspense
                fallback={<div className="emoji-panel" aria-busy="true" />}
              >
                <EmojiPanel
                  onPick={(c) => parts.onPick?.(c)}
                  onClose={() => {
                    setMore(false);
                    chevron.current?.focus();
                  }}
                />
              </Suspense>
            </div>
          )}
          {more === "full" && (
            <div className="pop-card" ref={card} style={pos.card}>
              {parts.actions}
            </div>
          )}
        </div>
      )}
      {(canReply || hasMenu) && (
        <button
          type="button"
          className="bubble-chevron"
          ref={chevron}
          aria-expanded={more === "full"}
          onClick={() => setMore(more === "full" ? false : "full")}
        >
          <Icon name="down" />
          <span className="sr-only">More for {what}</span>
        </button>
      )}
      {hasMenu && quickReact && (
        <button
          type="button"
          className="bubble-react"
          tabIndex={-1}
          aria-hidden="true"
          onClick={() => setMore(more === "react" ? false : "react")}
        >
          <Icon name="emoji" />
        </button>
      )}
      {after}
    </div>
  );
}

/** A row of the action card: its icon and its words. */
export function PopItem({
  icon,
  danger = false,
  onClick,
  children,
}: {
  icon: string;
  danger?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <button
      type="button"
      className={`pop-item${danger ? " danger" : ""}`}
      onClick={onClick}
    >
      <Icon name={icon} />
      <span>{children}</span>
    </button>
  );
}

/** The menu's Copy: the message's words to the clipboard; it says so. */
export function CopyItem({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <PopItem
      icon="copy"
      onClick={() =>
        void navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => undefined,
        )
      }
    >
      {copied ? "Copied" : "Copy"}
    </PopItem>
  );
}

/** The ticks under a message of ours: two when it is delivered, blue once
 * they have read it. */
export function Ticks({ read }: { read: boolean }) {
  return (
    <span
      className={`ticks${read ? " read" : ""}`}
      role="img"
      aria-label={read ? "Read" : "Delivered"}
    >
      <Icon name="tick-double" />
    </span>
  );
}
