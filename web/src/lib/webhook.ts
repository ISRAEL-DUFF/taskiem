// Webhook trigger settings (wd-v1 webhook triggers) as the editor changes them.
import type { JSONSchema } from "../api";

export const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE"];

/** The methods with m turned on or off, in a fixed order; undefined is POST alone, the default. */
export function withMethod(methods: string[], m: string, on: boolean): string[] | undefined {
  const next = METHODS.filter((x) => (x === m ? on : methods.includes(x)));
  return next.length === 0 || (next.length === 1 && next[0] === "POST") ? undefined : next;
}

/** Origins typed separated by commas. */
export function parseOrigins(text: string): string[] {
  return [...new Set(text.split(",").map((s) => s.trim()).filter(Boolean))];
}

/** A JSON Schema describing a sample value: its types, nothing required. */
export function schemaOf(v: unknown): JSONSchema {
  if (Array.isArray(v)) return v.length ? { type: "array", items: schemaOf(v[0]) } : { type: "array" };
  if (v === null) return {};
  switch (typeof v) {
    case "object":
      return { type: "object", properties: Object.fromEntries(Object.entries(v as Record<string, unknown>).map(([k, x]) => [k, schemaOf(x)])) };
    case "number":
      return { type: Number.isInteger(v) ? "integer" : "number" };
    case "boolean":
      return { type: "boolean" };
    default:
      return { type: "string" };
  }
}
