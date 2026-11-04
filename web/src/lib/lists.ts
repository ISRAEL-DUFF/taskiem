// Filters for the Workflows and Runs lists that the API has no query
// parameter for, applied to what it returned.
import type { RunSummary, WorkflowSummary } from "../api";

const words = (q: string) => q.trim().toLowerCase().split(/\s+/).filter(Boolean);

/** Workflows whose name or key holds every word of `q`, and that are
 * published ("published"), not published ("draft") or either (""). */
export function filterWorkflows(all: WorkflowSummary[], q: string, state: string): WorkflowSummary[] {
  const ws = words(q);
  return all.filter(
    (w) =>
      ws.every((x) => w.name.toLowerCase().includes(x) || w.key.toLowerCase().includes(x)) &&
      (state === "" || (state === "published" ? w.active_version !== null : w.active_version === null)),
  );
}

/** The start of the day after `day` (YYYY-MM-DD, local time), as RFC 3339:
 * runs "up to and including" a day are the ones started before it. */
export function dayAfter(day: string): string {
  const [y, m, d] = day.split("-").map(Number);
  return new Date(y ?? 1970, (m ?? 1) - 1, (d ?? 1) + 1).toISOString();
}

/** The start of `day` (YYYY-MM-DD, local time) in milliseconds, or 0. */
export const dayStart = (day: string) => (day ? new Date(`${day}T00:00:00`).getTime() : 0);

/** Runs started on or after `from` whose workflow, ID or starter holds
 * every word of `q`. */
export function filterRuns(runs: RunSummary[], q: string, from: string): RunSummary[] {
  const ws = words(q);
  const since = dayStart(from);
  return runs.filter((r) => {
    if (since && new Date(r.started_at).getTime() < since) return false;
    const text = `${r.workflow} ${r.id} ${r.started_by ?? ""}`.toLowerCase();
    return ws.every((w) => text.includes(w));
  });
}

/** Runs grouped by the day they started, newest first, in the browser's time
 * zone: "Today", "Yesterday", then "Monday 19 October". */
export function dayGroups<T extends { started_at: string }>(runs: T[], now = new Date()): { label: string; runs: T[] }[] {
  const key = (d: Date) => `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
  const today = key(now);
  const y = new Date(now);
  y.setDate(y.getDate() - 1);
  const yesterday = key(y);
  const out: { label: string; runs: T[] }[] = [];
  const at = new Map<string, number>();
  for (const r of runs) {
    const d = new Date(r.started_at);
    const k = key(d);
    let i = at.get(k);
    if (i === undefined) {
      const label = k === today ? "Today" : k === yesterday ? "Yesterday" : d.toLocaleDateString("en-GB", { weekday: "long", day: "numeric", month: "long" });
      i = out.push({ label, runs: [] }) - 1;
      at.set(k, i);
    }
    out[i]?.runs.push(r);
  }
  return out;
}

/** A run's icon and its tint, by status. */
export function runLook(status: string): { icon: string; tone?: "warn" | "danger" | "ok" | "info" | "muted" } {
  switch (status) {
    case "completed":
      return { icon: "start", tone: "ok" };
    case "failed":
    case "needs_reconciliation":
      return { icon: "alerts", tone: "danger" };
    case "waiting":
      return { icon: "clock", tone: "warn" };
    case "running":
    case "queued":
      return { icon: "runs", tone: "info" };
    default:
      return { icon: "close", tone: "muted" };
  }
}

/** A run's steps so far by how they stand, in words ("2 done · 1 waiting"); "" with none yet. */
export function stepCounts(statuses: string[]): string {
  const n: Record<string, number> = {};
  for (const s of statuses) {
    const k = s === "completed" || s === "skipped" ? "done" : s === "failed" || s === "parked" ? "stuck" : s === "waiting" ? "waiting" : s === "cancelled" ? "stopped" : "going";
    n[k] = (n[k] ?? 0) + 1;
  }
  const said: Record<string, string> = { done: "done", going: "running", waiting: "waiting", stuck: "stuck", stopped: "stopped" };
  return ["done", "going", "waiting", "stuck", "stopped"].filter((k) => n[k]).map((k) => `${n[k]} ${said[k]}`).join(" · ");
}
