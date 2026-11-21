// Saved things from a file (saved objects S3; POST /v1/saved/import): a
// business's people, places, accounts and texts from a spreadsheet saved
// as CSV, checked whole and shown, then saved, as its students are
// imported. What is here words the plan, what saving did, and a refusal.
import { plainMessage } from "./invoices";
import { KIND_WORDS, type SavedKind } from "./saved";

/** One line of the file as it would be saved; its number masked in hint. */
export interface ImportRow {
  line: number;
  name: string;
  title: string;
  kind: SavedKind;
  hint: string;
  /** Something to look at before saving, in words; saved all the same. */
  warning?: string;
  /** Saved already as it is: skipped, not saved again. */
  skip?: boolean;
}

/** The answer without confirm: the plan, and the file's hash to save it by. */
export interface ImportPlan {
  saved: false;
  hash: string;
  total: number;
  rows: ImportRow[];
}

/** The answer with confirm: what was added, and the lines that were not. */
export interface ImportDone {
  saved: true;
  hash: string;
  total: number;
  added: string[];
  failed: { line: number; name: string; message: string }[];
  /** Names saved already as they are, skipped. */
  skipped?: string[];
  /** Bank accounts saved whose names were not checked yet. */
  unchecked?: { line: number; name: string; problem: string }[];
  /** Lines not reached before saving stopped: the file sent again saves them. */
  left?: { line: number; name: string }[];
}

export interface ImportRefusal {
  message: string;
  problems: { line: number; message: string }[];
  total: number;
}

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** A line's kind in a word or two: "A bank account". */
export const importKindWord = (k: string) => {
  const w = KIND_WORDS[k as SavedKind]?.one ?? "a thing";
  return w.charAt(0).toUpperCase() + w.slice(1);
};

/** The kinds a plan holds, most first: "2 people, 1 bank account". */
const MANY: Record<SavedKind, [string, string]> = {
  bank_account: ["bank account", "bank accounts"],
  person: ["person", "people"],
  place: ["place", "places"],
  text: ["text", "texts"],
  provider_account: ["account with a provider", "accounts with a provider"],
  file: ["file", "files"],
};

/** The rows a plan saves: all but those saved already. */
export const importToSave = (rows: ImportRow[]) => rows.filter((r) => !r.skip);

/** What a plan saves, in a sentence; the lines saved already after it. */
export function importPlanLine(p: Pick<ImportPlan, "rows">): string {
  const rows = importToSave(p.rows);
  const skipped = p.rows.length - rows.length;
  const after = skipped ? ` ${count(skipped, "line is", "lines are")} saved already: skipped.` : "";
  if (rows.length === 0) return `Everything in this file is saved already: nothing to save.`;
  const by = new Map<SavedKind, number>();
  for (const r of rows) by.set(r.kind, (by.get(r.kind) ?? 0) + 1);
  const parts = [...by.entries()].sort((a, b) => b[1] - a[1]).map(([k, n]) => count(n, ...(MANY[k] ?? ["thing", "things"])));
  // One kind: "This file saves 3 people."; more: each kind's count.
  if (parts.length === 1) return `This file saves ${parts[0] ?? ""}.${after}`;
  return `This file saves ${count(rows.length, "thing", "things")}: ${parts.join(", ")}.${after}`;
}

/** The button that saves a plan. */
export const importSaveWords = (n: number) => `Save ${count(n, "thing", "things")}`;

/** What saving did, in a sentence. */
export function importDoneLine(d: Pick<ImportDone, "added" | "failed" | "skipped" | "left">): string {
  const added = d.added.length ? `Saved ${count(d.added.length, "thing", "things")}: ${d.added.map((n) => `@${n}`).join(", ")}.` : "Nothing was saved.";
  const failed = d.failed.length ? ` ${count(d.failed.length, "line was", "lines were")} not saved.` : "";
  const skipped = d.skipped?.length ? ` ${count(d.skipped.length, "was", "were")} saved already: skipped.` : "";
  const left = d.left?.length ? ` ${count(d.left.length, "line is", "lines are")} not saved yet: send the file again.` : "";
  return `${added}${failed}${skipped}${left}`;
}

/** The largest file the server reads. */
export const IMPORT_MAX_BYTES = 256 * 1024;

/** Why a chosen file is not sent, or null. */
export const importSizeProblem = (size: number) => (size > IMPORT_MAX_BYTES ? "The file is too big: up to 256 KB." : null);

/** A file's text: UTF-16 by its byte order mark (Excel's "Unicode text",
 * often tab-separated, which the server reads), else UTF-8, a UTF-8 mark
 * dropped. */
export function decodeImportFile(bytes: Uint8Array): string {
  if (bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xfe) return new TextDecoder("utf-16le").decode(bytes);
  if (bytes.length >= 2 && bytes[0] === 0xfe && bytes[1] === 0xff) return new TextDecoder("utf-16be").decode(bytes);
  return new TextDecoder("utf-8").decode(bytes);
}

/** Reads a chosen file as it is now (read again each time it is sent). */
export async function readImportFile(f: Blob): Promise<string> {
  return decodeImportFile(new Uint8Array(await f.arrayBuffer()));
}

/** A 409 import_changed: the file is not the one checked; check it again. */
export const importChanged = (status: number, body: Record<string, unknown> | undefined) => status === 409 && body?.code === "import_changed";

/** A 422 import_unreadable answer as the page shows it, or null when the error is something else. */
export function importRefusal(status: number, body: Record<string, unknown> | undefined, message: string): ImportRefusal | null {
  if (status !== 422 || !body || body.code !== "import_unreadable") return null;
  const problems = Array.isArray(body.problems)
    ? body.problems
        .filter((p): p is { line: number; message: string } => !!p && typeof p === "object" && typeof (p as { line?: unknown }).line === "number")
        .map((p) => ({ line: p.line, message: String(p.message ?? "") }))
    : [];
  const total = typeof body.total === "number" ? body.total : problems.length;
  return { message: plainMessage(String(body.error ?? message)), problems, total };
}

/** The columns the file is read by, in words, for the page's hint. */
export const IMPORT_COLUMNS =
  "The first line names the columns: a title for each thing, then what it holds, such as bank and account number, phone and email, street and area, or text. Add name for its @name and kind when it is not clear.";

/** A small file to start from, with one of each common kind. */
export const IMPORT_EXAMPLE =
  [
    "title,name,kind,bank,account_number,phone,email,street,area,landmark,text,note",
    "Bayo Rice Supplies,bayo,bank_account,GTBank,0123456789,,,,,,,Pays for rice",
    "Bisi Okafor,bisi,person,,,08031234567,bisi@example.com,,,,,",
    "Ikeja shop,shop,place,,,08037654321,,12 Allen Avenue,Ikeja,Opposite the bank,,",
    "Price list,price-list,text,,,,,,,,Rice ₦75000 a bag; beans ₦60000 a bag,",
  ].join("\n") + "\n";

/** The example as a link's address, to download. */
export const importExampleHref = () => `data:text/csv;charset=utf-8,${encodeURIComponent(IMPORT_EXAMPLE)}`;
