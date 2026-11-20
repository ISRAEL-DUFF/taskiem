// Saved things (docs/spec/saved-objects.md; build plan S0): a person's own
// and a business's saved bank accounts, called up by @name. The server
// never sends an account number in full but to a reveal with a passkey;
// what is here words and checks what a person types before it is sent.

export interface SavedCheck {
  state: "checked" | "unchecked";
  account_name: string | null;
  via: string | null;
  at: string | null;
}

export interface SavedThing {
  id: string;
  owner: "mine" | "business";
  name: string;
  title: string;
  kind: "bank_account";
  hint: string;
  details: { bank_name: string; bank_code?: string; code_from?: string; note?: string; account_number?: string };
  check: SavedCheck;
  to_change: boolean;
  created_at: string;
  updated_at: string;
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
export const savedSub = (x: SavedThing) => `@${x.name} · ${x.hint}`;

/** What a row says of the bank's name for the account. */
export function checkLine(x: SavedThing): { words: string; tone: "ok" | "warn" } {
  if (x.check.state === "checked" && x.check.account_name) return { words: `Bank's name: ${x.check.account_name}`, tone: "ok" };
  return { words: "Name not checked yet", tone: "warn" };
}

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
  const b = list.banks.find((x) => key(x.name) === key(d.bank_name));
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
