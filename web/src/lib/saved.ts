import type { Suggestion } from "./mentions";

// Saved things (docs/spec/saved-objects.md; build plan S0, S1): a person's
// own and a business's saved accounts, people, places, texts and accounts
// with a provider, called up by @name. The server
// never sends an account number in full but to a reveal with a passkey;
// what is here words and checks what a person types before it is sent.

export interface SavedCheck {
  /** A bank account's name check; none for every other kind. */
  state: "checked" | "unchecked" | "none";
  account_name: string | null;
  via: string | null;
  at: string | null;
  /** Why it will not be checked until the account changes; null otherwise. */
  problem: string | null;
}

export interface SavedThing {
  id: string;
  owner: "mine" | "business";
  name: string;
  title: string;
  /** custom: one of the owner's own kinds (kind_id, kind_name). */
  kind: SavedKind | "custom";
  kind_id?: string;
  kind_name?: string;
  /** A made-up kind's values by field key, each sensitive one masked. */
  values?: Record<string, string>;
  hint: string;
  details: SavedDetails;
  check: SavedCheck;
  to_change: boolean;
  created_at: string;
  updated_at: string;
}

/** The kinds Taskiem knows (saved-objects §4.1), in the order the page
 * lists them. */
export type SavedKind = "bank_account" | "person" | "place" | "text" | "provider_account";

export const SAVED_KINDS: SavedKind[] = ["bank_account", "person", "place", "text", "provider_account"];

/** Each kind's fields that are not sensitive (a bank account's number only
 * masked). */
export interface SavedDetails {
  bank_name?: string;
  bank_code?: string;
  code_from?: string;
  account_number?: string;
  note?: string;
  phone?: string;
  email?: string;
  address?: string;
  birthday?: string;
  line?: string;
  area?: string;
  landmark?: string;
  body?: string;
  type?: ProviderType;
  provider?: string;
  number?: string;
  name_on_it?: string;
}

export type ProviderType = "electricity" | "tv" | "phone" | "internet" | "water" | "school" | "other";

export const PROVIDER_TYPES: { value: ProviderType; label: string }[] = [
  { value: "electricity", label: "Electricity" },
  { value: "tv", label: "TV" },
  { value: "phone", label: "Phone" },
  { value: "internet", label: "Internet" },
  { value: "water", label: "Water" },
  { value: "school", label: "School" },
  { value: "other", label: "Another" },
];

/** A kind as a chooser offers it: "A person", "An account with a provider". */
export const kindChoice = (k: SavedKind) => (k === "provider_account" ? "An account with a provider" : KIND_WORDS[k].one.replace(/^a /, "A "));

/** A kind as a filter names its things, and as one is called. */
export const KIND_WORDS: Record<SavedKind, { many: string; one: string; icon: string }> = {
  bank_account: { many: "Accounts", one: "a bank account", icon: "money" },
  person: { many: "People", one: "a person", icon: "members" },
  place: { many: "Places", one: "a place", icon: "place" },
  text: { many: "Texts", one: "a text", icon: "note" },
  provider_account: { many: "Bills", one: "an account with a provider", icon: "invoices" },
};

/** The fields a form for kind offers, other than the bank account's, each
 * with its label and hint, in order. */
export const KIND_FIELDS: Record<Exclude<SavedKind, "bank_account">, { key: keyof SavedDetails; label: string; hint?: string; need?: string; long?: boolean }[]> = {
  person: [
    { key: "phone", label: "Phone", hint: "Optional." },
    { key: "email", label: "Email", hint: "Optional." },
    { key: "address", label: "Address", hint: "Optional." },
    { key: "birthday", label: "Birthday", hint: "Optional, like 1970-03-21." },
  ],
  place: [
    { key: "line", label: "Street and number", need: "Give the place its street and number." },
    { key: "area", label: "Area", hint: "Optional. Such as Yaba." },
    { key: "landmark", label: "Landmark", hint: "Optional. Something a rider can find." },
    { key: "phone", label: "Phone for the rider", hint: "Optional." },
  ],
  text: [{ key: "body", label: "The text", hint: "Up to 4,000 characters. It goes into your message to change before sending.", need: "Write the text.", long: true }],
  provider_account: [
    { key: "provider", label: "Provider", hint: "Such as Ikeja Electric, DSTV or MTN.", need: "Give the provider's name, like Ikeja Electric or DSTV." },
    { key: "number", label: "Number with them", hint: "A meter, smartcard, phone or student number.", need: "Give the number you have with them." },
    { key: "name_on_it", label: "Name on the account", hint: "Optional." },
  ],
};

