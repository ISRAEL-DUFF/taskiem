// The canvas in plain words (no-technical-steps N8): a schedule said as
// "every Friday at 10:00" instead of cron, a value "from an earlier step"
// instead of an expression, and "run only when" as a list of rules. Each
// turns into the definition's cron or expression and is read back from
// it; what cannot be read back is left to Advanced.
import type { JSONSchema } from "../api";

// --- Schedules ---

export type Every = "hour" | "day" | "weekdays" | "week" | "month";

export interface Schedule {
  every: Every;
  /** HH:MM, for every but hour. */
  time: string;
  /** For an hourly schedule, the minute past the hour. */
  minute: number;
  /** For a weekly one, 0 (Sunday) to 6. */
  weekday: number;
  /** For a monthly one, 1 to 28. */
  day: number;
}

export const WEEKDAY_NAMES = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

export const DEFAULT_SCHEDULE: Schedule = { every: "weekdays", time: "09:00", minute: 0, weekday: 1, day: 1 };

const num = (s: string, lo: number, hi: number): number | null => {
  if (!/^\d{1,2}$/.test(s)) return null;
  const n = Number(s);
  return n >= lo && n <= hi ? n : null;
};

const pad = (n: number) => String(n).padStart(2, "0");

/** The cron a schedule is. */
export function cronOf(s: Schedule): string {
  const [h, m] = s.time.split(":").map(Number);
  switch (s.every) {
    case "hour":
      return `${s.minute} * * * *`;
    case "day":
      return `${m} ${h} * * *`;
    case "weekdays":
      return `${m} ${h} * * 1-5`;
    case "week":
      return `${m} ${h} * * ${s.weekday}`;
    case "month":
      return `${m} ${h} ${s.day} * *`;
  }
}

/** The schedule a cron is, or null when it is not one the picker says. */
export function scheduleOf(cron: string): Schedule | null {
  const f = cron.trim().split(/\s+/);
  if (f.length !== 5) return null;
  const [mi, ho, dom, mon, dow] = f as [string, string, string, string, string];
  if (mon !== "*") return null;
  const minute = num(mi, 0, 59);
  if (minute === null) return null;
  if (ho === "*" && dom === "*" && dow === "*") return { ...DEFAULT_SCHEDULE, every: "hour", minute };
  const hour = num(ho, 0, 23);
  if (hour === null) return null;
  const time = `${pad(hour)}:${pad(minute)}`;
  if (dom === "*" && dow === "*") return { ...DEFAULT_SCHEDULE, every: "day", time };
  if (dom === "*" && dow === "1-5") return { ...DEFAULT_SCHEDULE, every: "weekdays", time };
  if (dom === "*") {
    const weekday = num(dow, 0, 7);
    return weekday === null ? null : { ...DEFAULT_SCHEDULE, every: "week", time, weekday: weekday % 7 };
  }
  if (dow === "*") {
    const day = num(dom, 1, 28);
    return day === null ? null : { ...DEFAULT_SCHEDULE, every: "month", time, day };
  }
  return null;
}

/** A schedule in words: "Every Friday at 10:00". */
export function scheduleWords(s: Schedule): string {
  switch (s.every) {
    case "hour":
      return s.minute === 0 ? "Every hour, on the hour" : `Every hour, at ${s.minute} minutes past`;
    case "day":
      return `Every day at ${s.time}`;
    case "weekdays":
      return `Every weekday (Monday to Friday) at ${s.time}`;
    case "week":
      return `Every ${WEEKDAY_NAMES[s.weekday]} at ${s.time}`;
    case "month":
      return `On day ${s.day} of every month at ${s.time}`;
  }
}

// --- Values from earlier steps ---

/** A value a field can take from what came before: its words and its expression. */
export interface Source {
  label: string;
  expr: string;
  /** Whether it is a number, for the rules that compare amounts. */
  number?: boolean;
}

interface StepLike {
  id: string;
  type: string;
  name?: string;
  needs?: string[];
  connector?: string;
  action?: string;
  template?: string;
  config?: unknown;
}

/** A callable template's outputs, as the template list gives them. */
interface CallableLike {
  id: string;
  callable?: { outputs: { name: string; type: string; title: string }[] };
}

interface DefLike {
  trigger?: { type: string };
  inputs?: { schema?: unknown };
  steps: StepLike[];
}

interface ConnectorLike {
  ref: string;
  actions: Record<string, { output?: JSONSchema }>;
}

const words = (s: string) => s.replace(/_/g, " ");
const stepWords = (s: StepLike) => s.name || words(s.id);
const isNumber = (s?: JSONSchema) => s?.type === "number" || s?.type === "integer";

/** The steps a step comes after: those it needs, and theirs. */
export function earlierSteps(steps: StepLike[], id: string): StepLike[] {
  const byId = new Map(steps.map((s) => [s.id, s]));
  const seen = new Set<string>();
  const walk = (sid: string) => {
    for (const n of byId.get(sid)?.needs ?? []) {
      if (!seen.has(n)) {
        seen.add(n);
        walk(n);
      }
    }
  };
  walk(id);
  return steps.filter((s) => seen.has(s.id));
}

