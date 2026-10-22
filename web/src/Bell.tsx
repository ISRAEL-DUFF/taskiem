import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate } from "react-router-dom";
import { get, post } from "./api";
import { Icon } from "./icons";
import { toast } from "./toast";
import { ErrorBox, fmtTime, useFocusTrap } from "./ui";

/**
 * The bell (no-technical-steps N12): what each person needs to know, with
 * nothing set up. Approvals waiting for them are pinned at the top; the
 * rest are their notifications, newest first.
 */

export interface Notification {
  id: string;
  kind: string;
  level: "info" | "success" | "warning" | "problem";
  title: string;
  body: string;
  link?: string;
  count: number;
  created_at: string;
  updated_at: string;
  read_at?: string;
}

export interface BellCount {
  unread: number;
  approvals: number;
}

/** How often the count is fetched while the page is in view. */
export const BELL_POLL_MS = 30_000;

/** Where a notification's link goes in the app: a path, or "" for an outside address. */
export function inAppPath(link: string | undefined, origin = window.location.origin): string {
  if (!link) return "";
  if (link.startsWith("/")) return link;
  if (link.startsWith(origin + "/")) return link.slice(origin.length);
  return "";
}

/** Today's and earlier notifications. */
export function byDay(list: Notification[], now = new Date()): { today: Notification[]; earlier: Notification[] } {
  const start = new Date(now);
  start.setHours(0, 0, 0, 0);
  return { today: list.filter((n) => new Date(n.updated_at) >= start), earlier: list.filter((n) => new Date(n.updated_at) < start) };
}

/**
 * The bell's count, kept current: on load, on each page, every 30 seconds
 * while the tab is in view and when it comes back. A new problem while
 * the person is looking is also shown as a toast.
 */
export function useBellCount(enabled: boolean, pathname: string): BellCount & { reload: () => void } {
  const [count, setCount] = useState<BellCount>({ unread: 0, approvals: 0 });
  const last = useRef<number | null>(null);
  const load = useCallback(() => {
    if (!enabled || document.visibilityState === "hidden") return;
    get<BellCount>("/v1/notifications/count").then(
      (c) => {
        setCount(c);
        if (last.current !== null && c.unread > last.current) {
          void get<{ notifications: Notification[] }>("/v1/notifications?unread=true").then(
            (r) => {
              const n = r.notifications[0];
              if (n && n.level === "problem") toast(n.title, "error", { href: inAppPath(n.link) || undefined, linkText: "Open" });
            },
            () => undefined,
          );
        }
        last.current = c.unread;
      },
      () => undefined,
    );
  }, [enabled]);
  useEffect(load, [load, pathname]);
  useEffect(() => {
    if (!enabled) return;
    const t = setInterval(load, BELL_POLL_MS);
    document.addEventListener("visibilitychange", load);
    return () => {
      clearInterval(t);
      document.removeEventListener("visibilitychange", load);
    };
  }, [enabled, load]);
  return { ...count, reload: load };
}

export function Bell({ count, onChange }: { count: BellCount; onChange: () => void }) {
  const [open, setOpen] = useState(false);
  const total = count.unread + count.approvals;
  const label = total > 0 ? `Notifications, ${total} new` : "Notifications";
  return (
    <>
      <button type="button" className="icon-button bell" aria-label={label} aria-expanded={open} onClick={() => setOpen(!open)}>
        <Icon name="bell" />
        {total > 0 && (
          <span className="count alert" aria-hidden="true">
            {total > 99 ? "99+" : total}
          </span>
        )}
      </button>
      {open && (
        <BellPanel
          approvals={count.approvals}
          onClose={() => {
            setOpen(false);
            onChange();
          }}
        />
      )}
    </>
  );
}

function BellPanel({ approvals, onClose }: { approvals: number; onClose: () => void }) {
  const navigate = useNavigate();
  const ref = useRef<HTMLDivElement>(null);
  useFocusTrap(ref, true, onClose);
  const [list, setList] = useState<Notification[]>();
  const [error, setError] = useState<unknown>();
  const load = useCallback(() => get<{ notifications: Notification[] }>("/v1/notifications").then((r) => setList(r.notifications), setError), []);
  useEffect(() => void load(), [load]);
  const open = async (n: Notification) => {
    if (!n.read_at) await post("/v1/notifications/read", { ids: [n.id] }).catch(() => undefined);
    const path = inAppPath(n.link);
    onClose();
    if (path) navigate(path);
    else if (n.link) window.open(n.link, "_blank", "noopener");
  };
  const readAll = async () => {
    await post("/v1/notifications/read", { all: true });
    await load();
  };
  const { today, earlier } = byDay(list ?? []);
  const unread = (list ?? []).some((n) => !n.read_at);
  const item = (n: Notification) => (
    <li key={n.id}>
      <button type="button" className={`bell-item level-${n.level}${n.read_at ? "" : " unread"}`} onClick={() => void open(n)}>
        <strong>{n.title}</strong>
        {n.body && <span>{n.body}</span>}
        <span className="hint">{fmtTime(n.updated_at)}</span>
      </button>
    </li>
  );
  return (
    <>
      <div className="nav-scrim bell-scrim" aria-hidden="true" onClick={onClose} />
      <div className="bell-panel" role="dialog" aria-label="Notifications" ref={ref} tabIndex={-1}>
        <div className="bell-head">
          <h2>Notifications</h2>
          {unread && (
            <button type="button" className="small" onClick={() => void readAll()}>
              Mark all read
            </button>
          )}
          <button type="button" className="icon-button" aria-label="Close" onClick={onClose}>
            <Icon name="close" />
          </button>
        </div>
        <ErrorBox error={error} />
        {approvals > 0 && (
          <section aria-label="Waiting for you">
            <h3>Waiting for you</h3>
            <Link className="bell-item level-warning unread" to="/approvals" onClick={onClose}>
              <strong>{approvals === 1 ? "1 approval waits for you" : `${approvals} approvals wait for you`}</strong>
            </Link>
          </section>
        )}
        {list && list.length === 0 && approvals === 0 && <p className="hint">Nothing new. Failed runs, setup problems and your runs that finish show here.</p>}
        {today.length > 0 && (
          <section aria-label="Today">
            <h3>Today</h3>
            <ul className="bell-list">{today.map(item)}</ul>
          </section>
        )}
        {earlier.length > 0 && (
          <section aria-label="Earlier">
            <h3>Earlier</h3>
            <ul className="bell-list">{earlier.map(item)}</ul>
          </section>
        )}
        <p className="hint">
          <Link to="/settings/notifications" onClick={onClose}>
            How you hear beyond the app
          </Link>
        </p>
      </div>
    </>
  );
}
