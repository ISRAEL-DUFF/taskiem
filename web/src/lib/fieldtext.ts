// Text with fields in it ("Hello [Customer name], thank you for order
// [Reference]"): what the builder's message boxes edit, kept in the
// definition as a fixed string when it has no fields, a field's own
// expression when it is just one field, and otherwise one expression that
// joins the text and the fields: ='Hello ' + string(trigger.body.name).
// Read back from those same shapes; anything else was set up by hand.

export type Segment = { text: string } | { expr: string; label: string };

const PATH = /^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*|\[\d+\])*$/;
// An amount kept in kobo, shown in naira: double(trigger.fields.amount_kobo) / 100.0.
const NAIRA = /^double\(([^()]+)\) \/ 100\.0$/;

/** Whether an expression (without its =) is one a field can be: a path, or a kobo path in naira. */
export const isField = (e: string) => PATH.test(e) || PATH.test(NAIRA.exec(e)?.[1] ?? "");

const quote = (s: string) => `'${s.replace(/\\/g, "\\\\").replace(/'/g, "\\'").replace(/\n/g, "\\n")}'`;

/** The value text with fields is ("" text and no fields: undefined). */
export function valueOf(segs: Segment[]): string | undefined {
  const parts = segs.filter((s) => ("text" in s ? s.text !== "" : true));
  if (parts.length === 0) return undefined;
  if (parts.every((s) => "text" in s)) return parts.map((s) => (s as { text: string }).text).join("");
  const only = parts.length === 1 ? parts[0] : undefined;
  if (only && "expr" in only) return only.expr;
  return "=" + parts.map((s) => ("text" in s ? quote(s.text) : `string(${s.expr.replace(/^=/, "")})`)).join(" + ");
}

/** Splits an expression's top level on +, outside quotes and brackets. */
function topLevel(e: string): string[] | null {
  const out: string[] = [];
  let depth = 0;
  let quoteCh = "";
  let cur = "";
  for (let i = 0; i < e.length; i++) {
    const c = e[i] as string;
    if (quoteCh) {
      cur += c;
      if (c === "\\") {
        cur += e[++i] ?? "";
      } else if (c === quoteCh) quoteCh = "";
      continue;
    }
    if (c === "'" || c === '"') quoteCh = c;
    else if (c === "(" || c === "[") depth++;
    else if (c === ")" || c === "]") depth--;
    if (c === "+" && depth === 0) {
      out.push(cur.trim());
      cur = "";
      continue;
    }
    cur += c;
  }
  if (quoteCh || depth !== 0) return null;
  out.push(cur.trim());
  return out;
}

function unquote(s: string): string | null {
  const m = /^(['"])((?:[^\\]|\\.)*)\1$/s.exec(s);
  if (!m) return null;
  return (m[2] ?? "").replace(/\\(.)/g, (_, ch: string) => (ch === "n" ? "\n" : ch));
}

/** The text and fields a value is, or null when it was set up by hand.
 * label names a field by its expression ("=trigger.body.name"). */
export function segmentsOf(value: unknown, label: (expr: string) => string): Segment[] | null {
  if (value === undefined || value === null || value === "") return [];
  if (typeof value === "number" || typeof value === "boolean") return [{ text: String(value) }];
  if (typeof value !== "string") return null;
  if (!value.startsWith("=")) return [{ text: value }];
  const parts = topLevel(value.slice(1).trim());
  if (!parts) return null;
  const out: Segment[] = [];
  for (const p of parts) {
    const lit = unquote(p);
    if (lit !== null) {
      out.push({ text: lit });
      continue;
    }
    const inner = /^string\((.+)\)$/.exec(p)?.[1]?.trim() ?? p;
    if (!isField(inner)) return null;
    out.push({ expr: "=" + inner, label: label("=" + inner) });
  }
  return out;
}

/** A field's words when no list names it: the last part of its path. */
export function fieldWords(expr: string): string {
  const last = expr.replace(/^=/, "").split(".").pop() ?? expr;
  return (last.charAt(0).toUpperCase() + last.slice(1)).replaceAll("_", " ");
}
