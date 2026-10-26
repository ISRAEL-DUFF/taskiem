// What a step can use from what came before, wherever it is: the steps
// before it in its own list, and, inside a choice's path or a for each,
// the steps before that (no-technical-steps N8, now at every depth). With
// a test's result, each value's sample too, for "What came in".
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { earlierSteps, sourcesFor, type Source } from "../lib/plaincanvas";
import { listAt, locate } from "../lib/flow";

/** The steps a step comes after, at its depth and every depth above it. */
export function earlierAnywhere(steps: Step[], id: string): Step[] {
  const at = locate(steps, id);
  if (!at) return [];
  const here = earlierSteps(listAt(steps, at.list) as Parameters<typeof earlierSteps>[0], id) as Step[];
  if (at.list === "") return here;
  const owner = at.list.split("/")[0] ?? "";
  return [...earlierAnywhere(steps, owner), ...here];
}

/** Whether a step is inside a for each (it can use the item). */
export function inForeach(steps: Step[], id: string): boolean {
  let at = locate(steps, id);
  while (at && at.list !== "") {
    if (at.list.endsWith("/each")) return true;
    at = locate(steps, at.list.split("/")[0] ?? "");
  }
  return false;
}

type Connectors = Parameters<typeof sourcesFor>[2];
type Callables = Parameters<typeof sourcesFor>[3];

/** The values a step can use, by their words. */
export function sourcesAnywhere(def: Pick<WorkflowDefinition, "trigger" | "inputs" | "steps">, id: string, connectors: Connectors, callables: Callables = []): Source[] {
  const earlier = earlierAnywhere(def.steps, id);
  const flat = [...earlier, { id, type: "transform", needs: earlier.map((s) => s.id) }];
  const out = sourcesFor({ trigger: def.trigger, inputs: def.inputs, steps: flat as Parameters<typeof sourcesFor>[0]["steps"] }, id, connectors, callables);
  return inForeach(def.steps, id) ? [{ label: "This item", expr: "=item" }, ...out] : out;
}

// A payment's fields, before a test shows them (schemas/capabilities-v1.json).
const PAYMENT_FIELDS = ["payment_id", "amount_kobo", "currency", "paid_at"];

const fieldWords = (k: string) => (k.charAt(0).toUpperCase() + k.slice(1)).replaceAll("_", " ");

/** A trigger field as a value: an amount in kobo is offered in naira. */
const fieldSource = (group: "fields" | "body", k: string): Source =>
  k.endsWith("_kobo")
    ? { label: `What started it: ${fieldWords(k.replace(/_kobo$/, ""))} (₦)`, expr: `=double(trigger.${group}.${k}) / 100.0`, number: true }
    : { label: `What started it: ${fieldWords(k)}`, expr: `=trigger.${group}.${k}`, number: /amount|count/.test(k) };

/** What started the run, when no form's questions say: a payment's fields, or what a test's sample held. */
export function withStarted(sources: Source[], trigger: { type: string } | undefined, result?: { trigger?: unknown }): Source[] {
  if (sources.some((s) => s.expr.startsWith("=trigger."))) return sources;
  const sample = (result?.trigger ?? {}) as { body?: unknown; fields?: unknown };
  const keysOf = (v: unknown) => (v && typeof v === "object" && !Array.isArray(v) ? Object.keys(v) : []);
  const fields = keysOf(sample.fields).length ? keysOf(sample.fields) : trigger?.type === "capability_event" ? PAYMENT_FIELDS : [];
  const started = [...fields.map((k) => fieldSource("fields", k)), ...keysOf(sample.body).map((k) => fieldSource("body", k))];
  const item = sources[0]?.expr === "=item" ? [sources[0]] : [];
  return [...item, ...started, ...sources.slice(item.length)];
}

/** A value's sample in a test's result, by its expression (=trigger.body.x, =steps.s.output.y). */
export function valueAt(result: { trigger?: unknown; outputs: Record<string, unknown> } | undefined, expr: string): unknown {
  if (!result) return undefined;
  const naira = /^=double\(([^()]+)\) \/ 100\.0$/.exec(expr);
  if (naira) {
    const kobo = valueAt(result, `=${naira[1] ?? ""}`);
    return typeof kobo === "number" ? kobo / 100 : undefined;
  }
  const parts = expr.replace(/^=/, "").split(".");
  let cur: unknown;
  if (parts[0] === "trigger") {
    cur = result.trigger;
    parts.shift();
  } else if (parts[0] === "steps" && parts.length >= 3 && parts[2] === "output") {
    cur = result.outputs[parts[1] ?? ""];
    parts.splice(0, 3);
  } else return undefined;
  for (const p of parts) {
    if (cur === null || typeof cur !== "object") return undefined;
    cur = (cur as Record<string, unknown>)[p];
  }
  return cur;
}

/** A sample in a few words for a row ("Adaeze Okafor", "₦25,000"-like numbers stay numbers). */
export function sampleWords(v: unknown): string {
  if (v === undefined) return "";
  if (v === null) return "nothing";
  if (typeof v === "string") return v.length > 60 ? v.slice(0, 57) + "…" : v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (Array.isArray(v)) return `${v.length} ${v.length === 1 ? "item" : "items"}`;
  const n = Object.keys(v).length;
  return `${n} ${n === 1 ? "field" : "fields"}`;
}
