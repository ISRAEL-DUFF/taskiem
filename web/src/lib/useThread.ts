import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { atBottom, atNewest, followNew, newestTop, scrollWithin } from "./thread";

/** How far down the thread's content the newest message ends. */
function newestEnd(el: HTMLElement): number | undefined {
  const all = el.querySelectorAll("[data-msg]");
  const last = all[all.length - 1];
  if (!last) return undefined;
  return (
    last.getBoundingClientRect().bottom -
    el.getBoundingClientRect().top +
    el.scrollTop
  );
}

/** Scrolls to the newest message: the end of the thread, or, with cards after the messages, the newest message at the view's end. */
function toNewest(el: HTMLElement) {
  const end = newestEnd(el);
  const top =
    end === undefined
      ? undefined
      : newestTop({
          scrollHeight: el.scrollHeight,
          clientHeight: el.clientHeight,
          newestEnd: end,
        });
  el.scrollTop = top ?? el.scrollHeight;
}

const atNewestOf = (el: HTMLElement) =>
  atNewest({
    scrollTop: el.scrollTop,
    scrollHeight: el.scrollHeight,
    clientHeight: el.clientHeight,
    newestEnd: newestEnd(el),
  });

/**
 * How a thread scrolls, as WhatsApp Web's, for any list of messages (a
 * conversation with a business, or with Taskiem): older ones load as the
 * reader nears the top and the view holds; new ones are followed only while
 * the reader is at the bottom (else a count for a button); the first showing
 * goes to the message jumped to, the first unread (`[data-unread]`) or the
 * end; a message found in the thread is scrolled to and flashed.
 *
 * Put `stream` on the scrolling element, `topMark` on a marker at its top
 * (shown while there are older messages), `onScroll` on the element, and
 * `data-msg={id}` on each message.
 */