/** What is wrong with a form's details for kind, or null. */
export function detailsProblem(kind: Exclude<SavedKind, "bank_account">, d: SavedDetails): string | null {
  for (const f of KIND_FIELDS[kind]) {
    if (f.need && !String(d[f.key] ?? "").trim()) return f.need;
  }
  if (kind === "provider_account" && !d.type) return "Choose what the provider is.";
  if (kind === "text" && (d.body ?? "").length > 4000) return "A text is up to 4,000 characters.";
  return null;
}

/** A field of a kind the owner makes (saved objects S1, part 5). */
export type FieldType = "text" | "number" | "money" | "date" | "phone" | "email" | "choice";

export interface KindField {
  /** Made from the label once by the server, then kept. */
  key?: string;
  label: string;
  type: FieldType;
  required?: boolean;
  /** Sealed, masked in lists, shown in full only with a passkey. */
  sensitive?: boolean;
  /** Kept out of every card. */
  never_share?: boolean;
  options?: string[];
}

/** A kind the owner made: "Car", its fields, how many things are of it. */
export interface MadeKind {
  id: string;
  name: string;
  fields: KindField[];
  count: number;
}

export interface KindList {
  kinds: MadeKind[];
  starters: { name: string; fields: KindField[] }[];
  limit: number;
  to_change: boolean;
}

export const FIELD_TYPES: { value: FieldType; label: string }[] = [
  { value: "text", label: "Words" },
  { value: "number", label: "A number" },
  { value: "money", label: "Money (₦)" },
  { value: "date", label: "A date" },
  { value: "phone", label: "A phone" },
  { value: "email", label: "An email" },
  { value: "choice", label: "A choice from a list" },
];

/** The kinds a person's own or a business's are made at. */
export const savedKindsBase = (mine: boolean) => (mine ? "/v1/me/saved-kinds" : "/v1/saved-kinds");

/** A value as a person reads it: money as naira, a date as a day; a
 * sensitive one as the server masked it. */
export function shownValue(f: KindField, v: string): string {
  if (v.startsWith("••")) return v;
  if (f.type === "money") {
    const [whole, frac] = v.split(".");
    return `₦${(whole ?? "").replace(/\B(?=(\d{3})+(?!\d))/g, ",")}${frac ? `.${frac}` : ""}`;
  }
  if (f.type === "date" && /^\d{4}-\d{2}-\d{2}$/.test(v)) {
    return new Date(`${v}T00:00:00`).toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric" });
  }
  return v;
}

/** What is wrong with a made-up kind's values in a form, or null: each
 * required field filled (a sensitive one kept as it was counts). */
export function valuesProblem(fields: KindField[], v: Record<string, string>, had: Record<string, string> = {}): string | null {
  for (const f of fields) {
    if (f.required && !(v[f.key ?? ""] ?? "").trim() && !had[f.key ?? ""]) return `Give it ${f.label.charAt(0).toLowerCase()}${f.label.slice(1)}.`;
  }
  return null;
}

/** What is wrong with a kind in its form, or null. */
export function kindProblem(name: string, fields: KindField[]): string | null {
  if (!name.trim()) return "Give the kind a name, such as Car or Generator.";
  if (fields.length === 0) return "Give the kind at least one field.";
  if (fields.length > 30) return "A kind has up to 30 fields.";
  const seen = new Set<string>();
  for (const f of fields) {
    const l = f.label.trim().toLowerCase();
    if (!l) return "Give each field a label.";
    if (seen.has(l)) return `Two fields are called ${f.label.trim()}: give each its own label.`;
    seen.add(l);
    if (f.type === "choice" && (f.options ?? []).filter((o) => o.trim()).length < 2) return `Give ${f.label.trim()} at least 2 choices.`;
  }
  return null;
}

export interface SavedList {
  saved: SavedThing[];
  limit: number;
  can_check?: boolean;
}

export interface Bank {
  code: string;
  name: string;
}

export interface BankList {
  banks: Bank[];
  from: string;
}

