import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { ApiError } from "./api";
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

export function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="modal-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="modal" role="dialog" aria-label={title}>
        <h2 style={{ marginTop: 0 }}>{title}</h2>
        {children}
      </div>
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
