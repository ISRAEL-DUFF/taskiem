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
        if (p.kind === "signal") r.waitingFor = `signal ${p.event ?? ""}`;
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
