import { useCallback, useEffect, useId, useRef, useState, type ReactNode, type RefObject } from "react";
import { onScreen, toast } from "./toast";
import { ApiError } from "./api";
import { Icon } from "./icons";
import { redact } from "./lib/timeline";
import { whenWords } from "./lib/time";

/** Loads data, reloads on demand, and optionally polls while `poll` is true. */
export function useLoad<T>(load: () => Promise<T>, deps: unknown[], poll = false): { data: T | undefined; error: unknown; reload: () => void } {
  const [data, setData] = useState<T>();
  const [error, setError] = useState<unknown>();
  const [tick, setTick] = useState(0);
  const fn = useRef(load);
  fn.current = load;
  useEffect(() => {
    let live = true;
    fn.current().then(
      (d) => {
        if (live) {
          setData(d);
          setError(undefined);
        }
      },
      (e: unknown) => live && setError(e),
    );
    return () => {
      live = false;
    };
  }, [...deps, tick]);
  useEffect(() => {
    if (!poll) return;
    const t = setInterval(() => setTick((n) => n + 1), 2000);
    return () => clearInterval(t);
  }, [poll]);
  return { data, error, reload: useCallback(() => setTick((n) => n + 1), []) };
}

/** Where each action's error is shown, for a toast's Show me. */
const anchors = new WeakMap<object, string>();

/** An error's text for people. */
export function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

export function ErrorBox({ error }: { error: unknown }) {
  if (!error) return null;
  const e = error instanceof ApiError ? error : null;
  const msg = e ? e.message : error instanceof Error ? error.message : String(error);
  return (
    <div className="error" role="alert" id={typeof error === "object" ? anchors.get(error) : undefined}>
      {msg}
      {e && e.problems.length > 0 && (
        <ul>
          {e.problems.map((p, i) => (
            <li key={i}>{typeof p === "string" ? p : `${p.path}: ${p.message}`}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

export function Badge({ value }: { value: string }) {
  return <span className={`badge ${value}`}>{value.replaceAll("_", " ")}</span>;
}

export function Json({ value }: { value: unknown }) {
  if (value === undefined) return <span className="hint">—</span>;
  return <pre>{JSON.stringify(redact(value), null, 2)}</pre>;
}

/** A labelled field. data-technical marks one an owner meets only under
 * Advanced (no-technical-steps N5: the gate's word test leaves it out). */
export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode; "data-technical"?: boolean }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
      {hint && <div className="hint">{hint}</div>}
    </label>
  );
}

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]):not([type="hidden"]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/** The elements Tab reaches inside `box`, in order. */
export function focusables(box: HTMLElement): HTMLElement[] {
  return Array.from(box.querySelectorAll<HTMLElement>(FOCUSABLE)).filter((el) => el.getClientRects().length > 0);
}

/** Keeps Tab inside `box` while it is open, closes on Escape, and gives
 * focus back to whatever had it before when it closes. `top` says whether
 * this box is the one on top (a modal opened from a modal owns the keys). */
export function useFocusTrap(ref: RefObject<HTMLElement | null>, open: boolean, onClose: () => void, top: () => boolean = () => true) {
  const close = useRef(onClose);
  close.current = onClose;
  const isTop = useRef(top);
  isTop.current = top;
  // What had focus when this first rendered: by the time the effect runs, an
  // autoFocus field inside the box may already have taken it.
  const [atMount] = useState(() => (document.activeElement instanceof HTMLElement ? document.activeElement : null));
  useEffect(() => {
    const box = ref.current;
    if (!open || !box) return;
    const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const opener = active && box.contains(active) ? atMount : active;
    // An autoFocus field inside keeps focus; otherwise the first field, or the box.
    if (!box.contains(document.activeElement)) {
      const fields = focusables(box);
      (fields.find((el) => el.matches("input, select, textarea")) ?? box).focus();
    }
    const onKey = (e: KeyboardEvent) => {
      if (!isTop.current()) return;
      if (e.key === "Escape") {
        e.stopPropagation();
        close.current();
        return;
      }
      if (e.key !== "Tab") return;
      const f = focusables(box);
      const first = f[0];
      const last = f[f.length - 1];
      if (!first || !last) {
        e.preventDefault();
        return;
      }
      const at = document.activeElement;
      if (!box.contains(at)) {
        e.preventDefault();
        first.focus();
      } else if (e.shiftKey && (at === first || at === box)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && at === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("keydown", onKey);
      if (opener?.isConnected) opener.focus();
    };
  }, [ref, open, atMount]);
}

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  const id = useId();
  // A modal opened from inside this one is on top and owns the keys.
  useFocusTrap(ref, true, onClose, () => !ref.current?.querySelector(".modal"));
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-modal="true" aria-labelledby={id} tabIndex={-1} ref={ref}>
        <h2 id={id} style={{ marginTop: 0 }}>
          {title}
        </h2>
        {children}
      </div>
    </div>
  );
}

/** On narrow screens wide tables scroll sideways inside themselves (see
 * styles.css); a table that does is made focusable so the keyboard can
 * scroll it too. Watches `box` for tables coming and going. */
export function useScrollableTables(box: RefObject<HTMLElement | null>) {
  useEffect(() => {
    const root = box.current;
    if (!root) return;
    let frame = 0;
    const mark = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        for (const t of Array.from(root.querySelectorAll<HTMLTableElement>("table"))) {
          const scrolls = t.scrollWidth > t.clientWidth + 1;
          if (scrolls && !t.hasAttribute("tabindex")) t.setAttribute("tabindex", "0");
          else if (!scrolls && t.getAttribute("tabindex") === "0") t.removeAttribute("tabindex");
        }
      });
    };
    const watch = new MutationObserver(mark);
    watch.observe(root, { childList: true, subtree: true });
    window.addEventListener("resize", mark);
    mark();
    return () => {
      cancelAnimationFrame(frame);
      watch.disconnect();
      window.removeEventListener("resize", mark);
    };
  }, [box]);
}