/** The values a step can use: what started the run and what the steps before it gave. */
export function sourcesFor(def: DefLike, id: string, connectors: ConnectorLike[], templates: CallableLike[] = []): Source[] {
  const out: Source[] = [];
  const props = (def.inputs?.schema as JSONSchema | undefined)?.properties ?? {};
  for (const [k, s] of Object.entries(props)) {
    out.push({ label: `What was filled in: ${s.title ?? words(k)}`, expr: `=trigger.body.${k}`, number: isNumber(s) });
  }
  for (const s of earlierSteps(def.steps, id)) {
    const from = `From “${stepWords(s)}”`;
    if (s.type === "approval") {
      out.push({ label: `${from}: the decision (approved or rejected)`, expr: `=steps.${s.id}.output.decision` });
    } else if (s.type === "form") {
      const fields = ((s.config as { schema?: JSONSchema } | undefined)?.schema?.properties ?? {}) as Record<string, JSONSchema>;
      for (const [k, f] of Object.entries(fields)) {
        out.push({ label: `${from}: ${f.title ?? words(k)}`, expr: `=steps.${s.id}.output.${k}`, number: isNumber(f) });
      }
    } else if (s.type === "transform") {
      const o = (s.config as { output?: unknown } | undefined)?.output;
      if (o && typeof o === "object" && !Array.isArray(o)) {
        for (const k of Object.keys(o)) out.push({ label: `${from}: ${words(k)}`, expr: `=steps.${s.id}.output.${k}` });
      }
    } else if (s.type === "template") {
      for (const o of templates.find((t) => t.id === s.template)?.callable?.outputs ?? []) {
        out.push({ label: `${from}: ${o.title}`, expr: `=steps.${s.id}.output.${o.name}`, number: o.type === "integer" || o.type === "number" });
      }
    } else if (s.type === "connector") {
      const output = connectors.find((c) => c.ref === s.connector)?.actions[s.action ?? ""]?.output;
      for (const [k, p] of Object.entries(output?.properties ?? {})) {
        out.push({ label: `${from}: ${p.title ?? words(k)}`, expr: `=steps.${s.id}.output.${k}`, number: isNumber(p) });
      }
    }
  }
  return out;
}

/** The source a value is, when it is one. */
export function sourceOf(sources: Source[], value: unknown): Source | undefined {
  return typeof value === "string" ? sources.find((s) => s.expr === value) : undefined;
}

// --- Rules ("run only when") ---

export type Op = "is" | "is not" | "more than" | "less than" | "at least" | "at most" | "contains";

export const OPS: Op[] = ["is", "is not", "more than", "less than", "at least", "at most", "contains"];

const CEL: Record<Exclude<Op, "contains">, string> = { is: "==", "is not": "!=", "more than": ">", "less than": "<", "at least": ">=", "at most": "<=" };

export interface Rule {
  /** A source's expression ("=steps.approve.output.decision"). */
  left: string;
  op: Op;
  right: string;
}

export interface Rules {
  rules: Rule[];
  /** Whether every rule must hold (and), or any one (or). */
  all: boolean;
}

const literal = (s: string): string => {
  const t = s.trim();
  if (/^-?\d+(\.\d+)?$/.test(t) || t === "true" || t === "false") return t;
  return `'${t.replace(/\\/g, "\\\\").replace(/'/g, "\\'")}'`;
};

const unliteral = (s: string): string | null => {
  const t = s.trim();
  if (/^-?\d+(\.\d+)?$/.test(t) || t === "true" || t === "false") return t;
  const m = /^'((?:[^'\\]|\\.)*)'$/.exec(t);
  return m ? (m[1] ?? "").replace(/\\(.)/g, "$1") : null;
};

/** The expression rules are ("" for none). */
export function rulesExpr(r: Rules): string {
  const parts = r.rules
    .filter((x) => x.left)
    .map((x) => {
      const left = x.left.replace(/^=/, "");
      return x.op === "contains" ? `${left}.contains(${literal(x.right)})` : `${left} ${CEL[x.op]} ${literal(x.right)}`;
    });
  return parts.length ? "=" + parts.join(r.all ? " && " : " || ") : "";
}

const PATH = /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$/;
const COMPARE = /^(.+?)\s*(==|!=|>=|<=|>|<)\s*(.+)$/;

/** The rules an expression is, or null when it is not one the builder says. */
export function rulesOf(expr: string): Rules | null {
  const e = expr.trim().replace(/^=/, "").trim();
  if (e === "") return { rules: [], all: true };
  const hasAnd = e.includes("&&");
  const hasOr = e.includes("||");
  if (hasAnd && hasOr) return null;
  const rules: Rule[] = [];
  for (const part of e.split(hasOr ? "||" : "&&")) {
    const p = part.trim();
    const c = /^(.+)\.contains\((.+)\)$/.exec(p);
    if (c) {
      const [, path = "", lit = ""] = c;
      const right = unliteral(lit);
      if (!PATH.test(path) || right === null) return null;
      rules.push({ left: "=" + path, op: "contains", right });
      continue;
    }
    const m = COMPARE.exec(p);
    if (!m) return null;
    const [, path = "", cmp = "", lit = ""] = m;
    const right = unliteral(lit);
    const op = (Object.keys(CEL) as (keyof typeof CEL)[]).find((k) => CEL[k] === cmp);
    if (!PATH.test(path) || right === null || !op) return null;
    rules.push({ left: "=" + path, op, right });
  }
  return { rules, all: !hasOr };
}

// --- Durations ---

/** Waits and time limits people pick from. */
export const DURATIONS: [string, string][] = [
  ["15m", "15 minutes"],
  ["30m", "30 minutes"],
  ["1h", "1 hour"],
  ["2h", "2 hours"],
  ["6h", "6 hours"],
  ["12h", "12 hours"],
  ["24h", "1 day"],
  ["48h", "2 days"],
  ["72h", "3 days"],
  ["168h", "1 week"],
];

/** A duration in words, or the duration itself when it is not listed. */
export function durationWords(d: string): string {
  return DURATIONS.find(([v]) => v === d)?.[1] ?? d;
}
