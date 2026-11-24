// Pages G5 (docs/spec/pages.md §10, 3 and 4; §18d): starters, Describe it,
// and saved things of one kind as a part's source. The server makes and
// checks everything; this file is the shapes the web reads and the words
// it says them in.

import type { Part } from "./pages";

/** A starter (GET /v1/page-starters). */
export interface PageStarter {
  id: string;
  title: string;
  about: string;
  icon: string;
  /** The page's title. */
  page: string;
  /** What it makes, a line each. */
  makes: string[];
  /** The kinds of part its page has, in order. */
  parts: string[];
}

/** What making a starter or a described page made. */
export interface BundleMade {
  page: string;
  title: string;
  tables: { name: string; title: string; asked: string; changed: boolean; id?: string }[];
  tools: { command: string; asked: string; changed: boolean; name: string; workflow: string; state: string }[];
  published: boolean;
  waiting?: string;
  problems: { path: string; message: string }[];
}

/** A page Describe it drafted (GET /v1/page-builds/{id}). */
export interface PageBuild {
  id: string;
  status: "running" | "proposed" | "failed" | "saved";
  stage: string;
  error: string | null;
  goal: string;
  page_name: string | null;
  proposal: {
    summary: string;
    problems: { path: string; message: string }[];
    rounds: number;
  } | null;
  review?: { makes: string[]; uses: string[]; shows: string[]; title: string; parts: string[] };
}

/** A kind of saved things the business made, for a part (GET /v1/page-choices). */
export interface ChoiceKind {
  name: string;
  fields: { key: string; label: string; type: string; private: boolean }[];
}

const PART_WORDS: Record<string, string> = {
  heading: "a heading",
  figure: "figures",
  list: "a list",
  table: "a table",
  buttons: "buttons",
  status: "where a run is",
  board: "a board",
  chart: "a chart",
};

/** A page's parts in a line: "figures, buttons, a board". */
export function partsWords(parts: string[]): string {
  const seen: string[] = [];
  for (const p of parts) {
    const w = PART_WORDS[p] ?? p;
    if (!seen.includes(w)) seen.push(w);
  }
  if (seen.length <= 1) return seen.join("");
  return `${seen.slice(0, -1).join(", ")} and ${seen[seen.length - 1]}`;
}

/** What a build is doing, in words. */
export function buildStage(b: PageBuild): string {
  if (b.status === "failed") return b.error ?? "Describe it could not draft the page.";
  if (b.status !== "running") return "";
  if (b.stage.startsWith("correct")) return "Putting the draft right…";
  if (b.stage === "validate") return "Checking the draft…";
  return "Drafting the page…";
}

/** What was made, in a sentence or two: a name taken is said, and why it is not in use yet. */
export function madeWords(m: BundleMade): string[] {
  const out: string[] = [];
  for (const t of m.tables) if (t.changed) out.push(`A table called ${t.asked} was there already, so this one is ${t.name}.`);
  for (const t of m.tools) if (t.changed) out.push(`/${t.asked} is taken, so it is called with /${t.command}.`);
  out.push(m.published ? `${m.title} is ready, with its tools.` : m.waiting || `${m.title} is saved as a draft.`);
  return out;
}

/** The date fields of a kind a list may be put in order by (never a private one). */
export const comingFields = (k: ChoiceKind | undefined) => (k?.fields ?? []).filter((f) => f.type === "date" && !f.private);

/** A new list of a kind's saved things: in the order its first date comes, each saying when. */
export function savedListPart(k: ChoiceKind | undefined, id: string): Part {
  const date = comingFields(k)[0];
  const lines = (k?.fields ?? []).filter((f) => f.type !== "file").slice(0, 11).map((f) => ({ label: f.label, value: `=row.${f.key}` }));
  return {
    id,
    type: "list",
    title: k ? `${k.name}s` : "Saved things",
    source: { saved: k?.name ?? "", ...(date ? { coming: date.key } : {}) },
    item: { title: "=row.title", ...(date ? { state: { text: "=row.coming" } } : {}) },
    detail: { title: "=row.title", lines },
    empty: "None saved yet",
  };
}

/** A saved part in the editor's list of parts. */
export function savedPartWords(p: Part, kinds: ChoiceKind[]): string {
  const k = kinds.find((x) => x.name.toLowerCase() === (p.source?.saved ?? "").toLowerCase());
  const date = k?.fields.find((f) => f.key === p.source?.coming);
  const what = p.type === "figure" ? `How many saved ${p.source?.saved}` : `${p.type === "table" ? "Table" : "List"} of saved ${p.source?.saved}`;
  const order = p.source?.coming ? `, in the order ${date?.label ?? p.source.coming.replace(/_/g, " ")} comes` : ", by name";
  const within = p.source?.within ? `, due within ${p.source.within} days` : "";
  return what + order + within;
}
