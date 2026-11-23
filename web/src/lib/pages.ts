// Pages (decision 0072, docs/pages.md): a business's own screens, a
// page/v1 document drawn by the server (GET /v1/pages/{name}/view). This
// file has the page's types, and the editor's work on its document in
// plain words: no "block", "source" or "query" ever reaches the owner.

import type { Column, Table } from "./tables";

export type Tone = "plain" | "good" | "warn" | "bad";

export interface Condition {
  column: string;
  op: string;
  value?: string | number | boolean;
}

export interface Source {
  /** A data table; or, with records, none. */
  table?: string;
  /** One of Taskiem's own record views (orders, invoices), read only. */
  records?: string;
  filters?: Condition[];
  order?: { column: string; desc?: boolean }[];
  uses?: string[];
  /** A made tool that answers at once (G2): its command, values and the output shown. */
  tool?: string;
  inputs?: Record<string, unknown>;
  output?: string;
}

/** A button (G2): a tool's form, a signal to a run, or a run's approval. */
export interface PageActionDef {
  label: string;
  tool?: string;
  inputs?: Record<string, unknown>;
  once?: boolean;
  signal?: { workflow: string; name: string; run?: string; to?: string; input?: Record<string, unknown> };
  approve?: { workflow: string; run: string; step: string };
  when?: string;
}

export interface Part {
  id: string;
  type: "heading" | "figure" | "list" | "table" | "buttons" | "status";
  title?: string;
  text?: string;
  label?: string;
  value?: "count" | "sum" | "output";
  of?: string;
  naira?: boolean;
  tone?: Tone;
  lead?: boolean;
  change?: { column: string; period: "day" | "week" | "month" };
  size?: "full" | "half";
  source?: Source;
  item?: { title: string; sub?: string; state?: { text: string; tone?: Tone; tones?: Record<string, Tone> } };
  group?: { column: string; order?: string[] };
  columns?: { column: string; label?: string; naira?: boolean }[];
  detail?: { title: string; lines?: { label: string; value: string; naira?: boolean }[] };
  empty?: string;
  /** G2: buttons (the page's, or each row's), a counting figure as an alert, a status's workflow. */
  actions?: PageActionDef[];
  alert?: boolean;
  workflow?: string;
}

export interface PageFilterDef {
  id: string;
  type: "search" | "choice" | "dates" | "mine";
  label?: string;
  column?: string;
  columns?: string[];
  options?: { value: string; label?: string }[];
}

export interface PageDoc {
  page: "page/v1";
  name: string;
  title: string;
  about?: string;
  icon?: string;
  roles?: string[];
  filters?: PageFilterDef[];
  blocks: Part[];
  /** A tab under one of Taskiem's areas, after its own tabs. */
  place?: { area: string };
}

export interface PageSummary {
  name: string;
  title: string;
  icon: string;
  about: string;
  roles: string[];
  pinned: boolean;
  published: boolean;
  has_draft: boolean;
  version: number | null;
  published_at?: string;
  updated_at: string;
}

export interface PageProblem {
  path: string;
  message: string;
}

export interface PageDetail extends PageSummary {
  document: PageDoc | null;
  draft?: PageDoc | null;
  draft_at?: string;
  problems?: PageProblem[];
  shows?: string[];
}

export interface PageList {
  pages: PageSummary[];
  can_edit: boolean;
  can_publish: boolean;
  max_pinned: number;
  max_pages: number;
}

export interface DrawnRow {
  id: string;
  title?: string;
  sub?: string;
  state?: { text: string; tone: Tone };
  group?: string;
  cells?: string[];
  detail?: { title: string; lines: { label: string; value: string }[] };
  /** A row of Taskiem's own records: the record's own screen (/storefront/orders/AD-12). */
  link?: string;
  /** The row's buttons that show for it (G2). */
  actions?: DrawnAction[];
  /** A table's yes/no cells this person may tick here, by column; null for the rest. */
  ticks?: (boolean | null)[];
}

/** A button as drawn: pressed by its part, row and place (POST /v1/pages/{name}/act). */
export interface DrawnAction {
  index: number;
  label: string;
  kind: "tool" | "signal" | "approve";
  tool?: string;
  /** Why this person cannot use it (the tool's roles): drawn greyed. */
  disabled?: string;
}

