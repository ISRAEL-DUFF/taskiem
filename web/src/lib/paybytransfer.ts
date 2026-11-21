// Paying a bill by transfer, in the chat (the payments flow, part 1;
// docs/spec/chat-payments-flow.md): the ways a bill can be paid, the
// account number the seller's side gives for it, and how its card reads.
// Words: "account number", "Good for", "Send exactly", "Paid".

import { naira } from "./invoices";

/** How a bill can be paid, in the server's order. */
export type WayToPay = "transfer" | "taskiem" | "checkout";

export type TransferState = "asking" | "open" | "paid" | "part" | "expired" | "failed";

/** A bill's account number as the buyer sees it (GET /v1/bills/{id}/transfer). */
export interface BillTransfer {
  state: TransferState;
  bank_name?: string;
  account_number?: string;
  account_name?: string;
  /** What the number is for: send exactly this. */
  amount_kobo?: number;
  /** Part paid: what arrived. */
  received_kobo?: number;
  expires_at?: string;
  /** Why a number could not be had, in plain words. */
  problem?: string;
  /** Paid: the same seller's next bill to pay. */
  next?: string;
}

/** An invoice's account number as its customer sees it, in their chat with
 * the business (GET /v1/me/chats/{id}/invoices/{invoice}/transfer). */
export interface CustomerTransfer {
  invoice: string;
  reference: string;
  due_kobo: number;
  state: "none" | "open" | "part" | "paid" | "expired";
  bank_name?: string;
  account_number?: string;
  account_name?: string;
  amount_kobo?: number;
  expires_at?: string;
}

/** A customer's invoice number in the bill's shape, so one card shows both. */
export function fromCustomer(c: CustomerTransfer): BillTransfer | null {
  if (c.state === "none") return null;
  const amount = c.amount_kobo ?? 0;
  return {
    state: c.state,
    bank_name: c.bank_name,
    account_number: c.account_number,
    account_name: c.account_name,
    amount_kobo: c.amount_kobo,
    // Part paid: what is left on the invoice is what did not arrive.
    received_kobo: c.state === "part" ? Math.max(0, amount - c.due_kobo) : undefined,
    expires_at: c.expires_at,
  };
}

export const WAY_WORDS: Record<WayToPay, string> = {
  transfer: "Pay by transfer",
  taskiem: "Pay from Taskiem",
  checkout: "Pay on their checkout",
};

/** A line under each way on the sheet. */
export function wayLine(w: WayToPay, supplier: string): string {
  switch (w) {
    case "transfer":
      return "From any bank app, to an account number for this bill";
    case "taskiem":
      return "From your Taskiem wallet, after one approval";
    default:
      return `On ${supplier}'s own page, by card or bank`;
  }
}

/** The ways to show, the last one chosen for this seller first; transfer
 * leads otherwise (the server's order). */
export function orderWays(ways: readonly string[] | undefined, last?: string | null): WayToPay[] {
  const known = (ways ?? []).filter((w): w is WayToPay => w === "transfer" || w === "taskiem" || w === "checkout");
  if (!last || !known.includes(last as WayToPay)) return known;
  return [last as WayToPay, ...known.filter((w) => w !== last)];
}

/** Ways a bill can be paid when the server says nothing of them (an older
 * answer): their checkout, when there is one. */
export function waysOf(b: { ways_to_pay?: string[]; pay_link?: string; due_kobo: number }): WayToPay[] {
  if (b.ways_to_pay) return orderWays(b.ways_to_pay);
  return b.due_kobo > 0 && b.pay_link ? ["checkout"] : [];
}

const WAY_KEY = "taskiem.pay-way.";

/** The way last chosen to pay this seller, on this browser. */
export function lastWay(seller: string, store: Pick<Storage, "getItem"> | undefined = globalThis.localStorage): WayToPay | null {
  try {
    const v = store?.getItem(WAY_KEY + seller);
    return v === "transfer" || v === "taskiem" || v === "checkout" ? v : null;
  } catch {
    return null;
  }
}

