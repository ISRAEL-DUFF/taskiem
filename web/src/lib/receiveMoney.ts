// Receive money with Taskiem (decision 0052; docs/spec/iswallet-account-
// numbers.md W2 to W4): a wallet and an account number under Taskiem's own
// iswallet client, for a business with no payments account of its own.
// GET /v1/settings/receive-money says whether it is on offer and on; the
// Payments settings page turns it on, shows the number and the balance,
// and withdraws to the business's bank.

import { naira, toKobo } from "./invoices";
import { spacedNumber } from "./schoolAccounts";

export type OwnerType = "client_end_user_business" | "client_end_business";

export interface ReceiveMoney {
  available: boolean;
  plan: boolean;
  plan_note?: string;
  on: boolean;
  default_owner_type: OwnerType;
  mode?: "live" | "test";
  owner_type?: OwnerType;
  connection?: string;
  account?: { account_number: string; bank_name?: string; account_name?: string };
  account_problem?: string;
  withdraw_to?: { bank_code: string; bank_name?: string; account_number: string; account_name: string };
  two_approvers_above_kobo: number;
  withdraw_workflow_id?: string;
}

export interface Balance {
  available_kobo: number;
  ledger_kobo: number;
  currency: string;
  at: string;
}

export interface Bank {
  code?: string;
  bank_code?: string;
  name?: string;
  bank_name?: string;
}

/** Where the money is held, in plain words, for the choice and after it. */
export const OWNER_TYPES: { value: OwnerType; title: string; detail: string; short: string }[] = [
  {
    value: "client_end_user_business",
    title: "Other people's money too, such as school fees",
    detail:
      "Kept in an account of your own at the partner bank. Each payment shows in your balance at once; the money itself reaches your account within minutes.",
    short: "In your own account",
  },
  {
    value: "client_end_business",
    title: "Only my own money",
    detail: "Kept with the partner bank's other money and counted as yours. Simpler, and the usual choice for a shop or a trader.",
    short: "Held with others, counted as yours",
  },
];

export function ownerTypeShort(t?: OwnerType): string {
  return OWNER_TYPES.find((o) => o.value === t)?.short ?? "";
}

/** Why turning it on is not possible here, or "" when it is. */
export function turnOnBlocker(r: ReceiveMoney, owner: boolean): string {
  if (!r.available) return "Receiving money with Taskiem is not available on this Taskiem yet.";
  if (!r.plan) return r.plan_note || "Your plan does not include receiving money with Taskiem. You can change plans on the Billing page.";
  if (!owner) return "Only an owner can turn this on.";
  return "";
}

/** The hero's words: the number, spaced as banks print it, or why there is none yet. */
export function receiveHero(r: ReceiveMoney): { label: string; figure: string; sub: string; tag: string } {
  const tag = r.mode === "test" ? "Practice" : "Live";
  if (!r.account)
    return { label: "Your account number", figure: "Not ready yet", sub: r.account_problem || "Asking the bank for your number…", tag };
  const sub = [r.account.bank_name, r.account.account_name].filter(Boolean).join(" · ");
  return { label: "Your account number", figure: spacedNumber(r.account.account_number), sub, tag };
}

/** The approval rule in a sentence. */
export function approvalLine(twoAboveKobo: number): string {
  return twoAboveKobo > 0
    ? `An owner approves each withdrawal; two owners approve ${naira(twoAboveKobo)} or more.`
    : "An owner approves each withdrawal.";
}

/** A bank as the picker shows it: its code and name, whichever names iswallet uses. */
export function bankOf(b: Bank): { code: string; name: string } {
  return { code: b.code ?? b.bank_code ?? "", name: b.name ?? b.bank_name ?? b.code ?? b.bank_code ?? "" };
}

/** A typed withdrawal amount in kobo, or the problem with it. */
export function withdrawAmount(text: string, availableKobo?: number): { kobo?: number; problem?: string } {
  const kobo = toKobo(text);
  if (kobo === undefined) return { problem: "Type the amount to withdraw." };
  if (Number.isNaN(kobo) || kobo < 100) return { problem: "The amount is a number of naira, at least ₦1, such as 25000 or 25000.50." };
  if (availableKobo !== undefined && kobo > availableKobo) return { problem: `Your wallet holds ${naira(availableKobo)}: withdraw that much or less.` };
  return { kobo };
}

/** A 10-digit account number, or the problem with it. */
export function accountProblem(n: string): string {
  return /^\d{10}$/.test(n) ? "" : "An account number is 10 digits.";
}