/** What pressing a button gives. */
export interface ActResult {
  kind: DrawnAction["kind"];
  tool?: { button: string; command: string; name: string; inputs: Record<string, unknown>; once: boolean };
  asks?: string;
  sent?: boolean;
  run?: string;
  words?: string;
  approval?: unknown;
}

/** "Worked out 3 minutes ago": when a tool worked a part out. */
export function workedWords(at: string | undefined, now: number = Date.now()): string {
  if (!at) return "";
  const mins = Math.max(0, Math.round((now - Date.parse(at)) / 60000));
  if (mins < 1) return "Worked out just now";
  if (mins === 1) return "Worked out a minute ago";
  if (mins < 60) return `Worked out ${mins} minutes ago`;
  const hours = Math.round(mins / 60);
  return hours === 1 ? "Worked out an hour ago" : `Worked out ${hours} hours ago`;
}

/** The pages the chat's tools panel shows (pages G3): the pinned ones with
 * nothing after "/", else those whose name or title starts with it. */
export function pagesFor(pages: PageSummary[], query: string): PageSummary[] {
  const q = query.trim().toLowerCase();
  if (!q) return pages.filter((p) => p.pinned);
  return pages.filter((p) => p.name.startsWith(q) || p.title.toLowerCase().startsWith(q));
}

/** The page "/" and its name opens, exactly (pages §9). */
export function pageCalled(pages: PageSummary[], text: string): PageSummary | undefined {
  const m = /^\/([a-z][a-z0-9-]{1,23})$/.exec(text.trim().toLowerCase());
  return m ? pages.find((p) => p.name === m[1]) : undefined;
}

/** Whether a page has a part a tool is still working out (read again soon). */
export const stillWorking = (page: { blocks: DrawnPart[] }) => page.blocks.some((b) => !!b.working);

export interface DrawnPart {
  id: string;
  type: Part["type"];
  title?: string;
  text?: string;
  label?: string;
  value?: number;
  shown?: string;
  tone?: Tone;
  lead?: boolean;
  naira?: boolean;
  over?: string;
  change?: { before: number; words: string; up: boolean; down: boolean };
  size?: "full" | "half";
  empty?: string;
  count?: number;
  has_detail?: boolean;
  rows?: DrawnRow[];
  more?: string | null;
  groups?: { key: string; label: string }[];
  /** sortable: whether this viewer may put the rows in the column's order
   * (never a personal column for someone without table.read). */
  columns?: { column: string; label: string; naira: boolean; sortable?: boolean; edit?: boolean }[];
  order?: { column: string; desc: boolean };
  /** The page's own buttons (a buttons part). */
  actions?: DrawnAction[];
  /** A counting figure that is an alert while above zero. */
  alert?: boolean;
  /** A table with columns this person may tick here: its table. */
  table_id?: string;
  /** Worked out by a made tool: when, or still working. */
  from_tool?: boolean;
  worked_at?: string;
  working?: string;
  /** A status part: its run, state and words, and who it waits for. */
  run?: string;
  status?: string;
  words?: string;
  started_at?: string;
  ended_at?: string;
  waiting_for?: { kind: string; who: string; you?: boolean; request?: string }[];
  broken?: string;
  error?: string;
  /** In place of a part over Taskiem's records: "You cannot see orders.", or its area is off. */
  unavailable?: string;
}

export interface DrawnFilter {
  id: string;
  type: PageFilterDef["type"];
  label: string;
  value: string;
  options?: { value: string; label: string }[];
}

export interface DrawnPage {
  page: PageSummary;
  draft: boolean;
  filters: DrawnFilter[];
  blocks: DrawnPart[];
  can_edit: boolean;
  drawn_at: string;
}

/** A tone as the app's badges and icons name it. */
export function toneClass(t: Tone | undefined): "ok" | "warn" | "danger" | "muted" {
  switch (t) {
    case "good":
      return "ok";
    case "warn":
      return "warn";
    case "bad":
      return "danger";
  }
  return "muted";
}

/** Figures that sit side by side, and the other parts, in order: a page
 * is drawn as runs of figures (a row of tiles) between the rest. */
export function layout(parts: DrawnPart[]): (DrawnPart | DrawnPart[])[] {
  const out: (DrawnPart | DrawnPart[])[] = [];
  for (const p of parts) {
    const last = out[out.length - 1];
    if (p.type === "figure" && !p.lead && !p.broken) {
      if (Array.isArray(last)) last.push(p);
      else out.push([p]);
    } else out.push(p);
  }
  return out;
}

