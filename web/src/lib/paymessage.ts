// Paying from a message (paying people, phase 2; docs/spec/chat-pay-people.md,
// "Pay one person"): a chat message holding a 10-digit account number gets
// a "Pay this account" chip; what the message says (POST /v1/payments/read)
// fills the Pay sheet in. Here, the quick check that a message may hold
// one, the chip's words, the sheet's values, and the words for a name that
// differs and for the banks' answers to the name check.

import { dateLabel, naira } from "./invoices";
import { PAY_FIELDS, type PayField } from "./payments";
import { cleanName, nameProblem, savedNameFor, type BankList, type SavedThing } from "./saved";

/** A bank the Pay sheet offers: likely when the number's check digit fits
 * its code (and the business pays it most). */
export interface PayBank {
  code: string;
  name: string;
  likely?: boolean;
}

/** What POST /v1/payments/read found in a message. */
export interface PayReading {
  found: boolean;
  account_number?: string;
  bank_name?: string;
  bank_code?: string;
  amount_kobo?: number;
  /** The name the message gives. */
  name?: string;
  /** With no bank read: the banks the number could be with. */
  likely?: PayBank[];
  /** The business's saved account with this number, by its @name. */
  payee?: string;
  /** The business's last payment to this account. */
  paid?: { id: string; amount_kobo: number; status: string; at: string };
}

/** One bank's answer to the name check (POST /v1/payments/name-check). */
export interface NameAnswer {
  bank_name: string;
  bank_code: string;
  account_name?: string;
  problem?: string;
}

/** Whether words may hold an account number: a run of exactly 10 digits
 * ("0123456789"), or 10 digits in groups ("012 345 6789"). A phone number
 * (11 digits) is not one. Only then is the message read by the server. */
export function mayHoldAccount(text: string | undefined | null): boolean {
  if (!text) return false;
  if (/(^|[^\d])\d{10}(?![\d])/.test(text)) return true;
  for (const m of text.matchAll(/(?:^|[^\d])(\d{2,4}(?:[ -]\d{2,4}){1,3})(?![\d])/g)) {
    if ((m[1] ?? "").replace(/\D/g, "").length === 10) return true;
  }
  return false;
}

// The day in Lagos, by dateLabel ("3 Nov 2026"), without this year's year.
const day = (at: string, now: Date) => {
  const t = new Date(at);
  if (Number.isNaN(t.getTime())) return "";
  const lagos = (d: Date) => d.toLocaleDateString("en-CA", { timeZone: "Africa/Lagos" });
  const label = dateLabel(lagos(t));
  const thisYear = lagos(now).slice(0, 4);
  return label.endsWith(` ${thisYear}`) ? label.slice(0, -5) : label;
};

/** A chip already paid, in words: "Paid ₦45,000 on 3 Nov"; sent and not
 * yet received, or waiting for the owner's OK, says so. */
export function paidWords(paid: PayReading["paid"], now = new Date()): string {
  if (!paid) return "";
  const on = day(paid.at, now);
  const amount = naira(paid.amount_kobo);
  switch (paid.status) {
    case "paid":
      return `Paid ${amount} on ${on}`;
    case "waiting_ok":
      return `${amount} waiting for the owner's OK since ${on}`;
    default:
      return `Sent ${amount} on ${on}`;
  }
}

/** What the chip under a message is, for the person who sees it: a way to
 * pay; for staff, only a saved account (anything else: ask an owner to
 * save it); or nothing, when the message holds no account. */
export function chipFor(r: PayReading | undefined, owner: boolean): "pay" | "ask" | null {
  if (!r?.found || !r.account_number) return null;
  if (owner || r.payee) return "pay";
  return "ask";
}

/** The chip's words: "Pay this account", "Pay @chidi" for a saved one, or
 * what was paid and Pay again. */
export function chipWords(r: PayReading, now = new Date()): string {
  if (r.paid) return `${paidWords(r.paid, now)} · Pay again`;
  const payee = (r.payee ?? "").trim().replace(/^@/, "");
  return payee ? `Pay @${payee}` : "Pay this account";
}

/** Kobo as the sheet's amount box holds it: "45,000", "1,250.50". */
export function amountTyped(kobo: number | undefined): string {
  if (!kobo || kobo <= 0) return "";
  return (kobo / 100).toLocaleString("en-GB", { minimumFractionDigits: kobo % 100 ? 2 : 0, maximumFractionDigits: 2 });
}

/** The Pay sheet as a message fills it in: the saved account by its
 * @name when the business has one with this number (the only way for
 * staff), else the number and its bank; the amount when the message says
 * one; and the name it gives, to compare with the bank's. */
export function messageFill(r: PayReading): {
  way: "person" | "account";
  fields: PayField[];
  initial: Record<string, string>;
  expectName?: string;
} {
  const amount = amountTyped(r.amount_kobo);
  const initial: Record<string, string> = {};
  if (amount) initial.amount = amount;
  const expectName = r.name?.trim() || undefined;
  if (r.payee) {
    initial.who = `@${r.payee.replace(/^@/, "")}`;
    return { way: "person", fields: PAY_FIELDS.person, initial, expectName };
  }
  initial.number = r.account_number ?? "";
  if (r.bank_name) initial.bank = r.bank_name;
  return { way: "account", fields: PAY_FIELDS.account, initial, expectName };
}

const nameWords = (s: string) =>
  s
    .toLowerCase()
    .normalize("NFKD")
    .replace(/[̀-ͯ]/g, "")
    .replace(/[^a-z\s]/g, " ")
    .split(/\s+/)
    .filter((w) => w.length > 1);

