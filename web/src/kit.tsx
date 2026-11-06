import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { Icon } from "./icons";
import { useSheetDrag } from "./lib/sheetDrag";
import { useFocusTrap } from "./ui";

// Taskiem's new look as shared parts (docs/spec/chat-tools.md §2,
// docs/brand/screens.md "The new kit"): built in phase 1 and used first by
// the chat; the rest of the app moves onto them page by page. Styles in
// kit.css, timings in motion.css.

/** Whether a media query holds, following changes. */
export function useMediaQuery(q: string): boolean {
  const get = () =>
    typeof window !== "undefined" && !!window.matchMedia?.(q).matches;
  const [on, setOn] = useState(get);
  useEffect(() => {
    const mq = window.matchMedia?.(q);
    if (!mq) return;
    const f = () => setOn(mq.matches);
    f();
    mq.addEventListener?.("change", f);
    return () => mq.removeEventListener?.("change", f);
  }, [q]);
  return on;
}

/** Whether the person asked for less motion. */
export const reducedMotion = () =>
  typeof window !== "undefined" &&
  !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;

/** A part that leaves with a little motion: mounted while shown and for
 * `ms` after, `leaving` meanwhile (nothing waits with motion turned down). */
export function usePresence(
  show: boolean,
  ms = 180,
): { mounted: boolean; leaving: boolean } {
  const [mounted, setMounted] = useState(show);
  const [leaving, setLeaving] = useState(false);
  useEffect(() => {
    if (show) {
      setMounted(true);
      setLeaving(false);
      return;
    }
    if (!mounted) return;
    if (reducedMotion()) {
      setMounted(false);
      return;
    }
    setLeaving(true);
    const t = window.setTimeout(() => {
      setMounted(false);
      setLeaving(false);
    }, ms);
    return () => window.clearTimeout(t);
  }, [show]);
  return { mounted, leaving };
}

/** A small word in a round pill: "4 left", "Accepted". */
export function Pill({
  tone = "plain",
  children,
}: {
  tone?: "lime" | "warn" | "danger" | "plain";
  children: ReactNode;
}) {
  return <span className={`tk-pill ${tone}`}>{children}</span>;
}

/** Nothing to show yet, said kindly, with what to do. */
export function KitEmpty({
  icon = "info",
  title,
  children,
  action,
}: {
  icon?: string;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="tk-empty">
      <span className="tk-empty-icon" aria-hidden="true">
        <Icon name={icon} />
      </span>
      <p className="tk-empty-title">{title}</p>
      {children && <p className="tk-empty-line">{children}</p>}
      {action}
    </div>
  );
}

/** Rows shimmering while a list loads. */
export function KitLoading({
  rows = 3,
  label = "Loading…",
}: {
  rows?: number;
  label?: string;
}) {
  return (
    <div className="tk-loading" aria-busy="true">
      <span className="sr-only">{label}</span>
      {Array.from({ length: rows }, (_, i) => (
        <span key={i} className="tk-loading-row" aria-hidden="true">
          <span className="tk-shimmer tk-loading-tile" />
          <span className="tk-loading-words">
            <span className="tk-shimmer" />
            <span className="tk-shimmer" />
          </span>
        </span>
      ))}
    </div>
  );
}

/** A field of the new look: the label over a tall, rounded box. */
export function KitField({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: ReactNode;
  children: ReactNode;
}) {
  return (
    <label className="tk-field">
      <span className="tk-field-label">{label}</span>
      {children}
      {hint && <span className="tk-field-hint">{hint}</span>}
    </label>
  );
}

/** A diary line: a change told in the chat as a line, not a card. */
export function DiaryLine({
  to,
  children,
  when,
  fresh,
  more,
  msg,
}: {
  to?: string;
  children: ReactNode;
  /** "You, 10:42". */
  when: ReactNode;
  /** Just made: it drops in lit in lime. */
  fresh?: boolean;
  /** "and 2 more changes", with its fold. */
  more?: ReactNode;
  /** The message it is (search results and quotes jump to it). */
  msg?: string;
}) {
  const body = (
    <>
      <Icon name="tick" />
      <span>{children}</span>
    </>
  );
  return (
    <div className="tk-diary" data-testid="chat-diary" data-msg={msg}>
      {to ? (
        <Link to={to} className={`tk-diary-pill${fresh ? " tk-land" : ""}`}>
          {body}
        </Link>
      ) : (
        <span className={`tk-diary-pill${fresh ? " tk-land" : ""}`}>
          {body}
        </span>
      )}
      <span className="tk-diary-when">
        {when}
        {more}
      </span>
    </div>
  );
}

