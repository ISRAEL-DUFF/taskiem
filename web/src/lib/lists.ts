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
