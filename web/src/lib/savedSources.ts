// A business's saved things as values a workflow step can use (saved
// objects S3): saved.price_list.body is @price-list's text, read when a run
// starts. The fields are those a run reads (api/saved_runs.go
// savedRunFields); a made-up kind's are its own, but for those never to
// be shared and files. A person's own saved things are never a
// business's workflow's.
import type { Source } from "./plaincanvas";
import type { KindList, MadeKind, SavedKind, SavedList, SavedThing } from "./saved";

/** The group the step's pickers list saved things under. */
export const SAVED_GROUP = "Saved";

/** Each kind's fields a run reads, with their words, after the title. */
const RUN_FIELDS: Record<SavedKind, [string, string][]> = {
  text: [
    ["body", "Text"],
    ["note", "Note"],
  ],
  person: [
    ["phone", "Phone"],
    ["email", "Email"],
    ["address", "Address"],
    ["birthday", "Birthday"],
    ["note", "Note"],
  ],
  place: [
    ["address", "Address"],
    ["line", "Street and number"],
    ["area", "Area"],
    ["landmark", "Landmark"],
    ["phone", "Phone"],
    ["note", "Note"],
  ],
  bank_account: [
    ["account_name", "Account name"],
    ["account_number", "Account number"],
    ["bank_name", "Bank"],
    ["bank_code", "Bank code"],
    ["note", "Note"],
  ],
  provider_account: [
    ["provider", "Provider"],
    ["number", "Number with them"],
    ["name_on_it", "Name on the account"],
    ["type", "What it is"],
    ["note", "Note"],
  ],
  file: [
    ["file_name", "File name"],
    ["file_type", "File type"],
    ["note", "Note"],
  ],
};

/** How a step reads a saved thing's field: saved.price_list.body ("_"
 * reads as "-"), or saved["2go"].body for a name that starts with a
 * number, which a dot cannot follow. */
export function savedExpr(name: string, field: string): string {
  return /^[a-z]/.test(name) ? `=saved.${name.replaceAll("-", "_")}.${field}` : `=saved["${name}"].${field}`;
}

/** Reads the business's saved things (never a person's own, never a
 * reveal) and its made-up kinds, as values; none when the list cannot be
 * read, so the other values still show. */
export async function loadSavedSources(get: <T>(path: string) => Promise<T>): Promise<Source[]> {
  try {
    const [list, kinds] = await Promise.all([get<SavedList>("/v1/saved"), get<KindList>("/v1/saved-kinds").catch(() => undefined)]);
    return savedSources(list.saved, kinds?.kinds ?? []);
  } catch {
    return [];
  }
}

/** The business's saved things as values, each field labelled
 * "@supplier · Account number", in the list's order. kinds are the
 * business's made-up kinds, for their fields. */
export function savedSources(things: SavedThing[], kinds: MadeKind[] = []): Source[] {
  const out: Source[] = [];
  for (const x of things) {
    if (x.owner === "mine") continue;
    // Every value is read as words (a made-up kind's number too).
    const add = (field: string, words: string) => out.push({ label: `@${x.name} · ${words}`, expr: savedExpr(x.name, field), group: SAVED_GROUP });
    add("title", "Title");
    if (x.kind === "custom") {
      const def = kinds.find((k) => k.id === x.kind_id);
      for (const f of def?.fields ?? []) {
        if (!f.key || f.never_share || f.type === "file") continue;
        add(f.key, f.label);
      }
      continue;
    }
    for (const [field, words] of RUN_FIELDS[x.kind] ?? []) add(field, words);
  }
  return out;
}
