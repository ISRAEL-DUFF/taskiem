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
