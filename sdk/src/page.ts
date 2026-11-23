// Pages in code (docs/spec/pages.md §12, docs/contracts/page-v1.md): the
// page/v1 document's types, and a builder that writes one. Like the
// workflow builder, it mirrors the document one to one; values may be
// words, raw CEL strings ("=row.title"), or arrow functions over what a
// page's expressions read (row, filters, item, outputs), compiled to CEL.
//
//   export default page({ name: "tickets", title: "Tickets", roles: ["support"] },
//     figure("open", { label: "Open", value: "count", source: { table: "tickets", filters: [{ column: "status", op: "eq", value: "open" }] } }),
//     list("list", { source: { table: "tickets" }, item: { title: ({ row }) => row.title },
//       actions: [toolAction("Assign to me", "assign-ticket", { ref: ({ row }) => row.id })] }));
//
// A page runs nothing: the server draws it (GET /v1/pages/{name}/view) and
// checks it against the business when it is saved and published.

import { compileFunction, ExpressionError } from "./expr.js";

/** The format a page document names. */
export const PAGE_FORMAT = "page/v1";

/** What a page's expressions may read (pages §5): never steps, secrets or the environment. */
export const PAGE_ROOTS = ["row", "item", "outputs", "filters"] as const;

/** What a page's expressions read (pages §5): a table's row, a tool's item
 * and outputs, and the page's filter values. Values are untyped. */
export interface PageContext {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  row: any;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  item: any;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  outputs: any;
  filters: Record<string, string>;
}

/** Words, a CEL string, or an arrow function compiled to CEL. */
export type PageValue = string | `=${string}` | ((ctx: PageContext) => unknown);

export type Tone = "plain" | "good" | "warn" | "bad";
export type PageIcon = "tables" | "orders" | "money" | "invoices" | "members" | "box" | "tag" | "clock" | "check" | "bell" | "star" | "place" | "forms" | "reports" | "store" | "grid";
export type Area = "storefront" | "orders" | "stock" | "money" | "links" | "schools";
export type RecordViewName = "orders" | "invoices" | "deliveries" | "customers" | "staff";
export type ConditionOp = "eq" | "neq" | "lt" | "lte" | "gt" | "gte" | "contains" | "is_null" | "not_null";

export interface Condition {
  column: string;
  op: ConditionOp;
  value?: string | number | boolean;
}

/** What feeds a part: a data table, one of Taskiem's record views, or a
 * made tool that answers at once (its output). One of them. */
export interface PageSource {
  table?: string;
  records?: RecordViewName;
  tool?: string;
  inputs?: Record<string, unknown>;
  output?: string;
  filters?: Condition[];
  order?: { column: string; desc?: boolean }[];
  uses?: string[];
}

export interface PageFilter {
  id: string;
  type: "search" | "choice" | "dates" | "mine";
  label?: string;
  column?: string;
  columns?: string[];
  options?: { value: string; label?: string }[];
}

/** A button (pages §7): a tool's form filled in, a signal to a run, or an approval. */
export interface PageAction {
  label: string;
  tool?: string;
  inputs?: Record<string, unknown>;
  once?: boolean;
  signal?: { workflow: string; name: string; run?: string; to?: string; input?: Record<string, unknown> };
  approve?: { workflow: string; run: string; step: string };
  when?: string;
}

export interface PageItem {
  title: string;
  sub?: string;
  state?: { text: string; tone?: Tone; tones?: Record<string, Tone> };
}

export interface PageDetail {
  title: string;
  lines?: { label: string; value: string; naira?: boolean }[];
}

export type PageBlock =
  | { id: string; type: "heading"; title: string; text?: string }
  | {
      id: string;
      type: "figure";
      label: string;
      value: "count" | "sum" | "output";
      source: PageSource;
      of?: string;
      naira?: boolean;
      tone?: Tone;
      lead?: boolean;
      alert?: boolean;
      change?: { column: string; period: "day" | "week" | "month" };
    }
  | {
      id: string;
      type: "list";
      source: PageSource;
      item: PageItem;
      title?: string;
      size?: "full" | "half";
      group?: { column: string; order?: string[] };
      detail?: PageDetail;
      actions?: PageAction[];
      empty?: string;
    }
  | {
      id: string;
      type: "table";
      source: PageSource;
      columns: { column: string; label?: string; naira?: boolean; edit?: boolean }[];
      title?: string;
      size?: "full" | "half";
      detail?: PageDetail;
      actions?: PageAction[];
      empty?: string;
    }
  | { id: string; type: "buttons"; actions: PageAction[]; title?: string }
  | { id: string; type: "status"; workflow: string; title?: string };

/** A page/v1 document. */
export interface PageDocument {
  page: typeof PAGE_FORMAT;
  name: string;
  title: string;
  about?: string;
  icon?: PageIcon;
  roles?: string[];
  filters?: PageFilter[];
  blocks: PageBlock[];
  place?: { area: Area };
}

// --------------------------------------------------------------- values

/** A value as the document writes it: words as they are, a function compiled to "=CEL". */
export function pageValue(v: PageValue, path = "value"): string {
  if (typeof v === "function") {
    try {
      return "=" + compileFunction(v.toString(), PAGE_ROOTS);
    } catch (e) {
      if (e instanceof ExpressionError) throw new ExpressionError(`${path}: ${e.message}`);
      throw e;
    }
  }
  return v;
}

