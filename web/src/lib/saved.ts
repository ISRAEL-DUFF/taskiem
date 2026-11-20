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
  kind: SavedKind;
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
