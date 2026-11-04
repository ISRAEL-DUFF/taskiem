// Tables an AI draft proposes (docs/ai.md): the person reviews and may
// change them, and they are made when the draft is saved.

export const COLUMN_TYPES = ["text", "integer", "number", "boolean", "date", "datetime", "phone", "email", "json"] as const;

export interface ProposedColumn {
  name: string;
  title: string;
  type: string;
  required: boolean;
}

export interface ProposedTable {
  name: string;
  title: string;
  columns: ProposedColumn[];
}

/** A table under review: the proposal, and whether to make it. */
export interface ReviewedTable extends ProposedTable {
  create: boolean;
}

/** What became of a table sent with the draft. */
export interface TableResult {
  name: string;
  status: "created" | "exists" | "skipped" | "failed";
  error?: string;
}

const NAME = /^[a-z][a-z0-9_]{0,62}$/;
const RESERVED = ["id", "created_at", "updated_at"];

/** Why a reviewed table cannot be made as it stands ("" when it can). */
export function tableProblem(t: ProposedTable): string {
  if (!NAME.test(t.name)) return `The table name "${t.name}" must be lower-case letters, digits and underscores, starting with a letter.`;
  if (t.columns.length === 0) return `The table "${t.name}" needs at least one column.`;
  const seen = new Set<string>();
  for (const c of t.columns) {
    if (!NAME.test(c.name)) return `The column name "${c.name}" must be lower-case letters, digits and underscores, starting with a letter.`;
    if (RESERVED.includes(c.name)) return `Every table has "${c.name}" already: rename that column.`;
    if (seen.has(c.name)) return `The column "${c.name}" is there twice.`;
    if (!(COLUMN_TYPES as readonly string[]).includes(c.type)) return `The column "${c.name}" has an unknown type.`;
    seen.add(c.name);
  }
  return "";
}

/** The tables to send with the save: those ticked, as POST /v1/tables takes them. */
export function tablesToSave(tables: ReviewedTable[]): ProposedTable[] {
  return tables
    .filter((t) => t.create)
    .map((t) => ({ name: t.name, title: t.title, columns: t.columns.map((c) => ({ name: c.name, title: c.title, type: c.type, required: c.required })) }));
}

/** Results to show the person: anything not simply made. */
export function tableTroubles(results: TableResult[] | undefined): TableResult[] {
  return (results ?? []).filter((r) => r.status === "failed" || r.status === "skipped");
}

/**
 * A new workflow's id from its name: "Leave request" → wf_leaveRequest.
 * The first few words, camel-cased, so a sentence typed as the name still
 * makes a short id (wf_ and up to 40 letters or digits).
 */
export function workflowID(name: string): `wf_${string}` {
  const words = (name.match(/[A-Za-z0-9]+/g) ?? []).slice(0, 4);
  const camel = words.map((w, i) => (i === 0 ? w.toLowerCase() : w.charAt(0).toUpperCase() + w.slice(1).toLowerCase())).join("");
  const id = camel.slice(0, 40);
  return `wf_${id || "workflow"}`;
}
