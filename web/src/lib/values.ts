// Plain inputs (no-technical-steps N5): what a person types, kept as they
// mean it, and shown back without JSON.
import type { JSONSchema } from "../api";

/** A Nigerian phone number in any common form, as +234…; null when it is not one. */
export function phoneOf(text: string): string | null {
  const digits = text.replace(/[\s\-().]/g, "");
  let m = /^\+?234(\d{10})$/.exec(digits);
  if (m) return `+234${m[1] ?? ""}`;
  m = /^0([789][01]\d{8})$/.exec(digits);
  if (m) return `+234${m[1] ?? ""}`;
  return null;
}

/**
 * What a typed value means: true or false; a phone number (kept as text,
 * +234…); a plain number (not one with a leading zero, which is an id or a
 * phone); otherwise the text as typed, without quotes.
 */
export function plainValue(text: string): unknown {
  const t = text.trim();
  if (/^(true|false)$/i.test(t)) return t.toLowerCase() === "true";
  const phone = phoneOf(t);
  if (phone) return phone;
  if (/^-?(0|[1-9]\d{0,14})(\.\d+)?$/.test(t)) return Number(t);
  return t;
}

/** A stored value as a person reads it: text without quotes, JSON only for structures. */
export function showValue(v: unknown): string {
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  if (v === null || v === undefined) return "";
  return JSON.stringify(v);
}

/** A workflow's inputs schema with its local $refs (#/types/…) resolved, for a form. */
export function resolveSchema(schema: JSONSchema | undefined, types: Record<string, JSONSchema> = {}, depth = 0): JSONSchema | undefined {
  if (!schema || depth > 8) return schema;
  const ref = (schema as { $ref?: string }).$ref;
  if (ref?.startsWith("#/types/")) return resolveSchema(types[ref.slice("#/types/".length)], types, depth + 1);
  if (!schema.properties) return schema;
  const props: Record<string, JSONSchema> = {};
  for (const [k, s] of Object.entries(schema.properties)) {
    const r = resolveSchema(s, types, depth + 1);
    if (r) props[k] = r;
  }
  return { ...schema, properties: props };
}