/** Whether the message's name and the bank's are different people's:
 * neither's words all in the other's ("Chidi Okeke" and "OKEKE CHIDI
 * EMMANUEL" are the same; "Chidi Okafor" and "Chidi Okeke" are not). */
export function namesDiffer(said: string | undefined, bank: string | undefined | null): boolean {
  const a = nameWords(said ?? "");
  const b = nameWords(bank ?? "");
  if (a.length === 0 || b.length === 0) return false;
  const within = (x: string[], y: string[]) => x.every((w) => y.includes(w));
  return !within(a, b) && !within(b, a);
}

/** The amber words when they differ, or null. */
export function nameWarning(said: string | undefined, bank: string | undefined | null): string | null {
  if (!said || !bank || !namesDiffer(said, bank)) return null;
  return `The message says ${said.trim()}; the bank says ${bank.trim()}.`;
}

/** One bank's answer in a few words: "GTBank: Chidi Okeke", "Access Bank:
 * no account", "Kuda: the bank did not say just now". */
export function answerWords(a: NameAnswer): string {
  if (a.account_name) return `${a.bank_name}: ${a.account_name}`;
  const p = (a.problem ?? "").trim().replace(/\.$/, "");
  if (!p || /^no account/i.test(p)) return `${a.bank_name}: no account`;
  return `${a.bank_name}: ${p.charAt(0).toLowerCase()}${p.slice(1)}`;
}

/** The fields of paying a rider: the amount, what for (a delivery unless
 * changed) and a note; the bank account is the one kept for their pay. */
export const RIDER_PAY_FIELDS: PayField[] = PAY_FIELDS.person.filter((f) => f.key !== "who");

/** The fields of paying a staff member: the same, wages unless changed;
 * the account is the one an owner linked to them. */
export const STAFF_PAY_FIELDS: PayField[] = RIDER_PAY_FIELDS;

/** The sheet's starting values for a rider: a delivery, and what is due
 * to them when something is. */
export function riderFill(riderId: string, dueKobo: number): Record<string, string> {
  const initial: Record<string, string> = { rider: riderId, for: "a delivery" };
  const amount = amountTyped(dueKobo);
  if (amount) initial.amount = amount;
  return initial;
}

// --- Save as… (chat-pay-people, "Save as a beneficiary"; the build plan's
// P2: "can be saved as @driver on the way"): an account a message holds,
// not saved yet, kept among the business's saved accounts by a name to
// call it by, so the chip and Pay go by @name next time.

/** Whether "Save as…" shows beside a message's chip: an account read, not
 * saved already (no payee), for someone who may manage saved things. */
export function saveAsShown(r: PayReading | undefined, may: boolean): boolean {
  return may && !!r?.found && !!r.account_number && !(r.payee ?? "").trim();
}

/** The sheet's first values: the @name made from the message's name, as
 * the Saved page's names are ("Chidi Okafor" → @chidi-okafor), and that
 * name as the title; both empty when the message gives none. */
export function saveAsStart(r: PayReading): { name: string; title: string } {
  const title = (r.name ?? "").trim().slice(0, 80);
  const n = savedNameFor(title);
  return { name: n ? `@${n}` : "", title };
}

/** An account number shown masked: "••6789". */
export function maskedAccount(n: string | undefined): string {
  const d = (n ?? "").replace(/\D/g, "");
  return d.length >= 4 ? `••${d.slice(-4)}` : "";
}

/** The bank fields saved, as the Saved page sends them: the bank's name,
 * and its code with the list it came from when the business's payments
 * account's list has one; the name alone otherwise. bank is the bank's
 * name, read from the message or picked. */
export function saveAsBank(
  bank: string,
  list: BankList | null | undefined,
  r?: PayReading,
): { bank_name: string; bank_code?: string; code_from?: string } | null {
  const name = bank.trim();
  if (!name) return null;
  const b = list?.banks.find((x) => x.name === name);
  const code = b?.code || (r?.bank_name === name ? r.bank_code : undefined);
  if (code && list?.from) return { bank_name: name, bank_code: code, code_from: list.from };
  return { bank_name: name };
}

/** What is wrong with the sheet's values, and where, in plain words, or
 * null. */
export function saveAsProblem(v: { name: string; title: string; bank: string }): { at: "name" | "title" | "bank"; words: string } | null {
  const name = nameProblem(v.name);
  if (name) return { at: "name", words: name };
  if (!v.title.trim()) return { at: "title", words: "Give it a title, like Chidi, rider." };
  if (!v.bank.trim()) return { at: "bank", words: "Choose the bank." };
  return null;
}

/** The body of POST /v1/saved, as the Saved page saves an account. */
export function saveAsBody(
  r: PayReading,
  v: { name: string; title: string },
  bank: { bank_name: string; bank_code?: string; code_from?: string },
): Record<string, unknown> {
  return { name: cleanName(v.name), title: v.title.trim(), kind: "bank_account", ...bank, account_number: r.account_number ?? "" };
}

/** What the bank said once it is saved: its name for the account, the
 * amber words when the message's name differs, or why it was not checked
 * (the server's words); nothing when names are not checked. */
export function saveAsAnswer(
  out: { saved: Pick<SavedThing, "name" | "check">; check_problem?: string },
  said?: string,
): { bankName?: string; differs?: string; problem?: string } {
  const bankName = out.saved.check?.state === "checked" ? (out.saved.check.account_name ?? undefined) : undefined;
  return {
    bankName,
    differs: nameWarning(said, bankName) ?? undefined,
    problem: out.check_problem || out.saved.check?.problem || undefined,
  };
}