/** A list's rows under their group headings, in the server's order. */
export function grouped(part: DrawnPart): { key: string; label: string; rows: DrawnRow[] }[] {
  const rows = part.rows ?? [];
  if (!part.groups?.length) return [{ key: "", label: "", rows }];
  return part.groups.map((g) => ({ ...g, rows: rows.filter((r) => (r.group ?? "") === g.key) })).filter((g) => g.rows.length > 0);
}

/** The filters param a page is drawn with: only those set. */
export function filtersParam(values: Record<string, string>): string {
  const set = Object.fromEntries(Object.entries(values).filter(([, v]) => v !== ""));
  return Object.keys(set).length ? `filters=${encodeURIComponent(JSON.stringify(set))}` : "";
}

/** A viewer's saved filter values kept for the filters the page has now:
 * a value for a filter it no longer has, a choice it no longer offers, or
 * an empty one is dropped. */
export function keptFilters(values: Record<string, string>, filters: DrawnFilter[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [id, v] of Object.entries(values)) {
    const f = filters.find((x) => x.id === id);
    if (!f || v === "") continue;
    if (f.type === "choice" && !f.options?.some((o) => o.value === v)) continue;
    out[id] = v;
  }
  return out;
}

/** A page drawn with a viewer's saved filters. When the server refuses
 * them (the page's filters changed since they were saved: a 400 about the
 * page's filters), they are dropped (onStale) and the page is drawn once
 * more without them. */
export async function viewWithFilters(
  load: (query: string) => Promise<DrawnPage>,
  values: Record<string, string>,
  onStale: () => void,
): Promise<DrawnPage> {
  const query = filtersParam(values);
  try {
    return await load(query);
  } catch (e: unknown) {
    const err = e as { status?: number; body?: Record<string, unknown> };
    if (!query || err?.status !== 400 || err.body?.code !== "page_problems") throw e;
    onStale();
    return load("");
  }
}

/** How many rows, in words: "3 rows", "1 row". */
export function rowsWord(n: number): string {
  return `${n.toLocaleString("en-GB")} ${n === 1 ? "row" : "rows"}`;
}

/** Who may open a page, in words. */
export function whoWords(roles: string[]): string {
  if (roles.length === 0) return "Everyone in the business";
  const words = roles.map((r) => r.replace(/_/g, " "));
  const list = words.length === 1 ? words[0] : `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`;
  return `Owners and ${list}`;
}

/** What publishing says (pages §8): who will be able to open it and what it shows. */
export function publishWords(title: string, roles: string[], shows: string[]): string[] {
  const who = roles.length === 0 ? "Everyone in the business" : whoWords(roles);
  return [`${who} will be able to open ${title}.`, ...shows.map((s) => `It shows ${s}.`)];
}

// --- the editor ---

/** A column a row value shows: "=row.status" is status; anything else is not one column. */
export function columnOf(v: string | undefined): string {
  const m = /^=row\.([a-z][a-z0-9_]*)$/.exec(v ?? "");
  return m?.[1] ?? "";
}

/** The value that shows one column. */
export const showColumn = (c: string) => (c ? `=row.${c}` : "");

/** A column's words: its title, else its name. */
export function columnWords(t: Table | undefined, name: string): string {
  if (name === "created_at") return "When added";
  if (name === "updated_at") return "When changed";
  const c = t?.columns.find((x) => x.name === name);
  return c?.title || c?.name || name.replace(/_/g, " ");
}

// --- Taskiem's own records (orders, invoices) as a part's source ---

/** A view of Taskiem's own records, as GET /v1/page-records lists it. */
export interface RecordView {
  name: string;
  title: string;
  on: boolean;
  may_read: boolean;
  area: string;
  columns: { name: string; title: string; type: string; personal: boolean }[];
}

/** The editor names a view as a table ("records:orders"), so its pickers
 * offer Orders and Invoices next to the business's tables. */
export const RECORDS = "records:";
export const isRecords = (name: string | undefined) => !!name?.startsWith(RECORDS);

/** The views whose area is on, as tables the editor picks from. */
export function recordTables(views: RecordView[]): Table[] {
  return views
    .filter((v) => v.on)
    .map((v) => ({
      id: RECORDS + v.name,
      name: RECORDS + v.name,
      title: `${v.title} (Taskiem's)`,
      description: "",
      columns: v.columns.map((c) => ({ name: c.name, title: c.title, type: c.type as Column["type"], required: false, personal: c.personal })),
      postgres_table: "",
      connection: "",
      created_by: "",
      created_at: "",
      updated_at: "",
    }));
}

