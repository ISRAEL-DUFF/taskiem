// The chat's Invoices and Money tools (docs/spec/chat-five-more-flows.md,
// phase 2 additions): what a business is owed and the money it has, worked
// on in the chat as orders and stock are. Pure helpers, tested on their own.

import { invoiceStatus, naira, type CustomerInvoice } from "./invoices";
import type { PillTone } from "./tools";

/** One entry of the money log (GET /v1/money/entries). */
export interface MoneyEntry {
  id: string;
  kind: string;
  direction: "in" | "out";
  amount_kobo?: number;
  fee_kobo?: number;
  status: string;
  channel?: string;
  reference?: string;
  when: string;
  description?: string;
  vendor?: string;
  category?: string;
  /** What is left of money in to count towards an invoice or order. */
  left_kobo?: number;
  /** What it counts towards now: "invoice INV-0104". */
  towards?: string;
  /** Matching left it for a person. */
  to_match: boolean;
}

const short = (iso: string) =>
  new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "short" });

/** An invoice still owed: sent and not paid in full. */
export const owing = (x: Pick<CustomerInvoice, "status" | "due_kobo">) =>
  x.due_kobo > 0 &&
  (x.status === "sent" || x.status === "part_paid" || x.status === "overdue" || x.status === "recorded");

/** An invoice's pill on its row. */
export function invoicePill(x: Pick<CustomerInvoice, "status">): { text: string; tone: PillTone } {
  const s = invoiceStatus(x.status);
  return {
    text: s.label,
    tone: s.tone === "ok" ? "lime" : s.tone === "warn" || s.tone === "danger" ? "warn" : "plain",
  };
}

/** An invoice's line under its number: "A••• · ₦45,000 · ₦20,000 to pay". */
export function invoiceSub(x: Pick<CustomerInvoice, "customer" | "total_kobo" | "due_kobo" | "status" | "due_on">): string {
  const parts = [x.customer.name, naira(x.total_kobo)];
  if (owing(x))
    parts.push(
      x.due_kobo < x.total_kobo
        ? `${naira(x.due_kobo)} to pay`
        : x.due_on
          ? `due ${short(x.due_on)}`
          : "to pay",
    );
  return parts.filter(Boolean).join(" · ");
}

/** The tags an invoice's row is found by with the chips. */
export function invoiceTags(x: Pick<CustomerInvoice, "status" | "due_kobo" | "kind">): string[] {
  const t: string[] = [];
  if (owing(x)) t.push("owed");
  if (x.status === "overdue") t.push("overdue");
  if (x.status === "draft") t.push("draft");
  if (x.kind === "credit") t.push("credit");
  return t;
}

const KIND_WORDS: Record<string, string> = {
  payment: "Payment",
  account_credit: "Transfer to an account",
  payout: "Paid out",
  refund: "Refund",
  pos_sale: "Card sale",
  fee: "Fee",
  statement: "From a statement",
  cash: "Cash",
  expense: "Expense",
  correction: "Correction",
};

/** What an entry is, as its row's name: its own words, else its kind. */
export function entryName(e: MoneyEntry): string {
  return e.description || e.vendor || KIND_WORDS[e.kind] || e.kind.replaceAll("_", " ");
}

/** Its amount with which way it went: "+₦4,500.00", "−₦1,200.00". */
export function entryAmount(e: MoneyEntry): string {
  if (e.amount_kobo === undefined) return "No amount";
  return `${e.direction === "in" ? "+" : "−"}${naira(e.amount_kobo)}`;
}

/** An entry's line: "+₦4,500.00 · Cash · 9 Nov". */
export function entrySub(e: MoneyEntry): string {
  const kind = e.description || e.vendor ? KIND_WORDS[e.kind] : undefined;
  return [entryAmount(e), kind, e.channel && e.channel !== "cash" ? e.channel : undefined, short(e.when)]
    .filter(Boolean)
    .join(" · ");
}

/** An entry's pill: waiting to be matched, what it counts for, or not settled. */
export function entryPill(e: MoneyEntry): { text: string; tone: PillTone } | undefined {
  if (e.status === "failed" || e.status === "reversed")
    return { text: e.status === "failed" ? "Failed" : "Reversed", tone: "plain" };
  if (e.status === "pending") return { text: "Pending", tone: "plain" };
  if (e.to_match) return { text: "To match", tone: "warn" };
  if (e.towards) return { text: "Counted", tone: "lime" };
  return undefined;
}

export function entryTags(e: MoneyEntry): string[] {
  const t: string[] = [e.direction];
  if (e.to_match) t.push("match");
  return t;
}

/** Money in with something left to count: it can be matched to an
 * invoice by hand (cash typed in never waits in the matching queue). */
export const matchable = (e: MoneyEntry) =>
  e.direction === "in" &&
  e.status === "settled" &&
  (e.left_kobo ?? 0) > 0;

/** What an entry counts towards, in a sentence. */
export function towardsWords(e: MoneyEntry): string {
  if (e.towards) return `Counted towards ${e.towards}.`;
  if (e.to_match) return "Not matched to an invoice or order yet.";
  return "";
}