export function useThread<T extends { id: string; created_at: string }>(o: {
  items: T[] | undefined;
  isMine: (m: T) => boolean;
  hasOlder: boolean;
  loadOlder: () => void;
}) {
  const { items, isMine, hasOlder, loadOlder } = o;
  const stream = useRef<HTMLDivElement>(null);
  const topMark = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  // Kept at the bottom as what is below grows: opened at the end, or
  // scrolled there by the reader (not opened at the unread or a message).
  const hold = useRef(false);
  // Where the reader was at the last scroll: at the very end.
  const atEnd = useRef(false);
  const placed = useRef(false);
  const anchor = useRef<{ h: number; top: number } | null>(null);
  const prevNewest = useRef<string>(undefined);
  const jumpTo = useRef<string>(undefined);
  const [away, setAway] = useState(false);
  const [newCount, setNewCount] = useState(0);
  const [flashId, setFlashId] = useState<string>();

  /** The messages are about to be replaced (another conversation, or a jump): place the view afresh, at `id` if given. */
  const reset = useCallback((id?: string) => {
    placed.current = false;
    prevNewest.current = undefined;
    jumpTo.current = id;
    setNewCount(0);
  }, []);
  /** Older messages are about to be added above: keep the reader's place. */
  const keepPlace = useCallback(() => {
    const el = stream.current;
    if (el) anchor.current = { h: el.scrollHeight, top: el.scrollTop };
  }, []);
  /** Scrolls to a message already in the thread and flashes it; false if it is not there. */
  const show = useCallback((id: string): boolean => {
    const el = stream.current;
    const target = el?.querySelector(`[data-msg="${id}"]`);
    if (!el || !target) return false;
    scrollWithin(el, target, "center");
    setFlashId(id);
    return true;
  }, []);
  const toBottom = useCallback(() => {
    const el = stream.current;
    if (el) el.scrollTo({ top: el.scrollHeight });
    setNewCount(0);
  }, []);

  useLayoutEffect(() => {
    const el = stream.current;
    if (!el || !items) return;
    if (anchor.current) {
      el.scrollTop = anchor.current.top + (el.scrollHeight - anchor.current.h);
      anchor.current = null;
      return;
    }
    const newest = items[items.length - 1]?.created_at;
    if (!placed.current) {
      placed.current = true;
      const id = jumpTo.current;
      jumpTo.current = undefined;
      const target = id
        ? el.querySelector(`[data-msg="${id}"]`)
        : el.querySelector("[data-unread]");
      if (target) {
        scrollWithin(el, target, id ? "center" : "start");
        if (id) setFlashId(id);
      } else el.scrollTop = el.scrollHeight;
      stick.current = atNewestOf(el);
      atEnd.current = atBottom(el);
      hold.current = !target;
      setAway(!stick.current);
      prevNewest.current = newest;
      return;
    }
    const f = followNew(prevNewest.current, items, stick.current, isMine);
    if (f.kind === "bottom") {
      // The newest message in view, not what follows it (the Taskiem
      // conversation's alert cards): an answer is never left out of sight.
      toNewest(el);
      // Not held at the end when that leaves the cards below the answer.
      atEnd.current = atBottom(el);
      if (!atEnd.current) hold.current = false;
      setNewCount(0);
    } else if (f.kind === "pill") setNewCount((n) => n + f.count);
    prevNewest.current = newest;
    // isMine is a function of the item alone: the items are what change.
  }, [items]);

  useEffect(() => {
    if (!flashId) return;
    const t = setTimeout(() => setFlashId(undefined), 1800);
    return () => clearTimeout(t);
  }, [flashId]);

  const hasItems = (items?.length ?? 0) > 0;
  // Watched afresh once older ones are added: the observer tells only of a
  // change, so a reader still at the top as a page lands (the mark never out
  // of reach) would otherwise not be given the next.
  const oldest = items?.[0]?.id;
  useEffect(() => {
    const mark = topMark.current;
    if (
      !mark ||
      !hasOlder ||
      !hasItems ||
      typeof IntersectionObserver === "undefined"
    )
      return;
    const io = new IntersectionObserver(
      ([e]) => e?.isIntersecting && loadOlder(),
      { root: stream.current, rootMargin: "200px 0px 0px 0px" },
    );
    io.observe(mark);
    return () => io.disconnect();
    // The oldest is what changes when a page is added above.
  }, [hasOlder, hasItems, loadOlder, oldest]);

  // A reader at the bottom stays there as what is below grows after the
  // thread was placed: Taskiem's cards arriving after its messages, a photo
  // that loads. (Without this, where the thread opened depended on which
  // came first.)
  useEffect(() => {
    const el = stream.current;
    if (!el || typeof ResizeObserver === "undefined") return;
    const keep = () => {
      if (placed.current && hold.current && atEnd.current && !anchor.current)
        el.scrollTop = el.scrollHeight;
    };
    const sizes = new ResizeObserver(keep);
    const watch = () => {
      sizes.disconnect();
      for (const c of Array.from(el.children)) sizes.observe(c);
    };
    watch();
    const kids =
      typeof MutationObserver === "undefined"
        ? undefined
        : new MutationObserver(() => (watch(), keep()));
    kids?.observe(el, { childList: true });
    return () => {
      sizes.disconnect();
      kids?.disconnect();
    };
  }, []);

  const onScroll = () => {
    const el = stream.current;
    if (!el) return;
    const near = atNewestOf(el);
    // Held at the very end only from there: arriving at it (the reader, or
    // the thread opening there), not with the newest message merely in view
    // (toNewest leaves Taskiem's cards below it, and that place stands).
    const end = atBottom(el);
    if (!end) hold.current = false;
    else if (!atEnd.current) hold.current = true;
    atEnd.current = end;
    stick.current = near;
    setAway(!near);
    if (near) setNewCount(0);
  };

  return {
    stream,
    topMark,
    onScroll,
    away,
    newCount,
    flashId,
    reset,
    keepPlace,
    show,
    toBottom,
  };
}