/** A source as the editor holds it: a view as "records:<view>" in table. */
export function editorSource(s: Source | undefined): Source | undefined {
  if (!s?.records) return s;
  const { records, ...rest } = s;
  return { ...rest, table: RECORDS + records };
}

/** A source as the page's document says it: {"records": "orders"}. */
export function docSource(s: Source | undefined): Source | undefined {
  if (!s || !isRecords(s.table)) return s;
  const { table, ...rest } = s;
  return { ...rest, records: (table ?? "").slice(RECORDS.length) };
}

/** A document as the editor holds it. */
export function editorDoc(doc: PageDoc | null): PageDoc | null {
  if (!doc) return doc;
  return { ...doc, blocks: doc.blocks.map((b) => (b.source ? { ...b, source: editorSource(b.source) } : b)) };
}

const isNumber = (c: Column) => c.type === "integer" || c.type === "number";
const isDate = (c: Column) => c.type === "date" || c.type === "datetime";

/** The columns a part may group by, order by, add up or compare by. A
 * personal column is never added up, compared by, grouped by or chosen
 * by (sum and date leave it out): the server refuses it (pages §18). */
export function columnsFor(t: Table | undefined, what: "any" | "pick" | "number" | "sum" | "date" | "group" | "text" | "email"): { name: string; words: string }[] {
  const cols = t?.columns ?? [];
  // pick: what rows may be narrowed or ordered by; a personal column of
  // Taskiem's records is masked for everyone, and never one of them.
  if (what === "pick") return columnsFor(t, "any").filter((c) => !(isRecords(t?.name) && cols.find((x) => x.name === c.name)?.personal));
  const pick = cols.filter((c) => {
    switch (what) {
      case "number":
        return isNumber(c);
      case "sum":
        return isNumber(c) && !c.personal;
      case "date":
        return isDate(c) && !c.personal;
      case "group":
        return c.type === "text" && !c.personal;
      case "text":
        return c.type === "text";
      case "email":
        return c.type === "email";
    }
    return c.type !== "json";
  });
  const out = pick.map((c) => ({ name: c.name, words: c.title || c.name }));
  if (what === "date" || what === "any") out.push({ name: "created_at", words: "When added" });
  return out;
}

/** A short id not yet used on the page. */
export function freeId(parts: { id: string }[], base: string): string {
  const clean = base.replace(/[^a-z0-9_]/g, "_").replace(/^[^a-z]+/, "") || "part";
  let id = clean.slice(0, 28);
  for (let i = 2; parts.some((p) => p.id === id); i++) id = `${clean.slice(0, 26)}_${i}`;
  return id;
}

/** The kinds of part the editor adds, in an owner's words. */
export const PART_KINDS: { kind: string; words: string; hint: string }[] = [
  { kind: "count", words: "A count", hint: "How many rows, like Open tickets" },
  { kind: "sum", words: "A total", hint: "A column added up, like money owed" },
  { kind: "list", words: "A list", hint: "Rows one under another, in groups if you like" },
  { kind: "table", words: "A table", hint: "Rows in columns you pick" },
  { kind: "heading", words: "A heading", hint: "A title and a line of words" },
];

/** A new part of a kind over a table (or Taskiem's records), filled in with sensible choices. */
export function newPart(kind: string, t: Table | undefined, parts: { id: string }[]): Part {
  const table = t?.name ?? "";
  const text = columnsFor(t, "text");
  const any = columnsFor(t, "any").filter((c) => c.name !== "created_at");
  switch (kind) {
    case "heading":
      return { id: freeId(parts, "heading"), type: "heading", title: "A heading" };
    case "sum": {
      const num = columnsFor(t, "sum")[0];
      return { id: freeId(parts, "total"), type: "figure", label: num ? `Total ${num.words.toLowerCase()}` : "Total", value: "sum", of: num?.name ?? "", source: { table } };
    }
    case "list": {
      const title = text[0]?.name ?? any[0]?.name ?? "";
      const sub = any.find((c) => c.name !== title)?.name ?? "";
      return {
        id: freeId(parts, "list"),
        type: "list",
        title: t?.title || t?.name || "List",
        source: { table, order: [{ column: "created_at", desc: true }] },
        item: { title: showColumn(title) || "='#' + row.id", ...(sub ? { sub: showColumn(sub) } : {}) },
        detail: { title: showColumn(title) || "='#' + row.id", lines: any.slice(0, 11).map((c) => ({ label: c.words, value: showColumn(c.name) })) },
      };
    }
    case "table":
      return { id: freeId(parts, "table"), type: "table", title: t?.title || t?.name || "Table", source: { table }, columns: any.slice(0, 3).map((c) => ({ column: c.name })) };
  }
  return { id: freeId(parts, "count"), type: "figure", label: t?.title || t?.name || "How many", value: "count", source: { table } };
}

