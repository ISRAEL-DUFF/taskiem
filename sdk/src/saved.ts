// Saved things in code (saved objects §6.4, S3; build plan H6): a step
// reads the business's saved things through the `saved` root, as the
// server's saved.* expressions do. Each is read when a run starts, so a
// changed thing is used from the next run; a person's own saved things are
// never a business's workflow's. These are the fields a run is given for
// each kind Taskiem knows (api/saved_runs.go): "" when the thing holds
// nothing, personal values sealed in the run's history.

/** What every saved thing has in a run. */
export interface SavedBase {
  name: string;
  title: string;
  kind: string;
  note: string;
}

export interface SavedPerson extends SavedBase {
  kind: "person";
  phone: string;
  email: string;
  address: string;
  birthday: string;
}

export interface SavedPlace extends SavedBase {
  kind: "place";
  line: string;
  area: string;
  landmark: string;
  phone: string;
  /** The line and the area, together. */
  address: string;
}

export interface SavedText extends SavedBase {
  kind: "text";
  body: string;
}

export interface SavedBankAccount extends SavedBase {
  kind: "bank_account";
  bank_name: string;
  bank_code: string;
  /** The bank's own name for the account; never the number. */
  account_name: string;
}

export interface SavedProviderAccount extends SavedBase {
  kind: "provider_account";
  type: string;
  provider: string;
  number: string;
  name_on_it: string;
}

export interface SavedFile extends SavedBase {
  kind: "file";
  file_name: string;
  file_type: string;
  /** A link to the file, made when the run starts and good for a week:
   * what notify's file sends (`file: ({ saved }) => saved.price_list`). */
  file: string;
}

/** A made-up kind: its fields by key (a field named as one every thing
 * has is read as field_<key>), sensitive and never-shared ones left out. */
export interface SavedCustom extends SavedBase {
  kind: "custom";
  [field: string]: string;
}

/** A business's saved thing as a run reads it, by its kind. */
export type SavedThing = SavedPerson | SavedPlace | SavedText | SavedBankAccount | SavedProviderAccount | SavedFile | SavedCustom;

/** A saved thing whose kind a step does not check: any field reads as
 * words ("" when it has none), as the server gives every field its kind
 * has. Narrow to a SavedThing by its kind to have only that kind's. */
export type SavedAny = SavedBase & { readonly [field: string]: string };

/** The fields a run is given for each kind Taskiem knows, beside name,
 * title, kind and note. */
export const SAVED_FIELDS = {
  person: ["phone", "email", "address", "birthday"],
  place: ["line", "area", "landmark", "phone", "address"],
  text: ["body"],
  bank_account: ["bank_name", "bank_code", "account_name"],
  provider_account: ["type", "provider", "number", "name_on_it"],
  file: ["file_name", "file_type", "file"],
} as const;

const NAME = /^[a-z0-9][a-z0-9-]{1,31}$/;

/** A saved thing, or one of its fields, as an expression, by the name it
 * is saved under: savedRef("price-list", "body") is
 * "=saved.price_list.body", savedRef("2go") is '=saved["2go"]' (a name
 * that starts with a digit cannot follow a dot). */
export function savedRef(name: string, field?: string): `=${string}` {
  if (!NAME.test(name)) throw new Error(`"${name}" is not a saved thing's name: lowercase letters, digits and -, 2 to 32 characters`);
  const thing = /^[a-z]/.test(name) ? `saved.${name.replaceAll("-", "_")}` : `saved["${name}"]`;
  if (field !== undefined && !/^[a-z][a-z0-9_]*$/.test(field)) throw new Error(`"${field}" is not a field's key`);
  return `=${thing}${field === undefined ? "" : `.${field}`}`;
}
