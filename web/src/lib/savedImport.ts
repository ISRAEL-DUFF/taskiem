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

/** What a plan saves, in a sentence. */
export function importPlanLine(p: Pick<ImportPlan, "rows">): string {
  const by = new Map<SavedKind, number>();
  for (const r of p.rows) by.set(r.kind, (by.get(r.kind) ?? 0) + 1);
  const parts = [...by.entries()].sort((a, b) => b[1] - a[1]).map(([k, n]) => count(n, ...(MANY[k] ?? ["thing", "things"])));
  // One kind: "This file saves 3 people."; more: each kind's count.
  if (parts.length === 1) return `This file saves ${parts[0] ?? ""}.`;
  return `This file saves ${count(p.rows.length, "thing", "things")}: ${parts.join(", ")}.`;
}

/** The button that saves a plan. */
export const importSaveWords = (n: number) => `Save ${count(n, "thing", "things")}`;

/** What saving did, in a sentence. */
export function importDoneLine(d: Pick<ImportDone, "added" | "failed">): string {
  const added = d.added.length ? `Saved ${count(d.added.length, "thing", "things")}: ${d.added.map((n) => `@${n}`).join(", ")}.` : "Nothing was saved.";
  return d.failed.length ? `${added} ${count(d.failed.length, "line was", "lines were")} not saved.` : added;
}

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