/** What a part is, in a line for the editor's list of parts. */
export function partWords(p: Part, tables: Table[]): string {
  const t = tables.find((x) => x.name === p.source?.table);
  const from = t ? t.title || t.name : p.source?.table ?? "";
  const where = (p.source?.filters ?? []).length ? `, only some rows` : "";
  switch (p.type) {
    case "heading":
      return `Heading: ${p.title ?? ""}`;
    case "figure":
      return p.value === "sum"
        ? `Total of ${columnWords(t, p.of ?? "")} from ${from}${where}`
        : `How many in ${from}${where}${p.change ? `, this ${p.change.period}` : ""}`;
    case "list":
      return `List of ${from}${p.group ? `, in groups by ${columnWords(t, p.group.column)}` : ""}${where}`;
    case "table":
      return `Table of ${from}: ${(p.columns ?? []).map((c) => c.label || columnWords(t, c.column)).join(", ")}${where}`;
  }
  return p.type;
}

/** A part's heading in the editor: its label or title. */
export const partName = (p: Part) => p.label || p.title || "Part";

/** Moves part i up (-1) or down (+1). */
export function move<T>(list: T[], i: number, by: -1 | 1): T[] {
  const j = i + by;
  if (j < 0 || j >= list.length) return list;
  const out = [...list];
  const a = out[i] as T;
  out[i] = out[j] as T;
  out[j] = a;
  return out;
}

/** The conditions an owner picks from, by a column's type. */
export function opsFor(c: Column | undefined): { op: string; words: string }[] {
  const base = [
    { op: "eq", words: "is" },
    { op: "neq", words: "is not" },
  ];
  const empty = [
    { op: "is_null", words: "is empty" },
    { op: "not_null", words: "is not empty" },
  ];
  if (!c) return [...base, ...empty];
  if (c.type === "text") return [...base, { op: "contains", words: "has the words" }, ...empty];
  if (c.type === "boolean") return [{ op: "eq", words: "is" }, ...empty];
  if (isNumber(c) || isDate(c))
    return [...base, { op: "gt", words: "is more than" }, { op: "gte", words: "is at least" }, { op: "lt", words: "is less than" }, { op: "lte", words: "is at most" }, ...empty];
  return [...base, ...empty];
}

/** Whether a filter fits a table (the server's own check, so the editor
 * sets a part to use only the filters it can). */
export function filterFits(f: PageFilterDef, t: Table | undefined): boolean {
  if (!t) return false;
  const col = t.columns.find((c) => c.name === f.column);
  switch (f.type) {
    case "search":
      if (isRecords(t.name) && t.columns.some((c) => c.personal && f.columns?.includes(c.name))) return false;
      return (f.columns?.length ? t.columns.filter((c) => f.columns?.includes(c.name)) : t.columns.filter((c) => c.type === "text" && !(isRecords(t.name) && c.personal))).length > 0;
    case "choice":
      return col?.type === "text" && !col.personal;
    case "dates":
      return f.column === "created_at" || (!!col && isDate(col) && !col.personal);
    case "mine":
      return col?.type === "email" && !isRecords(t.name);
  }
  return false;
}

/** The document as it is saved: each list and table uses every filter
 * that fits its table (figures stay whole), and a part over Taskiem's
 * records says so as {"records": "orders"}. */
export function withUses(doc: PageDoc, tables: Table[]): PageDoc {
  const filters = doc.filters ?? [];
  return {
    ...doc,
    blocks: doc.blocks.map((b) => {
      if (!b.source) return b;
      if (b.type !== "list" && b.type !== "table") return { ...b, source: docSource(b.source) };
      const t = tables.find((x) => x.name === b.source?.table);
      const uses = filters.filter((f) => filterFits(f, t)).map((f) => f.id);
      const source = { ...b.source };
      if (uses.length) source.uses = uses;
      else delete source.uses;
      return { ...b, source: docSource(source) };
    }),
  };
}