export interface SavedProvider {
  connected: { channel: string; name: string; wallet: boolean } | null;
  owner: boolean;
  wallet?: { available: boolean; plan: boolean; plan_note?: string };
  providers?: { id: string; name: string }[];
  words?: string;
}

/** The API's base for one owner's saved things. */
export const savedBase = (mine: boolean) => (mine ? "/v1/me/saved" : "/v1/saved");

/** A name as the person types it, as the server keeps it: no @,
 * lowercase, spaces as "-". */
export function cleanName(typed: string): string {
  return typed
    .trim()
    .toLowerCase()
    .replace(/^@/, "")
    .replace(/[\s_]+/g, "-");
}

/** What is wrong with a typed name, in plain words, or null. */
export function nameProblem(typed: string): string | null {
  const n = cleanName(typed);
  if (!n) return "Give it a short name to call it up by, like @mum or @bayo.";
  if (n.length < 2 || n.length > 32) return "A name is 2 to 32 letters or numbers, like @mum or @bola-stores.";
  if (!/^[a-z0-9][a-z0-9-]*[a-z0-9]$/.test(n) || n.includes("--")) return 'A name uses only letters, numbers and "-" between them, like @bola-stores.';
  return null;
}

/** An account number as typed, without spaces, dots or dashes. */
export const cleanNumber = (typed: string) => typed.replace(/[\s.-]/g, "");

/** What is wrong with a typed account number, or null. */
export function numberProblem(typed: string): string | null {
  return /^\d{10}$/.test(cleanNumber(typed)) ? null : "An account number is 10 digits.";
}

/** The line under a saved account's title: its @name, bank and masked
 * number ("@bayo · GTBank ••6789"). */
export const savedSub = (x: SavedThing) => (x.hint ? `@${x.name} · ${x.hint}` : `@${x.name}`);

/** What a row says of the bank's name for the account. */
export function checkLine(x: SavedThing): { words: string; tone: "ok" | "warn" } {
  if (x.check.state === "checked" && x.check.account_name) return { words: `Bank's name: ${x.check.account_name}`, tone: "ok" };
  if (x.check.problem) return { words: x.check.problem, tone: "warn" };
  return { words: "Name not checked yet", tone: "warn" };
}

/** A bank's name as searched: lowercase, without "Bank", "PLC" or
 * punctuation, so "gtb", "guaranty" and "Guaranty Trust" all find it. */
const bankWords = (s: string) =>
  s
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((w) => w && !["bank", "plc", "limited", "ltd", "of", "for"].includes(w));

/** The banks whose name has every word typed at the start of one of its
 * words (or, three letters or more, within the whole name run together,
 * for "gtbank"), the chosen one
 * always kept so the picker still shows it. Empty search: all of them. */
export function filterBanks(banks: Bank[], typed: string, chosen = ""): Bank[] {
  const want = bankWords(typed);
  if (want.length === 0) return banks;
  return banks.filter((b) => {
    if (bankValue(b) === chosen) return true;
    const words = bankWords(b.name);
    const joined = b.name.toLowerCase().replace(/[^a-z0-9]/g, "");
    // Within the name run together only for three letters or more, so
    // "fi" finds First, not Microfinance.
    return want.every((w) => words.some((x) => x.startsWith(w)) || (w.length >= 3 && joined.includes(w)));
  });
}

/** The banks a list offers a search for: lists longer than this. */
export const BANK_SEARCH_FROM = 8;

/** The bank chosen in a picker, as its value: the code when the list has
 * codes, else the name. */
export const bankValue = (b: Bank) => (b.code ? `c:${b.code}` : `n:${b.name}`);

/** The fields a chosen bank sends: its name, and its code with the list it
 * came from when it has one. */
export function bankFields(list: BankList | undefined, value: string): { bank_name: string; bank_code?: string; code_from?: string } | null {
  if (!list) return null;
  const b = list.banks.find((x) => bankValue(x) === value);
  if (!b) return null;
  return b.code ? { bank_name: b.name, bank_code: b.code, code_from: list.from } : { bank_name: b.name };
}

/** The picker's value for a saved account's bank in a list: by its code
 * when it came from this list, else by name (case and "Bank"/"PLC" set
 * aside); "" when the list does not have it. */
