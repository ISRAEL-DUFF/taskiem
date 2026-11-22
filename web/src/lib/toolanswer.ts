// A tool's start and its answer (made tools §7, §8): what POST
// /v1/tools/{command}/start says, and what GET /v1/runs/{id}/answer draws,
// in words a person reads.

import { ApiError } from "../api";

/** A value as the person sees it before the run (masked). */
export interface ToolValue {
  name: string;
  label: string;
  value: string;
}

/** POST /v1/tools/{command}/start: the run at once, or a yes it waits for. */
export interface ToolStart {
  run_id?: string;
  care: "answers" | "yes" | "step_up";
  confirm?: string;
  expires_at?: string;
  created?: boolean;
  values: ToolValue[];
}

/** A button on an answer: it opens the tool it names, filled in. */
export interface ToolButton {
  label: string;
  tool: string;
  inputs?: Record<string, unknown>;
  once?: boolean;
}

export interface AnswerState {
  text: string;
  tone?: string;
}

export interface AnswerRow {
  title: string;
  sub?: string;
  state?: AnswerState;
  actions?: ToolButton[];
}

/** GET /v1/runs/{id}/answer. */
export interface ToolAnswer {
  status: string;
  type: "outcome" | "text" | "card" | "list";
  tool?: string;
  text?: string;
  title?: string;
  ref?: string;
  state?: AnswerState;
  lines?: { label: string; value: string }[];
  amount_kobo?: number;
  buttons?: ToolButton[];
  rows?: AnswerRow[];
  empty?: string;
  error?: string;
}

/** The pill tones of the kit. */
export type Tone = "lime" | "warn" | "danger" | "plain";

/** A state's tone (plain, good, warn, bad) as the kit's pill. */
export function answerTone(tone?: string): Tone {
  switch (tone) {
    case "good":
      return "lime";
    case "warn":
      return "warn";
    case "bad":
      return "danger";
    default:
      return "plain";
  }
}

/** Whether the run is still on its way, so the answer is asked for again. */
export const stillGoing = (status: string) =>
  status === "running" || status === "queued" || status === "waiting";

/** How long to wait before asking again: gently, a second, then longer,
 * never more than five. */
export const nextAsk = (asked: number) => Math.min(5000, 1000 + asked * 500);

/** What to say while there is no answer to draw (undefined when there is
 * one): on its way, waiting, stopped, failed, or done with nothing to say. */
export function answerWords(a: ToolAnswer): string | undefined {
  switch (a.status) {
    case "running":
      return "Working on it…";
    case "queued":
      return "Queued behind other work. It starts by itself.";
    case "waiting":
      return "Waiting for someone to answer or approve. You will see the answer here when it is done.";
    case "failed":
      return a.error ? `It did not finish: ${a.error}` : "It did not finish.";
    case "cancelled":
      return "It was stopped before it finished.";
    case "needs_reconciliation":
      return "It stopped partway and needs a look.";
  }
  if (a.type === "outcome") return "Done.";
  return undefined;
}

/** A list's rows to show at first, and how many more there are. */
export function firstRows<T>(rows: T[], n = 10): { shown: T[]; more: number } {
  return { shown: rows.slice(0, n), more: Math.max(0, rows.length - n) };
}

/** A button's inputs as a form's first values (only what fits a field). */
export function buttonValues(b: ToolButton): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(b.inputs ?? {}))
    if (v !== null && v !== undefined) out[k] = v;
  return out;
}

/** "Amount: 4500 · What for: Diesel": the values in a line. */
export const valuesLine = (values: ToolValue[]) =>
  values.map((v) => `${v.label}: ${v.value}`).join(" · ");

/** Why a tool could not start, in plain words. */
export function startErrorWords(e: unknown): string {
  if (!(e instanceof ApiError))
    return e instanceof Error ? e.message : "Something went wrong. Try again.";
  const first = (s: string) => s.charAt(0).toUpperCase() + s.slice(1);
  switch (e.status) {
    case 0:
      return "You are offline. Try again when you are back online.";
    case 403:
      return e.message
        ? `This tool is not yours to use: ${e.message}.`
        : "This tool is not yours to use. Ask an owner if you need it.";
    case 404:
      return "This tool is not there any more. It may have been turned off or renamed.";
    case 409:
      return first(e.message || "That changed meanwhile. Start the tool again.");
    case 422: {
      const why = e.problems
        .map((p) => (typeof p === "string" ? p : (p.plain ?? p.message)))
        .filter(Boolean);
      return why.length
        ? `Some answers do not fit: ${why.join("; ")}.`
        : "Some answers do not fit. Check them and try again.";
    }
    case 429:
      return `Not started: ${e.message || "too many runs just now. Try again later."}`;
  }
  return first(e.message || "Something went wrong. Try again.");
}

/** What a tool does before it runs, in words. */
export function careWords(level?: string): string {
  switch (level) {
    case "answers":
      return "Answers at once";
    case "yes":
      return "Asks yes first";
    case "step_up":
      return "Asks for your passkey";
  }
  return "";
}
