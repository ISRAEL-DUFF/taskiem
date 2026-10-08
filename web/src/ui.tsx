import { useCallback, useEffect, useId, useRef, useState, type ReactNode, type RefObject } from "react";
import { ApiError } from "./api";
import { Icon } from "./icons";
import { redact } from "./lib/timeline";

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

export function ErrorBox({ error }: { error: unknown }) {
  if (!error) return null;
  const e = error instanceof ApiError ? error : null;
  const msg = e ? e.message : error instanceof Error ? error.message : String(error);
  return (
    <div className="error" role="alert">
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

export function Field({ label, hint, children }: { label: string; hint?: ReactNode; children: ReactNode }) {
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

/** Runs an async action with a busy flag and an error. */
export function useAction(): { busy: boolean; error: unknown; run: (fn: () => Promise<unknown>) => Promise<boolean>; clear: () => void } {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const run = useCallback(async (fn: () => Promise<unknown>) => {
    setBusy(true);
    setError(undefined);
    try {
      await fn();
      return true;
    } catch (e) {
      setError(e);
      return false;
    } finally {
      setBusy(false);
    }
  }, []);
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

export const fmtTime = (s: string | null | undefined) => (s ? new Date(s).toLocaleString() : "—");
