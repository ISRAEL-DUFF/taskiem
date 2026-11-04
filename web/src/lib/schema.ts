// Field kinds for forms generated from JSON Schema (connector action
// inputs, workflow inputs). Values may be literals or "=..." expressions.
import type { JSONSchema } from "../api";

export type FieldKind = "string" | "number" | "integer" | "boolean" | "enum" | "object" | "json";

export function kindOf(s: JSONSchema | undefined): FieldKind {
  if (!s) return "json";
  if (s.enum) return "enum";
  const t = Array.isArray(s.type) ? s.type.find((x) => x !== "null") : s.type;
  switch (t) {
    case "string":
    case "number":
    case "integer":
    case "boolean":
      return t;
    case "object":
      return s.properties ? "object" : "json";
  }
  return "json";
}

export const isExpression = (v: unknown): v is string => typeof v === "string" && v.startsWith("=") && v.length > 1;

/** Parses a form field's text into the value the schema wants. */
export function coerce(kind: FieldKind, text: string): unknown {
  if (text.startsWith("=")) return text;
  switch (kind) {
    case "number":
    case "integer": {
      if (text.trim() === "") return undefined;
      const n = Number(text);
      return Number.isFinite(n) && (kind === "number" || Number.isInteger(n)) ? n : text;
    }
    case "boolean":
      return text === "true";
    case "json":
      try {
        return text.trim() === "" ? undefined : JSON.parse(text);
      } catch {
        return text;
      }
  }
  return text === "" ? undefined : text;
}

/** Sets or removes key in obj, returning a new object. */
export function setKey(obj: Record<string, unknown> | undefined, key: string, value: unknown): Record<string, unknown> {
  return merge(obj, { [key]: value });
}

/** obj with patch applied; keys the patch sets to undefined or "" are removed. */
export function merge<T extends object>(obj: T | undefined, patch: Record<string, unknown>): T {
  const blank = (v: unknown) => v === undefined || v === "";
  return Object.fromEntries(Object.entries({ ...(obj ?? {}), ...patch }).filter(([k, v]) => !(k in patch && blank(v)))) as T;
}