/** Remembers the way chosen to pay this seller. */
export function rememberWay(seller: string, way: WayToPay, store: Pick<Storage, "setItem"> | undefined = globalThis.localStorage): void {
  try {
    store?.setItem(WAY_KEY + seller, way);
  } catch {
    // Storage blocked: next time starts with transfer again.
  }
}

/** An account number as it reads: 0123 456 789. */
export function spacedNumber(n: string): string {
  const d = n.replace(/\s+/g, "");
  if (/^\d{10}$/.test(d)) return `${d.slice(0, 4)} ${d.slice(4, 7)} ${d.slice(7)}`;
  return d.replace(/(.{4})(?=.)/g, "$1 ");
}

/** An account number as it is copied: no spaces. */
export const bareNumber = (n: string) => n.replace(/\s+/g, "");

/** The last five minutes turn the countdown to a warning. */
export const WARN_MS = 5 * 60_000;

function clockOf(ms: number): string {
  const s = Math.max(0, Math.ceil(ms / 1000));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const ss = String(s % 60).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

/** How long the number is good for, from the server's expiry: "Good for
 * 29:12", "Expires in 4:05" in the last five minutes, over at zero. */
export function countdown(expiresAt: string | undefined, now: number): { words: string; warn: boolean; over: boolean } | null {
  if (!expiresAt) return null;
  const left = Date.parse(expiresAt) - now;
  if (Number.isNaN(left)) return null;
  if (left <= 0) return { words: "Expires in 0:00", warn: true, over: true };
  const warn = left <= WARN_MS;
  return { words: `${warn ? "Expires in" : "Good for"} ${clockOf(left)}`, warn, over: false };
}

/** The state the card shows: an open number past its time is expired,
 * whatever the last answer said. */
export function shownState(t: BillTransfer, now: number): TransferState {
  if (t.state === "open" && t.expires_at && Date.parse(t.expires_at) <= now) return "expired";
  return t.state;
}

/** The card's first line, by state. */
export function transferHead(t: BillTransfer, state: TransferState = t.state, paidAt?: string, when?: (iso: string) => string): string {
  const amount = t.amount_kobo ?? 0;
  switch (state) {
    case "asking":
      return "Getting an account number…";
    case "open":
      return `Send exactly ${naira(amount)}`;
    case "paid":
      return `Paid ${naira(amount)}${paidAt && when ? ` · ${when(paidAt)}` : ""}`;
    case "part": {
      const got = t.received_kobo ?? 0;
      return `Received ${naira(got)} of ${naira(amount)}. ${naira(Math.max(0, amount - got))} left`;
    }
    case "expired":
      return "This number has expired. Don't send to it";
    default:
      return t.problem || "We couldn't get an account number just now. Try again, or pay another way.";
  }
}

/** The card's pill: a word and its tone. */
export function transferPill(state: TransferState): { word: string; tone: "lime" | "warn" | "danger" | "plain" } {
  switch (state) {
    case "asking":
      return { word: "Asking", tone: "plain" };
    case "open":
      return { word: "Waiting", tone: "warn" };
    case "paid":
      return { word: "Paid", tone: "lime" };
    case "part":
      return { word: "Part paid", tone: "warn" };
    case "expired":
      return { word: "Expired", tone: "plain" };
    default:
      return { word: "No number", tone: "danger" };
  }
}

/** How soon to look again: every 2 seconds while a number is coming,
 * every 10 while one is open; not at all once it is settled. */
export function pollEvery(state: TransferState | undefined): number | null {
  if (state === "asking") return 2_000;
  if (state === "open") return 10_000;
  return null;
}

/** A refusal from the server, in the card's words. */
export function refusalWords(code: string | undefined, error: string | undefined): string {
  switch (code) {
    case "too_many":
      return error || "Too many numbers asked for this bill. Try again in a while, or pay on their checkout.";
    case "disputed":
      return error || "This bill is queried: pay once the query is settled.";
    case "nothing_due":
      return error || "Nothing is left to pay on this bill.";
    default:
      return error || "We couldn't get an account number just now. Try again, or pay another way.";
  }
}
