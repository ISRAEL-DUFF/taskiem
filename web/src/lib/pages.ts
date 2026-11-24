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
  type: "heading" | "figure" | "list" | "table" | "buttons" | "status" | "board" | "chart";
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
  columns?: { column: string; label?: string; naira?: boolean; edit?: boolean }[];
  detail?: { title: string; lines?: { label: string; value: string; naira?: boolean }[] };
  empty?: string;
  /** G2: buttons (the page's, or each row's), a counting figure as an alert, a status's workflow. */
  actions?: PageActionDef[];
  alert?: boolean;
  workflow?: string;
  /** H5: each row shows where a run of workflow is (its number in a column, or what it waits for). */
  status?: { workflow: string; run?: string; to?: string };
  /** G4: a board's columns by a choice, and the tool a card moved runs. */
  board?: BoardDef;
  /** G4: what a chart shows. */
  chart?: ChartDef;
}

/** A board's columns (G4): the choice they are by, each column's value and
 * heading, and the tool a card moved opens (its values may read "=to", the
 * column it goes to). */
export interface BoardDef {
  column: string;
  columns: { value: string; label?: string }[];
  move?: PageActionDef;
}

/** What a chart shows (G4): over time (by a date column, a period and how
 * many) or by a group (a column of words), counted or added up (of); or a
 * tool's rows, each a label and a number. */
export interface ChartDef {
  kind: "bars" | "line";
  of?: string;
  by?: string;
  period?: "day" | "week" | "month";
  last?: number;
  label?: string;
  value?: string;
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
  /** The area it is a tab under (published pages only). */
  place?: string;
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
  /** The page's settings: about how many runs its tools start a day. */
  runs?: PageRuns;
}

export interface PageRuns {
  views_a_day: number;
  tools: number;
  runs_a_day: number;
  runs_per_day?: number;
  runs_per_month?: number;
}

/** The estimate of a page's runs a day, in words (pages §13): "About 40
 * runs a day: 20 views a day, 2 tools each. Your plan allows 500 a day." */
export function runsWords(r: PageRuns | undefined): { line: string; over: boolean } | null {
  if (!r || r.tools === 0) return null;
  const n = (x: number) => x.toLocaleString("en-NG");
  const each = r.tools === 1 ? "1 tool each" : `${n(r.tools)} tools each`;
  const views = r.views_a_day === 0 ? "no views yet in the last week" : `${n(r.views_a_day)} ${r.views_a_day === 1 ? "view" : "views"} a day`;
  let line = `About ${n(r.runs_a_day)} ${r.runs_a_day === 1 ? "run" : "runs"} a day from this page: ${views}, ${each}. Each person's answer is kept for a minute.`;
  let over = false;
  if (r.runs_per_day && r.runs_per_day > 0) {
    over = r.runs_a_day > r.runs_per_day;
    line += ` Your plan allows ${n(r.runs_per_day)} runs a day for everything.`;
  } else if (r.runs_per_month && r.runs_per_month > 0) {
    over = r.runs_a_day * 30 > r.runs_per_month;
    line += ` Your plan allows ${n(r.runs_per_month)} runs a month for everything (about ${n(r.runs_a_day * 30)} a month from this page).`;
  }
  return { line, over };
}

/** A page link's filters (?filters={"day":"today"}, the link a Tell me
 * message's page button answers with): at most four short words by filter
 * id; null when there are none or they are not that. */
export function linkFilters(raw: string | null): Record<string, string> | null {
  if (!raw || raw.length > 600) return null;
  try {
    const m: unknown = JSON.parse(raw);
    if (!m || typeof m !== "object" || Array.isArray(m)) return null;
    const entries = Object.entries(m as Record<string, unknown>);
    if (entries.length === 0 || entries.length > 4) return null;
    const out: Record<string, string> = {};
    for (const [k, v] of entries) {
      if (!/^[a-z][a-z0-9_-]{0,23}$/.test(k) || typeof v !== "string" || v.length > 100) return null;
      out[k] = v;
    }
    return out;
  } catch {
    return null;
  }
}

