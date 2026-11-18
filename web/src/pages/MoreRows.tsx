import { useEffect, useRef } from "react";

/** The end of a list read a page at a time: the next page comes when this
 * scrolls into view, or with the button (keyboards, screen readers).
 *
 * Watched afresh after each page lands (`count`, the rows read so far): an
 * observer tells only of a change, so an end still in view after a short
 * page would otherwise never bring the next. A page that failed adds no
 * rows and so is not asked for again by itself (the button still asks). */
export function MoreRows({
  more,
  busy,
  onMore,
  count,
  label = "Show more",
  testid,
}: {
  more: boolean;
  busy: boolean;
  onMore: () => void;
  /** The rows read so far. */
  count: number;
  label?: string;
  testid?: string;
}) {
  const at = useRef<HTMLDivElement>(null);
  const go = useRef(onMore);
  go.current = onMore;
  const busyNow = useRef(busy);
  busyNow.current = busy;
  useEffect(() => {
    const el = at.current;
    if (!more || !el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((seen) => seen.some((e) => e.isIntersecting) && !busyNow.current && go.current(), { rootMargin: "200px" });
    io.observe(el);
    return () => io.disconnect();
  }, [more, count]);
  if (!more) return null;
  return (
    <div ref={at} className="more-rows">
      <button type="button" className="small" disabled={busy} onClick={onMore} data-testid={testid}>
        {busy ? "Loading…" : label}
      </button>
    </div>
  );
}