export function bankValueOf(list: BankList | undefined, d: SavedThing["details"]): string {
  if (!list) return "";
  if (d.bank_code && d.code_from === list.from) {
    const b = list.banks.find((x) => x.code === d.bank_code);
    if (b) return bankValue(b);
  }
  const key = (s: string) =>
    s
      .toLowerCase()
      .split(/[^a-z0-9]+/)
      .filter((w) => w && !["bank", "plc", "limited", "ltd"].includes(w))
      .join(" ");
  const b = list.banks.find((x) => key(x.name) === key(d.bank_name ?? ""));
  return b ? bankValue(b) : "";
}

/** The words for where names are checked: the connection, or why not. */
export function providerLine(p: SavedProvider | undefined): string | null {
  if (!p) return null;
  if (p.connected) return `Names are checked with the bank through ${p.connected.wallet ? "your Taskiem wallet" : p.connected.name}.`;
  return null;
}

/** Whether the step to connect a payments account is shown: to an owner
 * of a business with none. */
export const showConnectStep = (p: SavedProvider | undefined) => !!p && !p.connected && p.owner;

/** A saved thing's icon by its kind. */
export const savedIcon = (kind?: string) => KIND_WORDS[kind as SavedKind]?.icon ?? "note";

/** The picker's suggestions for saved things (saved objects S1): the
 * person's own first, then the business's, each by its @name with its
 * title and hint under it; a text carries its words. Texts only where
 * nothing else can be shared yet (every chat but Taskiem's: other kinds go
 * as cards, later). */
export function savedSuggestions(mine: SavedThing[], business: SavedThing[], textsOnly: boolean): Suggestion[] {
  const all = [...mine, ...business].filter((x) => !textsOnly || x.kind === "text");
  // Two of one name (one's own and the business's): each says whose.
  const twice = new Set(all.filter((x, i) => all.findIndex((y) => y.name === x.name) !== i).map((x) => x.name));
  return all.map((x) => ({
    kind: "saved" as const,
    id: x.id,
    name: x.name,
    sub: [x.title, x.hint, twice.has(x.name) ? (x.owner === "mine" ? "Yours" : "The business's") : ""].filter(Boolean).join(" · "),
    words: x.title,
    savedKind: x.kind,
    savedOwner: x.owner,
    savedSensitive: savedSensitive(x),
    body: x.kind === "text" ? (x.details.body ?? "") : undefined,
  }));
}

/** saved.MakeCard's rule: an account's number, a person's phone or email,
 * a place's phone. */
export function savedSensitive(x: Pick<SavedThing, "kind" | "details">): boolean {
  const d = x.details;
  if (x.kind === "bank_account") return true;
  if (x.kind === "person") return !!(d.phone || d.email);
  if (x.kind === "place") return !!d.phone;
  return false;
}

/** The server's question before sharing what is sensitive (shareQuestion):
 * "Share Mum's account details with Bola Stores?". */
export function shareQuestion(s: { words?: string; name: string; savedKind?: string }, to: string): string {
  const what = s.savedKind === "bank_account" ? "account details" : s.savedKind === "person" || s.savedKind === "place" ? "phone number and details" : "details";
  return `Share ${s.words || s.name}'s ${what} with ${to || "them"}?`;
}

/** A saved thing shared in a chat (saved objects S1, part 3; the server's
 * saved.Card): a copy of it as it was sent, its lines to copy, and its
 * fields for Save this. */
export interface SavedCard {
  kind: SavedKind | "custom";
  /** A made-up kind's name ("Car"). */
  kind_name?: string;
  title: string;
  /** The sender's @name for it. */
  name: string;
  lines: { label: string; value: string }[];
  details: SavedDetails;
  sensitive: boolean;
}

/** A saved thing a message shares (POST …/messages {card: {type: saved,
 * saved}}): one's own or the business's; confirm says yes to sharing what
 * is sensitive in it, asked first by a 409 confirm_share. */
export interface SavedShare {
  id: string;
  owner: "mine" | "business";
  confirm?: boolean;
}

/** A card's line under its title, folded: its kind and first detail
 * ("Bank account · Guaranty Trust Bank"). */
export function cardSub(c: SavedCard): string {
  const kind = c.kind === "custom" ? (c.kind_name ?? "a saved thing") : (KIND_WORDS[c.kind]?.one ?? "a saved thing");
  return [kind.charAt(0).toUpperCase() + kind.slice(1), c.lines[0]?.label === "Text" ? "" : c.lines[0]?.value].filter(Boolean).join(" · ");
}
