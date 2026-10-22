import { useEffect, useState } from "react";

/**
 * Messages after an action (no-technical-steps N12): "Saved", "Published",
 * or an error the person cannot see where it happened. One Toaster is
 * mounted in the shell; anything calls toast().
 */

export type ToastKind = "success" | "error" | "info";

export interface Toast {
  id: number;
  kind: ToastKind;
  text: string;
  /** An element to scroll to ("Show me"): the error's own box. */
  target?: string;
  /** A link to follow. */
  href?: string;
  linkText?: string;
}

/** How long a success or info message stays; errors stay until dismissed. */
export const TOAST_MS = 4000;
/** At most this many at once: the oldest goes. */
export const MAX_TOASTS = 3;

let push: ((t: Omit<Toast, "id">) => void) | null = null;
let seq = 0;

/** Shows a message; nothing when no Toaster is mounted (tests, the embed). */
export function toast(text: string, kind: ToastKind = "success", extra: Partial<Pick<Toast, "target" | "href" | "linkText">> = {}) {
  push?.({ text, kind, ...extra });
}

/** Adds t to list, keeping at most MAX_TOASTS and never the same twice. */
export function addToast(list: Toast[], t: Toast): Toast[] {
  const rest = list.filter((x) => !(x.text === t.text && x.kind === t.kind));
  return [...rest, t].slice(-MAX_TOASTS);
}

export function Toaster() {
  const [list, setList] = useState<Toast[]>([]);
  useEffect(() => {
    push = (t) => {
      const id = ++seq;
      setList((l) => addToast(l, { ...t, id }));
      if (t.kind !== "error") setTimeout(() => setList((l) => l.filter((x) => x.id !== id)), TOAST_MS);
    };
    return () => {
      push = null;
    };
  }, []);
  const close = (id: number) => setList((l) => l.filter((x) => x.id !== id));
  return (
    <div className="toasts" aria-live="polite" aria-atomic="false">
      {list.map((t) => (
        // The region is live: a success needs no role of its own (and so
        // never clashes with a page's own status line); an error is an alert.
        <div key={t.id} className={`toast toast-${t.kind}`} role={t.kind === "error" ? "alert" : undefined}>
          <span className="toast-text">{t.text}</span>
          {t.target && (
            <button
              type="button"
              className="small"
              onClick={() => {
                document.getElementById(t.target ?? "")?.scrollIntoView({ behavior: "smooth", block: "center" });
                close(t.id);
              }}
            >
              Show me
            </button>
          )}
          {t.href && (
            <a className="button small" href={t.href} onClick={() => close(t.id)}>
              {t.linkText ?? "Open"}
            </a>
          )}
          <button type="button" className="toast-close" aria-label="Dismiss" onClick={() => close(t.id)}>
            ×
          </button>
        </div>
      ))}
    </div>
  );
}

/** Whether an element is on screen now. */
export function onScreen(id: string): boolean {
  const el = document.getElementById(id);
  if (!el) return false;
  const r = el.getBoundingClientRect();
  return r.bottom > 0 && r.top < window.innerHeight && r.width > 0;
}
