// Data tables (docs/data-tables.md): the column types, the checks the
// page runs before calling the API, and CSV parsing and serialising for
// the import picker and the grid.

export const COLUMN_TYPES = ["text", "integer", "number", "boolean", "date", "datetime", "phone", "email", "json"] as const;
export type ColumnType = (typeof COLUMN_TYPES)[number];

/** What each type holds, for the columns builder. */
export const TYPE_HELP: Record<ColumnType, string> = {
  text: "Words and names",
  integer: "A whole number (a count, an amount in kobo)",
  number: "A number with decimals",
  boolean: "Yes or no",
  date: "A day, as 2026-11-01",
  datetime: "A date and time",
  phone: "A phone number with the country code (personal data)",
  email: "An email address (personal data)",
  json: "Structured data",
};

export interface Column {
  name: string;
  title: string;
  type: ColumnType;
  required: boolean;
  personal: boolean;
}

export interface Table {
  id: string;
  name: string;
  title: string;
  description: string;
  columns: Column[];
  postgres_table: string;
  connection: string;
  row_count?: number;
  /** The Taskiem feature that keeps the table (money_log), or null: a kept table is read-only here. */
  kept_by?: string | null;
  created_by: string;
  created_at: string;
  updated_at: string;
}

export interface TablesStatus {
  available: boolean;
  configured: boolean;
  status: "none" | "creating" | "enabling" | "ready" | "failed";
  own: boolean;
  connection: string;
  tables: number;
  project_name?: string;
  project_id?: string;
  project_ref?: string;
  api_url?: string;
  token_expires_at?: string;
  error?: string;
}

export type Row = Record<string, unknown>;

const NAME = /^[a-z][a-z0-9_]{0,62}$/;
const RESERVED = ["id", "created_at", "updated_at"];

/** Why a table or column name is not allowed, or null. */
export function nameError(name: string): string | null {
  if (!NAME.test(name)) return "lower-case letters, digits and underscores, starting with a letter (at most 63 characters)";
  if (RESERVED.includes(name) || name.startsWith("xb_") || name.startsWith("pg_")) return `${name} is reserved`;
  return null;
}

/** Whether the type holds personal data unless the column says otherwise. */
export const personalByDefault = (type: ColumnType) => type === "phone" || type === "email";

/** A blank column for the builder. */
export function newColumn(): Column {
  return { name: "", title: "", type: "text", required: false, personal: false };
}

/** The problems with a set of columns, by index (-1 for the set itself). */
export function columnErrors(columns: Column[], limit = 0): { index: number; message: string }[] {
  const out: { index: number; message: string }[] = [];
  if (columns.length === 0) out.push({ index: -1, message: "A table needs at least one column." });
  if (limit > 0 && columns.length > limit) out.push({ index: -1, message: `A table may have at most ${limit} columns.` });
  const seen = new Set<string>();
  columns.forEach((c, i) => {
    const bad = nameError(c.name);
    if (bad) out.push({ index: i, message: bad });
    else if (seen.has(c.name)) out.push({ index: i, message: `${c.name} is listed twice` });
    seen.add(c.name);
    if (!COLUMN_TYPES.includes(c.type)) out.push({ index: i, message: `unknown type ${c.type}` });
  });
  return out;
}

/** The value as typed in a cell, turned into what the API takes; "" is null for every type but text. */
export function cellValue(type: ColumnType, text: string): unknown {
  if (type === "text") return text;
  const t = text.trim();
  if (t === "") return null;
  switch (type) {
    case "integer":
    case "number":
      return Number.isFinite(Number(t)) ? Number(t) : t;
    case "boolean":
      if (["true", "yes", "1", "y"].includes(t.toLowerCase())) return true;
      if (["false", "no", "0", "n"].includes(t.toLowerCase())) return false;
      return t;
    case "json":
      try {
        return JSON.parse(t) as unknown;
      } catch {
        return t;
      }
    default:
      return t;
  }
}

