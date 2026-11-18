import { useEffect, useRef } from "react";

/** The end of a list read a page at a time: the next page comes when this
 * scrolls into view, or with the button (keyboards, screen readers). */
export function MoreRows({ more, busy, onMore, label = "Show more", testid }: { more: boolean; busy: boolean; onMore: () => void; label?: string; testid?: string }) {
  const at = useRef<HTMLDivElement>(null);
  const go = useRef(onMore);
  go.current = onMore;
  useEffect(() => {
    const el = at.current;
    if (!more || !el || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((seen) => seen.some((e) => e.isIntersecting) && go.current(), { rootMargin: "200px" });
    io.observe(el);
    return () => io.disconnect();
  }, [more]);
  if (!more) return null;
  return (
    <div ref={at} className="more-rows">
      <button type="button" className="small" disabled={busy} onClick={onMore} data-testid={testid}>
        {busy ? "Loading…" : label}
      </button>
    </div>
  );
}
