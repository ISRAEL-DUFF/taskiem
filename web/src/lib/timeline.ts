// Rebuilds per-step status from a run's events (spec 15.1, run inspector).
import type { RunEvent } from "../api";

export type StepStatus = "scheduled" | "running" | "completed" | "failed" | "skipped" | "cancelled" | "waiting" | "retrying" | "parked";

export interface StepRow {
  id: string; // step instance id, e.g. pay_all[2].pay_employee
  status: StepStatus;
  attempts: number;
  firstAt: string;
  lastAt: string;
  input?: unknown;
  output?: unknown;
  error?: { kind?: string; message?: string; next?: string };
  waitingFor?: string;
  logs?: unknown;
  events: RunEvent[];
}

interface Payload {
  input?: unknown;
  output?: unknown;
  logs?: unknown;
  error?: { kind?: string; message?: string; next?: string };
  kind?: string;
  event?: string;
  role?: string;
  queue?: string;
  form?: { title?: string };
}

export function timeline(events: RunEvent[]): StepRow[] {
  const rows = new Map<string, StepRow>();
  for (const ev of events) {
    if (!ev.step_id) continue;
    const p = (ev.payload ?? {}) as Payload;
    let r = rows.get(ev.step_id);
    if (!r) {
      r = { id: ev.step_id, status: "scheduled", attempts: 0, firstAt: ev.recorded_at, lastAt: ev.recorded_at, events: [] };
      rows.set(ev.step_id, r);
    }
    r.events.push(ev);
    r.lastAt = ev.recorded_at;
    switch (ev.type) {
      case "StepScheduled":
        r.attempts = Math.max(r.attempts, ev.attempt ?? 1);
        r.input = p.input;
        r.status = p.kind === "signal" ? "waiting" : p.kind === "timer" ? "waiting" : "scheduled";
        if (p.kind === "signal") r.waitingFor = p.form ? "the form to be filled in" : `signal ${p.event ?? ""}`;
        if (p.kind === "timer") r.waitingFor = "timer";
        break;
      case "StepStarted":
      case "EffectIntent":
        r.status = "running";
        break;
      case "ApprovalRequested":
        r.status = "waiting";
        r.waitingFor = p.role ? `approval by ${p.role}` : "approval";
        break;
      case "StepCompleted":
        r.status = "completed";
        r.output = p.output;
        r.logs = p.logs;
        r.error = undefined;
        r.waitingFor = undefined;
        break;
      case "StepFailed":
        r.error = p.error;
        r.status = p.error?.next === "retry" || p.error?.next === "reconcile" ? "retrying" : p.error?.next === "park" ? "parked" : "failed";
        break;
      case "StepSkipped":
        r.status = "skipped";
        break;
      case "StepCancelled":
        r.status = "cancelled";
        r.waitingFor = undefined;
        break;
    }
  }
  return [...rows.values()].sort((a, b) => (a.events[0]?.seq ?? 0) - (b.events[0]?.seq ?? 0));
}

/** True for values the engine stored sealed (personal data, spec 4.9). */
export function isSealed(v: unknown): boolean {
  return typeof v === "object" && v !== null && "$pii" in v && "ct" in v;
}

/** Replaces sealed envelopes with a marker, for display. */
export function redact(v: unknown): unknown {
  if (isSealed(v)) return `🔒 ${(v as { $pii: string }).$pii}`;
  if (Array.isArray(v)) return v.map(redact);
  if (v && typeof v === "object") return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, redact(x)]));
  return v;
}

export function duration(from: string, to: string): string {
  const ms = new Date(to).getTime() - new Date(from).getTime();
  if (!Number.isFinite(ms) || ms < 0) return "";
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
  if (ms < 3_600_000) return `${Math.round(ms / 60_000)} min`;
  return `${(ms / 3_600_000).toFixed(1)} h`;
}

// Status of each top-level step for the live canvas: a step's own row and
// those of the instances nested in it (pay_all[2].pay, branch.path.step)
// combine, the most pressing first.
const PRIORITY: StepStatus[] = ["failed", "parked", "running", "retrying", "waiting", "scheduled", "completed", "cancelled", "skipped"];

export function topLevelStatus(rows: StepRow[], ids: string[]): Record<string, StepStatus> {
  const out: Record<string, StepStatus> = {};
  for (const id of ids) {
    // A template or capability step's expanded steps are <id>__<name>.
    const mine = rows.filter((r) => r.id === id || r.id.startsWith(id + "[") || r.id.startsWith(id + ".") || r.id.startsWith(id + "__"));
    if (mine.length === 0) continue;
    const own = mine.find((r) => r.id === id);
    // A finished container step is what it says; while it runs, its
    // children show what is happening inside it.
    if (own && ["completed", "failed", "cancelled", "skipped"].includes(own.status)) {
      out[id] = own.status;
      continue;
    }
    out[id] = PRIORITY.find((p) => mine.some((r) => r.status === p)) ?? (mine[0] as StepRow).status;
  }
  return out;
}

/** A run's rows, with each top-level template step's rows together under it. */
export interface RowGroup {
  /** The template step's id, or null for a row of the workflow's own. */
  template: string | null;
  rows: StepRow[];
}

/**
 * Groups a run's rows by template step (decision 0047): a template step T
 * runs as T__body, T__input, T__<its steps> and T itself (its outputs), so
 * a row whose step is T or starts with T__ is T's, where T__body is a row.
 * Groups keep the order of their first row.
 */
export function templateGroups(rows: StepRow[]): RowGroup[] {
  const templates = new Set(rows.map((r) => /^([a-z][a-z0-9_]*?)__body$/.exec(r.id)?.[1]).filter((t): t is string => t !== undefined && !t.includes("__")));
  const out: RowGroup[] = [];
  const byTemplate = new Map<string, RowGroup>();
  for (const r of rows) {
    const head = r.id.split(".")[0]?.replace(/\[\d+\]$/, "") ?? r.id;
    const t = [...templates].find((x) => head === x || head.startsWith(x + "__"));
    if (t === undefined) {
      out.push({ template: null, rows: [r] });
      continue;
    }
    let g = byTemplate.get(t);
    if (!g) {
      g = { template: t, rows: [] };
      byTemplate.set(t, g);
      out.push(g);
    }
    g.rows.push(r);
  }
  return out;
}