export interface RowRunStatus {
  status?: string;
  words: string;
  tone: string;
  run?: string;
  started_at?: string;
  ended_at?: string;
  waiting_for?: { kind: string; who: string; you?: boolean; request?: string }[];
}

/** A row's run status as one line: "Waiting for Ada", "Done". */
export function runStatusLine(s: RowRunStatus | undefined): string {
  if (!s) return "";
  const who = (s.waiting_for ?? []).map((w) => (w.you ? "you" : w.who)).filter(Boolean);
  return who.length ? `${s.words}: for ${who.join(", ")}` : s.words;
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
  /** Where the row's run is (the part's status): words for everyone; the
   * run, its times and who it waits for only for those who may read runs. */
  run_status?: RowRunStatus;
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

/** The published pages placed as tabs under these areas, in the list's order (pages §9). */
export function placedPages(pages: PageSummary[], areas: string[]): PageSummary[] {
  return pages.filter((p) => p.published && !!p.place && areas.includes(p.place));
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
  /** A board (G4): its columns with their first cards, and the move. */
  lanes?: DrawnLane[];
  move?: { label: string; tool: string; disabled?: string };
  /** A chart (G4), every figure worked out by the server. */
  kind?: "bars" | "line";
  points?: ChartPoint[];
  ticks?: { value: number; shown: string; share: number }[];
  zero?: number;
  label_head?: string;
  value_head?: string;
  summary?: string;
}

/** One of a board's columns as drawn. */
export interface DrawnLane {
  value: string;
  label: string;
  count: number;
  rows: DrawnRow[];
  more?: string | null;
}

/** One of a chart's bars or points: its place on the scale (share, 0 to 1) is the server's. */
export interface ChartPoint {
  label: string;
  long: string;
  value: number;
  shown: string;
  share: number;
  tick: boolean;
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
  /** sealed: read from its record's envelope (a rider's name): never narrowed or ordered by. */
  columns: { name: string; title: string; type: string; personal: boolean; sealed?: boolean }[];
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
      // A sealed column is offered as a personal one is: shown, never a condition or an order.
      columns: v.columns.map((c) => ({ name: c.name, title: c.title, type: c.type as Column["type"], required: false, personal: c.personal || !!c.sealed })),
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
export function columnsFor(t: Table | undefined, what: "any" | "pick" | "number" | "sum" | "date" | "group" | "text" | "mine"): { name: string; words: string }[] {
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
      case "mine":
        return c.type === "email" || c.type === "phone";
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
  { kind: "tool-figure", words: "A figure a tool works out", hint: "One of a tool's answers, like this month's spending" },
  { kind: "tool-list", words: "A list a tool works out", hint: "The rows a tool answers with" },
  { kind: "buttons", words: "Buttons", hint: "Up to three for the whole page, like New ticket" },
  { kind: "status", words: "Where a run is", hint: "Waiting, done or stopped, and who it waits for" },
  { kind: "board", words: "A board", hint: "Rows in columns, like Open, Doing and Done; moving a card runs a tool" },
  { kind: "chart", words: "A chart", hint: "Bars or a line: a total by week, or by kind" },
  { kind: "tool-chart", words: "A chart a tool works out", hint: "Bars or a line of the rows a tool answers with" },
];

/** The kinds that need no table (a tool, a run, or nothing feeds them). */
export const NO_TABLE_KINDS = ["heading", "tool-figure", "tool-list", "buttons", "status", "tool-chart"];

/** The kinds that read only a business's own table, never Taskiem's records. */
export const OWN_TABLE_KINDS = ["board", "chart"];

// --- the tools and workflows a page may use (GET /v1/page-choices) ---

export interface ChoiceTool {
  command: string;
  name: string;
  /** Answers at once (changes nothing): only such a tool feeds a part. */
  answers: boolean;
  outputs: { name: string; title: string; type: string }[];
  inputs: { name: string; title: string; personal: boolean }[];
  list?: { from: string; title: string; sub?: string };
}

export interface ChoiceWorkflow {
  name: string;
  /** The signals a page may send it. */
  signals: string[];
  approvals: { step: string; name: string }[];
}

export interface PageChoices {
  tools: ChoiceTool[];
  workflows: ChoiceWorkflow[];
}

/** The tools that may feed a part showing one figure, or a list. */
export function feedingTools(c: PageChoices, kind: "figure" | "list"): ChoiceTool[] {
  return c.tools.filter((t) => t.answers && t.outputs.some((o) => (kind === "list" ? o.type === "list" : o.type !== "list" && o.type !== "object")));
}

/** A CEL string literal of words: nothing in it is worked out. */
export const quoted = (s: string) => `'${s.replace(/\\/g, "\\\\").replace(/'/g, "\\'")}'`;

/** Where a button's value comes from, in plain words: a column of the row,
 * one of the page's filters, words written here, or nothing (asked). */
export type ValueFrom = { from: "column"; column: string } | { from: "filter"; id: string } | { from: "words"; words: string } | { from: "asked" };

/** A value as the document keeps it. */
export function valueExpr(v: ValueFrom): string | undefined {
  switch (v.from) {
    case "column":
      return v.column ? `=row.${v.column}` : undefined;
    case "filter":
      return v.id ? `=filters.${v.id}` : undefined;
    case "words":
      return v.words === "" ? undefined : v.words.startsWith("=") ? `=${quoted(v.words)}` : v.words;
  }
  return undefined;
}

/** A value read back for the editor: null when it was written another way (a file). */
export function valueFrom(v: unknown): ValueFrom | null {
  if (v === undefined) return { from: "asked" };
  if (typeof v !== "string") return { from: "words", words: String(v) };
  let m = /^=row\.([a-z][a-z0-9_]*)$/.exec(v);
  if (m) return { from: "column", column: m[1] ?? "" };
  m = /^=filters\.([a-z][a-z0-9_-]*)$/.exec(v);
  if (m) return { from: "filter", id: m[1] ?? "" };
  m = /^='((?:[^'\\]|\\.)*)'$/.exec(v);
  if (m) return { from: "words", words: (m[1] ?? "").replace(/\\(.)/g, "$1") };
  if (v.startsWith("=")) return null;
  return { from: "words", words: v };
}