/** A value as the grid and a CSV show it. */
export function cellText(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  return JSON.stringify(v);
}

/** Parses CSV text (RFC 4180: quoted cells, doubled quotes, newlines inside quotes) into a header and rows of cells. */
export function parseCSV(text: string): { header: string[]; rows: string[][] } {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  const src = text.startsWith("﻿") ? text.slice(1) : text;
  for (let i = 0; i < src.length; i++) {
    const ch = src[i];
    if (quoted) {
      if (ch === '"') {
        if (src[i + 1] === '"') {
          cell += '"';
          i++;
        } else quoted = false;
      } else cell += ch;
      continue;
    }
    if (ch === '"') quoted = true;
    else if (ch === ",") {
      row.push(cell);
      cell = "";
    } else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && src[i + 1] === "\n") i++;
      row.push(cell);
      rows.push(row);
      row = [];
      cell = "";
    } else cell += ch;
  }
  if (cell !== "" || row.length > 0) {
    row.push(cell);
    rows.push(row);
  }
  const header = (rows.shift() ?? []).map((h) => h.trim());
  return { header, rows: rows.filter((r) => r.some((c) => c.trim() !== "")) };
}

/** One CSV cell, quoted when it needs to be. */
export function csvCell(v: unknown): string {
  const s = cellText(v);
  return /[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
}

/** Rows as CSV with a header of the given columns. */
export function toCSV(columns: string[], rows: Row[]): string {
  const lines = [columns.map(csvCell).join(",")];
  for (const r of rows) lines.push(columns.map((c) => csvCell(r[c])).join(","));
  return lines.join("\r\n") + "\r\n";
}

/** A header or column name as matched: lower case, letters and digits only ("Phone Number" is phone_number), as the server matches. */
export const headerKey = (s: string) => s.toLowerCase().replace(/[^\p{L}\p{N}]/gu, "");

/** Each header's column, by name or title loosely (no-technical-steps N5); the headers that match none. */
export function matchHeaders(header: string[], columns: Column[]): { matched: Record<string, string>; unmatched: string[] } {
  const byKey = new Map<string, string>();
  for (const r of RESERVED) byKey.set(headerKey(r), r);
  for (const c of columns) byKey.set(headerKey(c.name), c.name);
  for (const c of columns) if (c.title && !byKey.has(headerKey(c.title))) byKey.set(headerKey(c.title), c.name);
  const matched: Record<string, string> = {};
  const unmatched: string[] = [];
  for (const h of header) {
    const c = byKey.get(headerKey(h));
    if (c) matched[h] = c;
    else unmatched.push(h);
  }
  return { matched, unmatched };
}

/** Checks a CSV's header against the table once the person has matched the rest (mapping: header to column, "" to leave out). */
export function importError(header: string[], columns: Column[], mapping: Record<string, string> = {}): string | null {
  if (header.length === 0) return "The file has no header row.";
  const { matched, unmatched } = matchHeaders(header, columns);
  const left = unmatched.filter((h) => !(h in mapping));
  if (left.length > 0) return `Match ${left.map((h) => `“${h}”`).join(", ")} to a column of the table, or leave it out.`;
  const used = [...Object.values(matched), ...Object.values(mapping)].filter((c) => c && !RESERVED.includes(c));
  if (used.length === 0) return "The file has none of the table's columns.";
  return null;
}

/** A table to make from a file's header (Make a table from this file): a column per header, typed from its values. */
export function tableFromCSV(name: string, header: string[], rows: string[][]): { name: string; title: string; columns: { name: string; title: string; type: string }[] } {
  const snake = (s: string) => s.trim().toLowerCase().replace(/[^a-z0-9]+/g, "_").replace(/^_+|_+$/g, "").replace(/^([0-9])/, "c_$1").slice(0, 63) || "column";
  const seen = new Set<string>();
  const columns = header
    .filter((h) => !RESERVED.includes(headerKey(h)) && !["createdat", "updatedat"].includes(headerKey(h)))
    .map((h, i) => {
      let n = snake(h);
      while (seen.has(n)) n = `${n}_${i}`;
      seen.add(n);
      const values = rows.map((r) => (r[header.indexOf(h)] ?? "").trim()).filter(Boolean);
      const type =
        values.length > 0 && values.every((v) => /^-?\d+(\.\d+)?$/.test(v.replace(/[,₦]/g, "")) && !/^0\d/.test(v))
          ? "number"
          : values.length > 0 && values.every((v) => /^(\+?234|0)[789][01]\d{8}$/.test(v.replace(/[\s-]/g, "")))
            ? "phone"
            : values.length > 0 && values.every((v) => /^\d{4}-\d{2}-\d{2}$/.test(v))
              ? "date"
              : "text";
      return { name: n, title: h.trim(), type };
    });
  return { name: snake(name), title: name.trim(), columns };
}

/** The wd/v1 step that reads the table, for the "Use in a workflow" panel. */
export function exampleStep(table: Table): string {
  const filter = table.columns.find((c) => c.type === "integer" || c.type === "number");
  const filters = filter ? `, "filters": [{"column": "${filter.name}", "op": "gt", "value": 0}]` : "";
  return `{"id": "read_${table.name}", "type": "connector", "connector": "xenibase@1", "action": "read_rows",
 "connection": "${table.connection}", "input": {"table": "${table.name}"${filters}, "limit": 1000}}`;
}

/** What the table page says of a kept table (docs/data-tables.md#kept-tables). */
export const KEPT_MESSAGE = "This table is kept by Taskiem: rows are added by Taskiem and cannot be changed or deleted. Corrections are new rows.";

/** The note for a business that brought its own Xenibase project, where a kept table can still be edited. */
export const KEPT_OWN_NOTE = "Your organisation uses its own Xenibase project, so the table can still be edited there, outside Taskiem.";

/** Whether Taskiem keeps the table. */
export const isKept = (table: Pick<Table, "kept_by">) => !!table.kept_by;

/** What the table page offers: a kept table only reads, searches and exports, whoever looks. */
export function tableControls(table: Pick<Table, "kept_by">, canEdit: boolean): { addRow: boolean; editRows: boolean; importCSV: boolean; columns: boolean; deleteTable: boolean; export: boolean; search: boolean } {
  const edit = canEdit && !isKept(table);
  return { addRow: edit, editRows: edit, importCSV: edit, columns: edit, deleteTable: edit, export: true, search: true };
}

/** The kept table's notice: the sentence, and the own-project note when the business brought its own project; null for an ordinary table. */
export function keptNotice(table: Pick<Table, "kept_by">, status?: Pick<TablesStatus, "own"> | null): string[] | null {
  if (!isKept(table)) return null;
  return status?.own ? [KEPT_MESSAGE, KEPT_OWN_NOTE] : [KEPT_MESSAGE];
}

/** The xenibase@1 actions a workflow uses on a table: only reads for a kept one. */
export function tableActions(table: Pick<Table, "kept_by">): { action: string; what: string }[] {
  return isKept(table) ? TABLE_ACTIONS.filter((a) => a.action === "read_rows" || a.action.startsWith("row_changed")) : TABLE_ACTIONS;
}

/** The xenibase@1 actions a workflow uses on a table. */
export const TABLE_ACTIONS: { action: string; what: string }[] = [
  { action: "read_rows", what: "a page of rows by filters and order" },
  { action: "insert_rows", what: "add rows" },
  { action: "upsert_rows", what: "add or update rows by a unique column" },
  { action: "update_rows", what: "change one row by id, or the rows a filter matches" },
  { action: "delete_rows", what: "delete one row by id, or the rows a filter matches" },
  { action: "row_changed (trigger)", what: "start a workflow when a row is added, changed or deleted" },
];