/** The problems that are about one part (by its place), and the rest. */
export function problemsOf(problems: PageProblem[], i: number): PageProblem[] {
  return problems.filter((p) => p.path === `/blocks/${i}` || p.path.startsWith(`/blocks/${i}/`));
}

/** The dates a dates filter offers. */
export const DATE_CHOICES = [
  { value: "", words: "Any time" },
  { value: "today", words: "Today" },
  { value: "week", words: "This week" },
  { value: "month", words: "This month" },
];

// --- versions, and a page to and from a file ---

/** One published version, as GET /v1/pages/{name}/versions lists it. */
export interface PageVersion {
  version: number;
  title?: string;
  published_by: string;
  published_at: string;
  summary: string;
  changes: string[];
  roles: string[];
  who: string;
  current: boolean;
}

/** A page made from a file (POST /v1/pages/import). */
export interface PageImported {
  page: PageDetail;
  missing: string[];
  renamed_from: string | null;
}

/** Whether this viewer may put a table's rows in a column's order. */
export const canSort = (c: { sortable?: boolean }) => c.sortable !== false;

/** The file a page is saved as. */
export const pageFileName = (name: string) => `${name}.page.json`;

/** How many conditions a page's parts have (to say how many the file left out). */
export function conditionCount(doc: PageDoc | null | undefined): number {
  return (doc?.blocks ?? []).reduce((n, b) => n + (b.source?.filters?.length ?? 0), 0);
}

/** A file read as a page, or why it is not one, in plain words. The server
 * checks it fully; this only stops what is plainly not a page. */
export function readPageFile(text: string): { document: PageDoc } | { error: string } {
  let doc: unknown;
  try {
    doc = JSON.parse(text);
  } catch {
    return { error: "This file is not a page saved from Taskiem: it could not be read." };
  }
  if (!doc || typeof doc !== "object" || Array.isArray(doc) || (doc as { page?: unknown }).page !== "page/v1") {
    return { error: "This file is not a page saved from Taskiem." };
  }
  return { document: doc as PageDoc };
}

/** What making a page from a file says: its new name, and what this
 * business lacks of what it shows. */
export function importWords(r: PageImported): string[] {
  const out: string[] = [];
  if (r.renamed_from) out.push(`Another page was called ${r.renamed_from}, so this one is ${r.page.name}.`);
  if (r.missing.length) out.push("Some of what it shows is not here yet:", ...r.missing);
  return out;
}

/** A version's line in the list: who and when go beside it. */
export function versionTitle(v: PageVersion): string {
  return v.current ? `Version ${v.version} (what people see now)` : `Version ${v.version}`;
}

// --- kept up to date (GET /v1/pages/{name}/live) ---

/** What a page's live notice says: the parts that may have changed (every
 * part, all, when the page itself changed). It carries no data. */
export interface PageChanged {
  page: string;
  blocks: string[];
  all?: boolean;
}

/** Gathers the parts named by notices and reads them once at most every
 * wait ms: a burst of notices is one read, and a notice that comes while a
 * read is under way is read after it. */
export function liveQueue(read: (ids: string[], all: boolean) => Promise<void> | void, wait = 2000, now: () => number = Date.now) {
  let pending = new Set<string>();
  let all = false;
  let last = -Infinity;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let busy = false;
  const fire = async () => {
    timer = undefined;
    if (busy) return;
    const ids = [...pending];
    const every = all;
    pending = new Set();
    all = false;
    if (ids.length === 0 && !every) return;
    busy = true;
    last = now();
    try {
      await read(ids, every);
    } finally {
      busy = false;
      if (pending.size > 0 || all) schedule();
    }
  };
  const schedule = () => {
    if (timer !== undefined) return;
    timer = setTimeout(() => void fire(), Math.max(0, last + wait - now()));
  };
  return {
    add(e: PageChanged) {
      e.blocks.forEach((b) => pending.add(b));
      all = all || !!e.all;
      schedule();
    },
    stop() {
      if (timer !== undefined) clearTimeout(timer);
      timer = undefined;
      pending = new Set();
    },
  };
}