/** One "Show it only when" condition, in plain words. */
export interface WhenCond {
  column: string;
  how: "is" | "is not" | "is empty" | "is not empty";
  value?: string;
}

/** The conditions as a button's when: every one must hold. */
export function whenExpr(conds: WhenCond[]): string | undefined {
  const parts = conds
    .filter((c) => c.column)
    .map((c) => {
      const col = `row.${c.column}`;
      switch (c.how) {
        case "is":
          return `${col} == ${quoted(c.value ?? "")}`;
        case "is not":
          return `${col} != ${quoted(c.value ?? "")}`;
        case "is empty":
          return `${col} == ''`;
        case "is not empty":
          return `${col} != ''`;
      }
      return "";
    });
  return parts.length ? `=${parts.join(" && ")}` : undefined;
}

/** A button's when read back as conditions; null when written another way. */
export function whenConds(when: string | undefined): WhenCond[] | null {
  if (!when) return [];
  if (!when.startsWith("=")) return null;
  const out: WhenCond[] = [];
  for (const part of when.slice(1).split(" && ")) {
    const m = /^row\.([a-z][a-z0-9_]*) (==|!=) '((?:[^'\\]|\\.)*)'$/.exec(part.trim());
    if (!m) return null;
    const value = (m[3] ?? "").replace(/\\(.)/g, "$1");
    const eq = m[2] === "==";
    out.push(value === "" ? { column: m[1] ?? "", how: eq ? "is empty" : "is not empty" } : { column: m[1] ?? "", how: eq ? "is" : "is not", value });
  }
  return out;
}

