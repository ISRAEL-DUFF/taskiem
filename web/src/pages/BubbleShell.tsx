import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "../icons";

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
  asResult = false,
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
  /** The menu's entries, given the way to close it; none: no menu. */
  menu?: (close: () => void) => ReactNode;
  /** Replying to it (the swipe, and the menu's entry if it has one). */
  onReply?: () => void;
  /** A search result: it has no menu, the conversation's does the work. */
  asResult?: boolean;
  children: ReactNode;
  after?: ReactNode;
}) {
  const [more, setMore] = useState(false);
  const bubbleEl = useRef<HTMLDivElement>(null);
  const chevron = useRef<HTMLButtonElement>(null);
  const hasMenu = !!menu && !asResult;
  const canReply = !!onReply && !asResult;
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
          ?.querySelector<HTMLElement>('[data-testid="chat-more"] button')
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
      {more && menu && (
        <span
          className="row-actions bubble-more"
          data-testid="chat-more"
          role="group"
          aria-label={`Options for ${what}`}
        >
          {menu(() => setMore(false))}
        </span>
      )}
      {(canReply || hasMenu) && !asResult && (
        <button
          type="button"
          className="bubble-chevron"
          ref={chevron}
          aria-expanded={more}
          onClick={() => setMore(!more)}
        >
          <Icon name="down" />
          <span className="sr-only">More for {what}</span>
        </button>
      )}
      {after}
    </div>
  );
}

/** The menu's Copy: the message's words to the clipboard; it says so. */
export function CopyItem({ text }: { text: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <button
      type="button"
      className="small"
      onClick={() =>
        void navigator.clipboard?.writeText(text).then(
          () => setCopied(true),
          () => undefined,
        )
      }
    >
      {copied ? "Copied" : "Copy"}
    </button>
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