/** A page's title, a one-line description of what it is for, and its main actions. */
export function PageHeader({ title, description, actions }: { title: ReactNode; description?: ReactNode; actions?: ReactNode }) {
  return (
    <div className="page-header">
      <div className="page-heading">
        <h1>{title}</h1>
        {description && <p className="page-desc">{description}</p>}
      </div>
      {actions && <div className="page-actions">{actions}</div>}
    </div>
  );
}

/** What a list shows before it has anything: an icon, one sentence saying
 * what goes here, and the action that adds the first one. */
export function EmptyState({ icon, children, action }: { icon: string; children: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty-state">
      <span className="empty-icon">
        <Icon name={icon} />
      </span>
      <p>{children}</p>
      {action && <div className="empty-action">{action}</div>}
    </div>
  );
}

/** A placeholder in the shape of a list while it loads. */
export function Skeleton({ rows = 4, label = "Loading…" }: { rows?: number; label?: string }) {
  return (
    <div className="skeleton" aria-busy="true">
      <span className="sr-only">{label}</span>
      {Array.from({ length: rows }, (_, i) => (
        <div key={i} className="skeleton-row" aria-hidden="true">
          <span />
          <span />
          <span />
        </div>
      ))}
    </div>
  );
}

/** Reloads every 30 seconds while the page is visible, and when the window gets focus again. */
export function useLiveRefresh(reload: () => void) {
  useEffect(() => {
    const every = setInterval(() => document.visibilityState === "visible" && reload(), 30_000);
    const back = () => document.visibilityState === "visible" && reload();
    window.addEventListener("focus", back);
    document.addEventListener("visibilitychange", back);
    return () => {
      clearInterval(every);
      window.removeEventListener("focus", back);
      document.removeEventListener("visibilitychange", back);
    };
  }, [reload]);
}

/** Runs an async action with a busy flag and an error. */
let actionSeq = 0;

/**
 * An action a person starts (save, publish, upload). run(fn, success)
 * says success in a toast ("Saved"), or nothing when it is false (the
 * page shows the result, or moves on). An error is shown where the page
 * puts its ErrorBox, and in a toast too when that box is not on screen,
 * with Show me (no-technical-steps N12).
 */
export function useAction(): {
  busy: boolean;
  error: unknown;
  run: (fn: () => Promise<unknown>, success: string | false) => Promise<boolean>;
  clear: () => void;
} {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [anchor] = useState(() => `action-error-${++actionSeq}`);
  const run = useCallback(
    async (fn: () => Promise<unknown>, success: string | false) => {
      setBusy(true);
      setError(undefined);
      try {
        await fn();
        if (success) toast(success);
        return true;
      } catch (e) {
        if (typeof e === "object" && e !== null) anchors.set(e, anchor);
        setError(e);
        // After the page has drawn the error: toast it if it cannot be seen.
        requestAnimationFrame(() =>
          requestAnimationFrame(() => {
            if (!onScreen(anchor)) toast(errorText(e), "error", document.getElementById(anchor) ? { target: anchor } : {});
          }),
        );
        return false;
      } finally {
        setBusy(false);
      }
    },
    [anchor],
  );
  return { busy, error, run, clear: () => setError(undefined) };
}

/** A JSON text area that reports parse errors and only emits valid values. */
export function JsonInput({ value, onChange, rows = 6 }: { value: unknown; onChange: (v: unknown) => void; rows?: number }) {
  const [text, setText] = useState(() => (value === undefined ? "" : JSON.stringify(value, null, 2)));
  const [bad, setBad] = useState(false);
  const last = useRef(value);
  useEffect(() => {
    if (value !== last.current) {
      last.current = value;
      setText(value === undefined ? "" : JSON.stringify(value, null, 2));
      setBad(false);
    }
  }, [value]);
  return (
    <>
      <textarea
        className="mono"
        rows={rows}
        value={text}
        spellCheck={false}
        onChange={(e) => {
          setText(e.target.value);
          if (e.target.value.trim() === "") {
            setBad(false);
            last.current = undefined;
            onChange(undefined);
            return;
          }
          try {
            const v: unknown = JSON.parse(e.target.value);
            setBad(false);
            last.current = v;
            onChange(v);
          } catch {
            setBad(true);
          }
        }}
      />
      {bad && <div className="hint" style={{ color: "var(--danger)" }}>Not valid JSON yet; the last valid value is kept.</div>}
    </>
  );
}

export const fmtTime = (s: string | null | undefined) => (s ? whenWords(new Date(s)) : "—");

/** A number that counts up to `to` when it first shows, then follows it;
 * no counting for people who ask for less motion. */
const stillMotion = () => typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

export function useCountUp(to: number, ms = 700): number {
  const [shown, setShown] = useState(() => (stillMotion() ? to : 0));
  const from = useRef<number | null>(null);
  useEffect(() => {
    const still = stillMotion();
    const start = from.current ?? 0;
    from.current = to;
    if (still || start === to) {
      setShown(to);
      return;
    }
    let frame = 0;
    const t0 = performance.now();
    const step = (t: number) => {
      const k = Math.min(1, (t - t0) / ms);
      setShown(Math.round(start + (to - start) * (1 - Math.pow(1 - k, 3))));
      if (k < 1) frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [to, ms]);
  return shown;
}