/** A row's title of one column, or two with a dot between ("AC-12 · Musa"). */
export function titleExpr(a: string, b?: string): string {
  if (!a) return "";
  return b ? `=row.${a} + ' · ' + row.${b}` : `=row.${a}`;
}

/** A row's title read back as its columns; [] when written another way. */
export function titleCols(v: string | undefined): string[] {
  const one = columnOf(v);
  if (one) return [one];
  const m = /^=row\.([a-z][a-z0-9_]*) \+ ' · ' \+ row\.([a-z][a-z0-9_]*)$/.exec(v ?? "");
  return m ? [m[1] ?? "", m[2] ?? ""] : [];
}

/** New page's "Nothing yet": a page started with a heading only. */
export const EMPTY = "empty:";

/** A page's short name from its name ("Deliveries today" → deliveries-today),
 * not one of taken (the next free: -2, -3). */
export function pageName(title: string, taken: string[]): string {
  let base = title
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^[^a-z]+/, "")
    .slice(0, 24)
    .replace(/-+$/, "");
  if (base.length < 2) base = "page";
  let name = base;
  for (let i = 2; taken.includes(name); i++) name = `${base.slice(0, 24 - String(i).length - 1).replace(/-+$/, "")}-${i}`;
  return name;
}

/** A new page's first draft: over Taskiem's records, how many and their
 * rows as a list (as Show as a page does for a table); else a heading only. */
export function newPageDoc(title: string, taken: string[], view: Table | undefined): PageDoc {
  const t = title.trim() || view?.title || "A page";
  const blocks: Part[] = view ? [newPart("count", view, []), newPart("list", view, [{ id: "count" }])] : [newPart("heading", undefined, [])];
  if (!view && blocks[0]) blocks[0].title = t;
  return { page: "page/v1", name: pageName(t, taken), title: t, icon: "tables", blocks };
}

/** Taskiem's areas a page may be a tab under, in Settings' words. */
export const PLACE_AREAS: { area: string; words: string }[] = [
  { area: "storefront", words: "Online shop" },
  { area: "orders", words: "Orders" },
  { area: "stock", words: "Stock" },
  { area: "money", words: "Money" },
  { area: "links", words: "Linked businesses" },
  { area: "schools", words: "Schools" },
];

/** A new part of a kind over a table (or Taskiem's records), or a tool or
 * a workflow the business has, filled in with sensible choices. */