/**
 * The sheet a tool opens in, one frame on every screen: up from the bottom
 * on a phone (pulled down to close), a drawer on a tablet, a panel beside
 * the content on a computer, where the page stays usable. From a tile it
 * grows out of the tile (the same shape expands), its content fading in
 * once it has room; it leaves the way it came.
 */
export function Sheet({
  title,
  count,
  from,
  onClose,
  footer,
  testid,
  children,
}: {
  title: string;
  count?: ReactNode;
  /** Where it grows from: the tile or card that opened it. */
  from?: DOMRect | null;
  onClose: () => void;
  footer?: ReactNode;
  testid?: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLElement>(null);
  const body = useRef<HTMLDivElement>(null);
  const id = useId();
  const docked = useMediaQuery("(min-width: 1200px)");
  const [leaving, setLeaving] = useState(false);
  const closing = useRef(false);
  // Leaves with its motion, then is gone (at once with motion turned down).
  const close = () => {
    if (closing.current) return;
    closing.current = true;
    if (reducedMotion()) return onClose();
    setLeaving(true);
    window.setTimeout(onClose, 260);
  };
  useFocusTrap(ref, !docked, close);
  const drag = useSheetDrag(ref, onClose);
  // On a computer the page makes room for the panel beside it.
  useEffect(() => {
    document.documentElement.classList.add("tk-sheet-open");
    return () => document.documentElement.classList.remove("tk-sheet-open");
  }, []);
  // Grows out of what opened it.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || !from || reducedMotion() || typeof el.animate !== "function")
      return;
    const to = el.getBoundingClientRect();
    if (!to.width || !to.height) return;
    const sx = from.width / to.width;
    const sy = from.height / to.height;
    const dx = from.left - to.left;
    const dy = from.top - to.top;
    el.classList.add("tk-morphing");
    const a = el.animate(
      [
        {
          transformOrigin: "0 0",
          transform: `translate(${dx}px, ${dy}px) scale(${sx}, ${sy})`,
          borderRadius: "16px",
        },
        { transformOrigin: "0 0", transform: "none" },
      ],
      { duration: 360, easing: "cubic-bezier(0.2, 0.9, 0.2, 1)" },
    );
    body.current?.animate(
      [{ opacity: 0 }, { opacity: 0, offset: 0.55 }, { opacity: 1 }],
      { duration: 420, easing: "ease-out" },
    );
    a.onfinish = () => el.classList.remove("tk-morphing");
  }, []);
  return (
    <>
      {!docked && (
        <div
          className={`tk-veil${leaving ? " leaving" : ""}`}
          onMouseDown={close}
          aria-hidden="true"
        />
      )}
      <section
        ref={ref}
        className={`tk-sheet${docked ? " docked" : ""}${leaving ? " leaving" : ""}`}
        role="dialog"
        aria-modal={!docked}
        aria-labelledby={id}
        tabIndex={-1}
        data-testid={testid}
        onKeyDown={(e) => {
          // Esc closes the sheet only, not what is under it.
          if (e.key === "Escape") {
            e.preventDefault();
            close();
          }
        }}
      >
        <div className="tk-sheet-grab" aria-hidden="true" {...drag} />
        <div ref={body} className="tk-sheet-body">
          <div className="tk-sheet-head">
            <h2 id={id}>
              {title}
              {count !== undefined && (
                <span className="tk-sheet-count">{count}</span>
              )}
            </h2>
            <button
              type="button"
              className="tk-round"
              aria-label={docked ? "Close panel" : "Close"}
              onClick={close}
            >
              <Icon name="close" />
            </button>
          </div>
          {children}
          {footer && <div className="tk-sheet-foot">{footer}</div>}
        </div>
      </section>
    </>
  );
}
