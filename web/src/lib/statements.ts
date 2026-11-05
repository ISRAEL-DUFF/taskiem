// A linked business's statement (business links L5, §10;
// GET /v1/links/{id}/statement): what they owe us and what we owe them.

export interface StatementDoc {
  id: string;
  reference: string;
  status: string;
  total_kobo: number;
  paid_kobo: number;
  due_kobo: number;
  due_on?: string;
  overdue: boolean;
  disputed: boolean;
  issued_at: string;
}

export interface StatementSide {
  owed_kobo: number;
  overdue_kobo: number;
  oldest_due?: string;
  terms_days?: number;
  credit_limit_kobo?: number;
  available_kobo?: number;
  documents: StatementDoc[];
}

export interface Statement {
  link: string;
  business: string;
  they_owe?: StatementSide;
  we_owe?: StatementSide;
}

/** The terms in words: "30 days to pay", "pay at once". */
export function termsWords(days?: number): string {
  if (days === undefined || days === null) return "";
  if (days === 0) return "pay at once";
  return days === 1 ? "1 day to pay" : `${days} days to pay`;
}

/** A typed number of days ("" for none); NaN when not one. */
export function daysValue(text: string): number | null {
  const t = text.trim();
  if (t === "") return null;
  if (!/^\d+$/.test(t) || Number(t) > 365) return Number.NaN;
  return Number(t);
}