export function newPart(kind: string, t: Table | undefined, parts: { id: string }[], choices?: PageChoices): Part {
  const table = t?.name ?? "";
  const text = columnsFor(t, "text");
  const any = columnsFor(t, "any").filter((c) => c.name !== "created_at");
  switch (kind) {
    case "tool-figure": {
      const tool = feedingTools(choices ?? { tools: [], workflows: [] }, "figure")[0];
      const out = tool?.outputs.find((o) => o.type !== "list" && o.type !== "object");
      return { id: freeId(parts, "figure"), type: "figure", label: out?.title || "A figure", value: "output", source: { tool: tool?.command ?? "", output: out?.name ?? "" } };
    }
    case "tool-list": {
      const tool = feedingTools(choices ?? { tools: [], workflows: [] }, "list")[0];
      const out = tool?.outputs.find((o) => o.type === "list");
      const own = tool?.list && tool.list.from === out?.name ? tool.list : undefined;
      return {
        id: freeId(parts, "list"),
        type: "list",
        title: tool?.name || "List",
        source: { tool: tool?.command ?? "", output: out?.name ?? "" },
        item: { title: own?.title || "=item.name", ...(own?.sub ? { sub: own.sub } : {}) },
      };
    }
    case "tool-chart": {
      const tool = feedingTools(choices ?? { tools: [], workflows: [] }, "list")[0];
      const out = tool?.outputs.find((o) => o.type === "list");
      return {
        id: freeId(parts, "chart"),
        type: "chart",
        title: tool?.name || "Chart",
        source: { tool: tool?.command ?? "", output: out?.name ?? "" },
        chart: { kind: "bars", label: "=item.name", value: "=item.total" },
      };
    }
    case "buttons":
      return { id: freeId(parts, "buttons"), type: "buttons", actions: [] };
    case "status":
      return { id: freeId(parts, "status"), type: "status", title: "Where it is", workflow: choices?.workflows[0]?.name ?? "" };
  }
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
    case "board": {
      const title = text[0]?.name ?? any[0]?.name ?? "";
      const by = columnsFor(t, "group").find((c) => /status|state|stage/.test(c.name)) ?? columnsFor(t, "group")[0];
      return {
        id: freeId(parts, "board"),
        type: "board",
        title: t?.title || t?.name || "Board",
        source: { table },
        item: { title: showColumn(title) || "='#' + row.id" },
        board: { column: by?.name ?? "", columns: BOARD_START.map((c) => ({ ...c })) },
      };
    }
    case "chart": {
      const date = columnsFor(t, "date").find((c) => c.name !== "created_at") ?? columnsFor(t, "date")[0];
      const num = columnsFor(t, "sum")[0];
      return {
        id: freeId(parts, "chart"),
        type: "chart",
        title: num ? `${num.words} by week` : "How many by week",
        source: { table },
        chart: { kind: "bars", by: date?.name ?? "created_at", period: "week", last: 8, ...(num ? { of: num.name } : {}) },
      };
    }
  }
  return { id: freeId(parts, "count"), type: "figure", label: t?.title || t?.name || "How many", value: "count", source: { table } };
}

