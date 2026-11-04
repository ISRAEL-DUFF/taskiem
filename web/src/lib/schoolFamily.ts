import { dateLabel, naira } from "./invoices";

// The family page's staff side (docs/spec/schools.md 10 and 11.3, B6;
// docs/schools.md#the-family-page): families' "I have paid" claims, their
// payment plan requests, a link sent to a family, and the link life
// setting. The words and checks the pages use, kept here to be tested.

export type ClaimStatus = "open" | "matched" | "dismissed";
export type ClaimHow = "transfer" | "cash" | "pos" | "other";
export type PlanStatus = "requested" | "approved" | "declined";

export interface Instalment {
  amount_kobo: number;
  due_on: string;
}

/** An "I have paid" claim (GET /v1/schools/claims; also in /v1/money/unmatched as `claims`, with `student` for the admission number). */
export interface Claim {
  id: string;
  family_id: string;
  student_id: string;
  admission_number?: string;
  student?: string;
  name: string;
  class: string;
  amount_kobo: number;
  paid_on: string;
  how: ClaimHow;
  reference?: string;
  note?: string;
  status?: ClaimStatus;
  created_at: string;
  resolved_at?: string;
  resolved_by?: string;
  resolution_reason?: string;
}

/** A payment plan request (GET /v1/schools/plans). */
export interface PlanRequest {
  id: string;
  family_id: string;
  student_id: string;
  admission_number: string;
  name: string;
  class: string;
  instalments: Instalment[];
  message?: string;
  status: PlanStatus;
  created_at: string;
  decided_at?: string;
  decided_by?: string;
  decision_note?: string;
  decided_instalments?: Instalment[];
}

/** How a family was reached with a fresh link (POST /v1/schools/families/{id}/link, and a plan decision's `sent`). */
export interface FamilySent {
  link?: string;
  expires_at: string;
  channels: Record<string, number>;
  nobody?: boolean;
}

export const CLAIM_HOW_WORDS: Record<ClaimHow, string> = { transfer: "bank transfer", cash: "cash", pos: "POS", other: "another way" };

/** "₦40,000 by bank transfer on 20 Oct 2026, ref GTB/1". */
export function claimLine(c: Pick<Claim, "amount_kobo" | "how" | "paid_on" | "reference">): string {
  let s = `${naira(c.amount_kobo)} by ${CLAIM_HOW_WORDS[c.how] ?? c.how} on ${dateLabel(c.paid_on)}`;
  if (c.reference) s += `, ref ${c.reference}`;
  return s;
}

/** A claim's state, for a pill. */
export function claimStatus(s: ClaimStatus | undefined): { label: string; tone: string } {
  switch (s) {
    case "matched":
      return { label: "Found and counted", tone: "ok" };
    case "dismissed":
      return { label: "Dismissed", tone: "muted" };
    default:
      return { label: "To find", tone: "warn" };
  }
}

/** A plan request's state, for a pill. */
export function planStatus(s: PlanStatus): { label: string; tone: string } {
  switch (s) {
    case "approved":
      return { label: "Approved", tone: "ok" };
    case "declined":
      return { label: "Declined", tone: "muted" };
    default:
      return { label: "Waiting for a decision", tone: "warn" };
  }
}

/** "₦50,000 by 24 Nov 2026; ₦68,000 by 24 Dec 2026 (₦118,000 in all)". */
export function instalmentsLine(list: Instalment[] | undefined): string {
  if (!list || list.length === 0) return "";
  const parts = list.map((i) => `${naira(i.amount_kobo)} by ${dateLabel(i.due_on)}`);
  const total = list.reduce((n, i) => n + i.amount_kobo, 0);
  return list.length === 1 ? (parts[0] ?? "") : `${parts.join("; ")} (${naira(total)} in all)`;
}

/** An instalment row as typed: naira and a date. */
export interface InstalmentRow {
  amount: string;
  due_on: string;
}

export function emptyRows(n: number): InstalmentRow[] {
  return Array.from({ length: n }, () => ({ amount: "", due_on: "" }));
}

/** Rows from a plan's instalments, to change before approving. */
export function rowsOf(list: Instalment[]): InstalmentRow[] {
  return list.map((i) => ({ amount: String(i.amount_kobo / 100), due_on: i.due_on }));
}

function kobo(s: string): number | null {
  const n = Number(s.replace(/[,₦\s]/g, ""));
  if (!s.trim() || !Number.isFinite(n) || n <= 0) return null;
  return Math.round(n * 100);
}

/** Why the rows cannot be sent, or null: 1 to 6 filled rows, each an amount and a date, in date order; empty rows are skipped. */
export function instalmentProblem(rows: InstalmentRow[]): string | null {
  const filled = rows.filter((r) => r.amount.trim() || r.due_on);
  if (filled.length === 0) return "Give at least one instalment.";
  if (filled.length > 6) return "A plan has at most six instalments.";
  let last = "";
  for (const r of filled) {
    if (kobo(r.amount) === null) return "Each instalment needs an amount in naira.";
    if (!/^\d{4}-\d{2}-\d{2}$/.test(r.due_on)) return "Each instalment needs a date.";
    if (r.due_on < last) return "Instalments are in date order.";
    last = r.due_on;
  }
  return null;
}

/** The instalments to send, from rows that pass instalmentProblem. */
export function instalmentsRequest(rows: InstalmentRow[]): Instalment[] {
  return rows.filter((r) => r.amount.trim() || r.due_on).map((r) => ({ amount_kobo: kobo(r.amount) ?? 0, due_on: r.due_on }));
}

/** "Sent on WhatsApp to 1 guardian and by SMS to 1; the link works until 27 Oct 2026." */
export function sentLine(s: FamilySent): string {
  const parts: string[] = [];
  const words: Record<string, string> = { whatsapp: "on WhatsApp", telegram: "on Telegram", sms: "by SMS" };
  for (const [ch, n] of Object.entries(s.channels ?? {})) {
    if (n > 0) parts.push(`${words[ch] ?? ch} to ${n} guardian${n === 1 ? "" : "s"}`);
  }
  const until = s.expires_at ? ` The link works until ${dateLabel(s.expires_at.slice(0, 10))}.` : "";
  if (parts.length === 0) return `Nobody could be reached (no phone on file, or too many messages this hour). Copy the link below to pass it on.${until}`;
  return `Sent ${parts.join(" and ")}.${until}`;
}

/** Why a link life cannot be saved, or null: a whole number of days from 1 to 30. */
export function linkDaysProblem(v: string): string | null {
  const n = Number(v);
  if (!v.trim() || !Number.isInteger(n) || n < 1 || n > 30) return "Links work for 1 to 30 days.";
  return null;
}
