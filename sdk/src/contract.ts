// Small helpers that implement wd/v1 contract rules (docs/contracts/wd-v1.md).
import type { Expression } from "./wd.js";

/** Reports whether a value is a CEL expression in the WD sense. */
export function isExpression(v: unknown): v is Expression {
  return typeof v === "string" && v.length > 1 && v.startsWith("=");
}

const unitMs = { ms: 1, s: 1_000, m: 60_000, h: 3_600_000, d: 86_400_000 } as const;
const durationRe = /^([0-9]+(ms|s|m|h|d))+$/;
const partRe = /([0-9]+)(ms|s|m|h|d)/g;

/** Parses a WD duration such as "90s" or "1h30m" into milliseconds. */
export function parseDuration(s: string): number {
  if (!durationRe.test(s)) {
    throw new Error(`invalid duration ${JSON.stringify(s)}: expected <n><unit>, units ms s m h d`);
  }
  let total = 0;
  for (const [, n, unit] of s.matchAll(partRe)) {
    total += Number(n) * unitMs[unit as keyof typeof unitMs];
  }
  return total;
}

const stepIdRe = /^[a-z][a-z0-9_]{0,62}$/;

/** Reports whether s is a valid step id. */
export function isStepId(s: string): boolean {
  return stepIdRe.test(s);
}