/** What a part is, in a line for the editor's list of parts. */
export function partWords(p: Part, tables: Table[]): string {
  const t = tables.find((x) => x.name === p.source?.table);
  const from = t ? t.title || t.name : p.source?.table ?? "";
  const where = (p.source?.filters ?? []).length ? `, only some rows` : "";
  if (p.source?.tool) return `${p.type === "figure" ? p.label ?? "A figure" : p.type === "chart" ? "A chart" : p.type === "board" ? "A board" : "A list"} worked out by /${p.source.tool}`;
  switch (p.type) {
    case "heading":
      return `Heading: ${p.title ?? ""}`;
    case "buttons":
      return `Buttons: ${(p.actions ?? []).map((a) => a.label).join(", ")}`;
    case "status":
      return `Where a run of ${p.workflow ?? ""} is`;
    case "figure":
      return p.value === "sum"
        ? `Total of ${columnWords(t, p.of ?? "")} from ${from}${where}`
        : `How many in ${from}${where}${p.change ? `, this ${p.change.period}` : ""}`;
    case "list":
      return `List of ${from}${p.group ? `, in groups by ${columnWords(t, p.group.column)}` : ""}${where}`;
    case "table":
      return `Table of ${from}: ${(p.columns ?? []).map((c) => c.label || columnWords(t, c.column)).join(", ")}${where}`;
    case "board":
      return `Board of ${from} in columns by ${columnWords(t, p.board?.column ?? "")}: ${(p.board?.columns ?? []).map((c) => c.label || c.value).join(", ")}${p.board?.move?.tool ? `; moving a card opens /${p.board.move.tool}` : ""}${where}`;
    case "chart":
      return `${p.chart?.kind === "line" ? "A line" : "Bars"} of ${p.chart?.of ? `${columnWords(t, p.chart.of)} added up` : "how many"} in ${from}, ${p.chart?.period ? `by ${p.chart.period}` : `by ${columnWords(t, p.chart?.by ?? "")}`}${where}`;
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
      return (col?.type === "email" || col?.type === "phone") && !isRecords(t.name);
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
      if (b.type !== "list" && b.type !== "table" && b.type !== "board") return { ...b, source: docSource(b.source) };
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

// --- a board and a chart (G4) ---

/** A new board's columns, until the owner names their own. */
export const BOARD_START: { value: string; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "doing", label: "Doing" },
  { value: "done", label: "Done" },
];

/** The column next to one (by -1 to the left, 1 to the right), for moving a card by the keyboard; the same at either end. */
export function laneStep(lanes: string[], at: string, by: -1 | 1): string {
  const i = lanes.indexOf(at);
  if (i < 0) return lanes[0] ?? at;
  return lanes[Math.min(lanes.length - 1, Math.max(0, i + by))] ?? at;
}

/** What a screen reader is told while a card is moved by the keyboard. */
export function moveWords(step: "picked" | "over" | "dropped" | "left", card: string, lane: string, from?: string): string {
  switch (step) {
    case "picked":
      return `${card} picked up, in ${lane}. Left and right arrows choose a column, Enter moves it there, Escape leaves it.`;
    case "over":
      return lane === from ? `${card} stays in ${lane}.` : `Move ${card} to ${lane}?`;
    case "dropped":
      return `Moving ${card} to ${lane}.`;
    case "left":
      return `${card} left in ${lane}.`;
  }
}

/** Where each bar or point is drawn in a plot of this width and height,
 * from the server's places on its scale (share, zero): nothing is worked
 * out here but the pixels. */
export function chartLayout(points: ChartPoint[], zero: number, width: number, height: number) {
  const n = Math.max(1, points.length);
  const band = width / n;
  const bar = Math.max(4, Math.min(24, band - 8));
  const y = (share: number) => height - share * height;
  return points.map((p, i) => {
    const cx = band * i + band / 2;
    const top = Math.min(y(p.share), y(zero));
    const bottom = Math.max(y(p.share), y(zero));
    return { cx, x: cx - bar / 2, w: bar, y: y(p.share), top, h: bottom - top, band, up: p.share >= zero };
  });
}

/** A bar's outline: 4px round at its data end, square on its foot (pages G4, as the money screens' bars). */
export function barPath(x: number, top: number, w: number, h: number, up: boolean): string {
  if (h <= 0) return "";
  const r = Math.min(4, w / 2, h);
  if (up) {
    return `M${x},${top + h}V${top + r}Q${x},${top} ${x + r},${top}H${x + w - r}Q${x + w},${top} ${x + w},${top + r}V${top + h}Z`;
  }
  const b = top + h;
  return `M${x},${top}V${b - r}Q${x},${b} ${x + r},${b}H${x + w - r}Q${x + w},${b} ${x + w},${b - r}V${top}Z`;
}

/** Where a board's move value comes from: the column the card goes to, or as a button's (the card's column, words, asked). */
export type MoveFrom = { from: "to" } | ValueFrom;

/** A move's value read back for the editor: null when written another way (a file). */
export function moveFrom(v: unknown): MoveFrom | null {
  return v === "=to" ? { from: "to" } : valueFrom(v);
}

/** A move's value as the document keeps it. */
export function moveExpr(v: MoveFrom): string | undefined {
  return v.from === "to" ? "=to" : valueExpr(v);
}

/** A move tool's first values: an input named for the board's column (or a status) gets the column the card goes to; one named for the card's number gets it. */
export function moveInputs(tool: ChoiceTool | undefined, column: string): Record<string, unknown> | undefined {
  const out: Record<string, unknown> = {};
  for (const i of tool?.inputs ?? []) {
    if (i.name === column || /status|state|stage|column/.test(i.name)) out[i.name] = "=to";
    else if (/^(id|ref|row|number|ticket)$/.test(i.name)) out[i.name] = "=row.id";
  }
  return Object.keys(out).length ? out : undefined;
}
