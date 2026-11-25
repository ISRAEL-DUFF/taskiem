// The own number's WhatsApp templates on the Channels page
// (docs/whatsapp.md#templates): each library template's status in the
// number's WhatsApp Business Account, and what that means for messages
// outside the 24-hour window.

export type TemplateStatus = "not_submitted" | "pending" | "approved" | "rejected" | "paused" | "disabled";

export interface TemplateView {
  name: string;
  category: "authentication" | "utility" | "marketing";
  language: string;
  status: TemplateStatus;
  reason?: string;
  meta_id?: string;
  submitted_at?: string;
  checked_at?: string;
  purpose: string;
  /** The category Meta gave it in this account, once Meta lists it. */
  meta_category?: string;
  /** Whether Meta's body and buttons are ours, once Meta lists them. */
  matches?: boolean;
}

export interface TemplatesView {
  tracked: boolean;
  templates: TemplateView[];
  note?: string;
}

/** The status in words, for the table. */
export function statusWords(s: TemplateStatus): string {
  switch (s) {
    case "not_submitted":
      return "Not submitted";
    case "pending":
      return "Waiting for Meta";
    case "approved":
      return "Approved";
    case "rejected":
      return "Rejected";
    case "paused":
      return "Paused by Meta";
    case "disabled":
      return "Disabled by Meta";
  }
}

/** The follow-up of an order form left half-filled: its category in
 * the account decides who may get it, not a warning (forms that sell §19). */
export const FINISH_ORDER = "taskiem_finish_order";

/** What Meta holds, when Meta lists it: its category there, and a
 * warning when that is not the category submitted or its words are not
 * ours (the follow-up of a form left half-filled is then not sent). The
 * follow-up filed as marketing still goes, only to buyers who ask on the
 * form; in any other category it does not. */
export function metaLine(t: TemplateView): { text: string; warn: boolean } | null {
  if (!t.meta_category) return null;
  if (t.matches === false) return { text: `Meta: ${t.meta_category}, with other words than ours`, warn: true };
  if (t.name === FINISH_ORDER && t.meta_category === "marketing")
    return { text: "Meta: marketing, so only buyers who tick the box on your order form get it", warn: false };
  if (t.meta_category !== t.category) return { text: `Meta filed it as ${t.meta_category}, not ${t.category}`, warn: true };
  return { text: `Meta: ${t.meta_category}`, warn: false };
}

/** Whether Submit is offered: a template Meta does not hold, or one it
 * deleted, can be submitted; the rest are Meta's to change. */
export function canSubmit(t: TemplateView): boolean {
  return t.status === "not_submitted";
}

/** How many are approved, of all. */
export function approvedCount(ts: TemplateView[]): { approved: number; total: number } {
  return { approved: ts.filter((t) => t.status === "approved").length, total: ts.length };
}

/** The one-line summary above the table. */
export function summaryLine(v: TemplatesView): string {
  if (!v.tracked) return "Connect your own number to submit the templates in its WhatsApp Business Account and follow their approval.";
  const { approved, total } = approvedCount(v.templates);
  if (approved === total) return `All ${total} templates are approved: every message goes on WhatsApp outside the 24-hour window too.`;
  return `${approved} of ${total} templates approved. ${v.note ?? ""}`.trim();
}