function values(m: Record<string, unknown> | undefined, path: string): Record<string, unknown> | undefined {
  if (m === undefined) return undefined;
  const out: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(m)) {
    if (v !== undefined) Object.defineProperty(out, k, { value: typeof v === "function" ? pageValue(v as PageValue, `${path}.${k}`) : v, enumerable: true, writable: true, configurable: true });
  }
  return out;
}

const word = (v: PageValue | undefined, path: string) => (v === undefined ? undefined : pageValue(v, path));

function clean<T>(o: object): T {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined)) as T;
}

// ---------------------------------------------------------------- parts

/** A heading: a title and a line of words. */
export function heading(id: string, title: string, text?: string): PageBlock {
  return clean({ id, type: "heading" as const, title, text });
}

/** A figure: a count or a sum over a table or a record view, or a tool's output. */
export function figure(id: string, f: Omit<Extract<PageBlock, { type: "figure" }>, "id" | "type">): PageBlock {
  return clean({ id, type: "figure" as const, ...f });
}

type ItemIn = { title: PageValue; sub?: PageValue; state?: { text: PageValue; tone?: Tone; tones?: Record<string, Tone> } };
type DetailIn = { title: PageValue; lines?: { label: string; value: PageValue; naira?: boolean }[] };

function item(i: ItemIn, path: string): PageItem {
  return clean({
    title: pageValue(i.title, `${path}.title`),
    sub: word(i.sub, `${path}.sub`),
    state: i.state && clean({ text: pageValue(i.state.text, `${path}.state.text`), tone: i.state.tone, tones: i.state.tones }),
  });
}

function detail(d: DetailIn | undefined, path: string): PageDetail | undefined {
  if (!d) return undefined;
  return clean({
    title: pageValue(d.title, `${path}.title`),
    lines: d.lines?.map((l, i) => clean({ label: l.label, value: pageValue(l.value, `${path}.lines[${i}].value`), naira: l.naira })),
  });
}

/** A list: rows, each a title, a line and a state; grouped, with a card and buttons. */
export function list(
  id: string,
  l: { source: PageSource; item: ItemIn; title?: string; size?: "full" | "half"; group?: { column: string; order?: string[] }; detail?: DetailIn; actions?: PageAction[]; empty?: string },
): PageBlock {
  return clean({ id, type: "list" as const, ...l, item: item(l.item, `${id}.item`), detail: detail(l.detail, `${id}.detail`) });
}

/** A table: rows in up to six columns, a yes/no one ticked in place with edit. */
export function table(
  id: string,
  t: { source: PageSource; columns: { column: string; label?: string; naira?: boolean; edit?: boolean }[]; title?: string; size?: "full" | "half"; detail?: DetailIn; actions?: PageAction[]; empty?: string },
): PageBlock {
  return clean({ id, type: "table" as const, ...t, detail: detail(t.detail, `${id}.detail`) });
}

/** The page's own buttons, three at most. */
export function buttons(id: string, actions: PageAction[], title?: string): PageBlock {
  return clean({ id, type: "buttons" as const, actions, title });
}

/** A run's state in plain words, of a workflow the page declares. */
export function status(id: string, workflow: string, title?: string): PageBlock {
  return clean({ id, type: "status" as const, workflow, title });
}

// --------------------------------------------------------------- actions

type ActionOpts = { when?: PageValue };

/** A made tool's form, filled in from the row (a masked value is asked, not filled). */
export function toolAction(label: string, tool: string, inputs?: Record<string, PageValue | number | boolean>, opts: ActionOpts & { once?: boolean } = {}): PageAction {
  return clean({ label, tool, inputs: values(inputs, `${label}.inputs`), once: opts.once, when: word(opts.when, `${label}.when`) });
}

/** A signal to one run of a workflow that declares it for pages ("pages": true on its signal step), after a Yes. */
export function signalAction(
  label: string,
  s: { workflow: string; name: string; run?: PageValue; to?: PageValue; input?: Record<string, PageValue | number | boolean> },
  opts: ActionOpts = {},
): PageAction {
  return clean({
    label,
    signal: clean({ workflow: s.workflow, name: s.name, run: word(s.run, `${label}.run`), to: word(s.to, `${label}.to`), input: values(s.input, `${label}.input`) }),
    when: word(opts.when, `${label}.when`),
  });
}

/** A run's approval at a step, with its own rules. */
export function approveAction(label: string, a: { workflow: string; run: PageValue; step: string }, opts: ActionOpts = {}): PageAction {
  return clean({ label, approve: { workflow: a.workflow, run: pageValue(a.run, `${label}.run`), step: a.step }, when: word(opts.when, `${label}.when`) });
}

// ------------------------------------------------------------------ page

/** A page/v1 document from its head and its parts. */
export function page(head: Omit<PageDocument, "page" | "blocks">, ...blocks: PageBlock[]): PageDocument {
  if (blocks.length === 0) throw new Error("a page needs at least one part");
  return clean({ page: PAGE_FORMAT, ...head, blocks });
}
