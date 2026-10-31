// Bills (business links L1; docs/spec/business-links.md §6): a linked
// supplier's invoice in the buyer's business (GET /v1/bills), how a bill
// reads in a row and on its page.

import { naira, type InvoiceLine } from "./invoices";

export type BillStatus = "open" | "paying" | "part_paid" | "paid" | "disputed" | "void";

export interface Bill {
  id: string;
  link: string;
  supplier: string;
  supplier_name: string;
  /** The supplier's invoice number. */
  reference: string;
  lines: InvoiceLine[];
  amount_kobo: number;
  /** What the supplier's match has counted paid. */
  paid_kobo: number;
  due_kobo: number;
  due_on?: string;
  status: BillStatus;
  allow_part_payments: boolean;
  disputed: boolean;
  /** What the business says it paid outside Taskiem: its own figure. */
  declared_paid_kobo: number;
  /** The account number the supplier issued this business, and its bank. */
  pay_account_number?: string;
  pay_bank_name?: string;
  /** The supplier's invoice page: a card or a transfer. */
  pay_link?: string;
  mirrored_at: string;
  created_at: string;
}

export const billPath = (id: string) => `/v1/bills/${encodeURIComponent(id)}`;

type Tone = "ok" | "warn" | "info" | "muted" | "danger";

/** A bill's status in a word or two, with its tone. */
export function billStatus(s: BillStatus): { label: string; tone: Tone } {
  switch (s) {
    case "paid":
      return { label: "Paid", tone: "ok" };
    case "part_paid":
      return { label: "Part-paid", tone: "warn" };
    case "paying":
      return { label: "Being paid", tone: "info" };
    case "disputed":
      return { label: "Queried", tone: "danger" };
    case "void":
      return { label: "Cancelled", tone: "muted" };
    default:
      return { label: "To pay", tone: "warn" };
  }
}

/** A bill's hero: the figure that matters, what it is, and the bar. */
export function billHero(b: Pick<Bill, "status" | "amount_kobo" | "paid_kobo" | "due_kobo" | "supplier_name">): { label: string; kobo: number; sub: string; paid: number } {
  const paid = b.amount_kobo > 0 ? Math.min(1, Math.max(0, b.paid_kobo / b.amount_kobo)) : 0;
  switch (b.status) {
    case "void":
      return { label: "Cancelled", kobo: b.amount_kobo, sub: `${b.supplier_name} cancelled this invoice`, paid };
    case "paid":
      return { label: "Paid in full", kobo: b.amount_kobo, sub: "Nothing left to pay", paid: 1 };
    default:
      return { label: "To pay", kobo: b.due_kobo, sub: `of ${naira(b.amount_kobo)} · ${naira(b.paid_kobo)} paid`, paid };
  }
}

/** What is left to pay across bills. */
export function billsDue(list: Bill[] | undefined): number {
  return (list ?? []).reduce((n, b) => n + (b.status === "void" ? 0 : b.due_kobo), 0);
}
