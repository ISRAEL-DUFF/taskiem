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

/** A save, done: the tick draws itself, with a light buzz. */
export function SavedTick({ words = "Saved" }: { words?: string }) {
  useEffect(() => {
    if (!reducedMotion()) navigator.vibrate?.(12);
  }, []);
  return (
    <div className="tk-saved" role="status">
      <svg viewBox="0 0 52 52" aria-hidden="true" className="tk-tick">
        <circle cx="26" cy="26" r="24" />
        <path d="M15 27l7 7 15-16" style={{ ["--len" as string]: 34 }} />
      </svg>
      <span>{words}</span>
    </div>
  );
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
  error,
  children,
}: {
  label: string;
  hint?: ReactNode;
  /** What is wrong with what was typed, said under it. */
  error?: string;
  children: ReactNode;
}) {
  return (
    <label className="tk-field">
      <span className="tk-field-label">{label}</span>
      {children}
      {error && (
        <span className="tk-field-error" role="alert">
          {error}
        </span>
      )}
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
  // "Price of Rice: ₦76,000 → ₦78,000": the new value rolls into place.
  const words =
    typeof children === "string" && children.includes(" → ") ? (
      <>
        {children.slice(0, children.lastIndexOf(" → ") + 3)}
        <b className={fresh ? "tk-roll" : undefined}>
          {children.slice(children.lastIndexOf(" → ") + 3)}
        </b>
      </>
    ) : (
      children
    );
  const body = (
    <>
      <Icon name="tick" />
      <span>{words}</span>
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
      {/* When it was, for a screen reader; the boards draw the line alone. */}
      <span className="tk-diary-when">
        <span className="sr-only">{when}</span>
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
  onBack,
  shut = false,
  headExtra,
  lead,
  sub,
  crumb,
  children,
}: {
  title: string;
  count?: ReactNode;
  /** Where it grows from: the tile or card that opened it. */
  from?: DOMRect | null;
  onClose: () => void;
  footer?: ReactNode;
  testid?: string;
  /** A step back inside the sheet (from a form to its list). */
  onBack?: () => void;
  /** Closes it, with its motion (after a save). */
  shut?: boolean;
  /** Beside the title: a link to the full page. */
  headExtra?: ReactNode;
  /** Before the title: the business's mark (the Buyer board). */
  lead?: ReactNode;
  /** Under the title: "Your prices · updated today". */
  sub?: ReactNode;
  /** With onBack: the list's name, for a "Products / Jollof rice" head. */
  crumb?: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLElement>(null);
  const body = useRef<HTMLDivElement>(null);
  const id = useId();
  const docked = useMediaQuery("(min-width: 1200px)");
  const [leaving, setLeaving] = useState(false);
  // Opened from a tile or a card: it grows out of it, and that is its one
  // entrance. The class is in the markup from the first render, so the CSS
  // rise is off before the sheet is measured, and stays off: removing it
  // when the grow ended played the rise again (a sheet that came up twice).
  const [grown] = useState(() => !!from && !reducedMotion());
  const closing = useRef(false);
  // Leaves with its motion, then is gone (at once with motion turned down).
  const close = () => {
    if (closing.current) return;
    closing.current = true;
    if (reducedMotion()) return onClose();
    setLeaving(true);
    window.setTimeout(onClose, 260);
  };
  // A step inside (a list to its form and back) keeps the focus in the
  // sheet: what was tapped is gone, and Esc must still reach it.
  useEffect(() => {
    const el = ref.current;
    if (el && !el.contains(document.activeElement))
      el.focus({ preventScroll: true });
  }, [title]);
  // Esc steps back from a form to its list, then closes the sheet only.
  const escape = () => (onBack ? onBack() : close());
  useFocusTrap(ref, !docked, escape);
  useEffect(() => {
    if (shut) close();
  }, [shut]);
  const drag = useSheetDrag(ref, onClose);
  // On a computer the page makes room for the panel beside it.
  useEffect(() => {
    document.documentElement.classList.add("tk-sheet-open");
    return () => document.documentElement.classList.remove("tk-sheet-open");
  }, []);
  // Grows out of what opened it.
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || !from || !grown || typeof el.animate !== "function") return;
    const to = el.getBoundingClientRect();
    if (!to.width || !to.height) return;
    const sx = from.width / to.width;
    const sy = from.height / to.height;
    const dx = from.left - to.left;
    const dy = from.top - to.top;
    el.animate(
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
        className={`tk-sheet${docked ? " docked" : ""}${grown ? " tk-grown" : ""}${leaving ? " leaving" : ""}`}
        role="dialog"
        aria-modal={!docked}
        aria-labelledby={id}
        tabIndex={-1}
        data-testid={testid}
        onKeyDown={(e) => {
          // Esc steps back from a form to its list, then closes the
          // sheet only, not what is under it.
          // (With the focus held, the trap hears Esc; docked, this does.)
          if (e.key === "Escape" && docked) {
            e.preventDefault();
            escape();
          }
        }}
      >
        <div className="tk-sheet-grab" aria-hidden="true" {...drag} />
        <div ref={body} className="tk-sheet-body">
          <div className={`tk-sheet-head${onBack && crumb ? " crumbed" : ""}`}>
            {onBack && !crumb && (
              <button
                type="button"
                className="tk-round tk-back"
                aria-label="Back"
                onClick={onBack}
              >
                <Icon name="back" />
              </button>
            )}
            {onBack && crumb && (
              // Where you are, as the boards draw it: "Products / Jollof
              // rice", the first word the way back.
              <button type="button" className="tk-crumb-back" onClick={onBack}>
                {crumb}
                <span className="sr-only">: back to the list</span>
              </button>
            )}
            {lead}
            <div className="tk-sheet-titles">
              <h2 id={id}>
                {title}
                {count !== undefined && (
                  <span className="tk-sheet-count">{count}</span>
                )}
              </h2>
              {sub && <span className="tk-sheet-sub">{sub}</span>}
            </div>
            {headExtra}
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
