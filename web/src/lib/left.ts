// Forms left half-filled (docs/spec/forms-that-sell.md §6.6): an order
// form's follow-ups, and who left it without ordering this week. The
// contact comes masked from the server ("… 4567", "…@example.com") and is
// never shown more.

export const MIN_WAIT = 10;
export const MAX_WAIT = 1440;

/** The waits the owner chooses from, in minutes. */
export const WAITS = [10, 15, 30, 60, 120, 240, 720, 1440];

export interface FollowupSettings {
  on: boolean;
  wait_minutes: number;
  held_at?: string;
  /** L4: a follow-up may go on WhatsApp from the business's own number. */
  whatsapp?: boolean;
  /** L4: only to a buyer who asks for it on the form (a marketing template). */
  whatsapp_opt_in?: boolean;
}

export interface LeftRow {
  id: string;
  contact_kind: "phone" | "email";
  contact: string;
  product: string;
  package?: number;
  zone?: string;
  at: string;
  followed_up_at?: string;
  came_back_at?: string;
  channel?: "sms" | "email" | "whatsapp";
  outcome?: "capped" | "stopped" | "limit" | "off" | "stale" | "failed";
}

export interface FormLeft {
  action_id: string;
  label: string;
  followups: FollowupSettings;
  week: { left: number; followed: number; came_back: number };
  left: LeftRow[];
}

/** "30 minutes", "1 hour", "12 hours", "24 hours". */
export function waitWords(minutes: number): string {
  if (minutes < 60) return `${minutes} minutes`;
  const h = minutes / 60;
  return h === 1 ? "1 hour" : `${Number.isInteger(h) ? h : h.toFixed(1)} hours`;
}

/** What the row was buying: "Smart Ear Cleaner, 2 items, to Lekki". */
export function choiceWords(r: LeftRow): string {
  const parts = [r.product || "Your order form"];
  if (r.package) parts.push(r.package === 1 ? "1 item" : `${r.package} items`);
  if (r.zone) parts.push(`to ${r.zone}`);
  return parts.join(", ");
}

const channelWords: Record<NonNullable<LeftRow["channel"]>, string> = { sms: "by text", email: "by email", whatsapp: "on WhatsApp" };

const outcomeWords: Record<NonNullable<LeftRow["outcome"]>, string> = {
  capped: "Not followed up: they had a message today already, or the day's follow-ups were used",
  stopped: "Not followed up: they stopped your messages",
  limit: "Not followed up: this month's follow-ups on your plan are used",
  off: "Not followed up: follow-ups were off",
  stale: "Not followed up: too long ago",
  failed: "Not followed up: the message could not be sent",
};

/** Where a row is: came back, followed up, why not, or waiting. */
export function rowStatus(r: LeftRow, held: boolean): { words: string; tone: "ok" | "info" | "muted" | "warn" } {
  if (r.came_back_at) return { words: r.followed_up_at ? `Came back and ordered, followed up ${channelWords[r.channel ?? "sms"]}` : "Came back and ordered", tone: "ok" };
  if (r.followed_up_at) return { words: `Followed up ${channelWords[r.channel ?? "sms"]}`, tone: "info" };
  if (r.outcome) return { words: outcomeWords[r.outcome], tone: "muted" };
  if (held) return { words: "Waiting: follow-ups are held", tone: "warn" };
  return { words: "Waiting to be followed up", tone: "muted" };
}

/** The contact as the owner may see it. */
export const contactWords = (r: LeftRow) => (r.contact_kind === "email" ? `Email ${r.contact}` : `Phone ${r.contact}`);

/** The week in one line, for a card: "3 left · 2 followed up · 1 came back". */
export function weekLine(w: FormLeft["week"]): string {
  return `${w.left} left · ${w.followed} followed up · ${w.came_back} came back`;
}

/** The consent line the form shows when follow-ups are on (engine/lang form.followup.consent). */
export const CONSENT_LINE =
  "If you leave before you send this, we may send you one message about this order, by text or email, with a link to finish it. You can stop these messages from that link.";

/** The consent line where a follow-up may go on WhatsApp (form.followup.consent_whatsapp, L4). */
export const CONSENT_LINE_WHATSAPP =
  "If you leave before you send this, we may send you one message about this order, on WhatsApp or by text or email, with a link to finish it. On WhatsApp, reply STOP to stop these messages; otherwise stop them from that link.";

/** The box under the line where the WhatsApp template is a marketing one (form.followup.whatsapp_optin, L4). */
export const WHATSAPP_OPT_IN = "Send that message on WhatsApp instead (reply STOP there to stop)";

/** How the follow-up goes, in a line for the owner (L4 adds WhatsApp). */
export function followupWords(f: FollowupSettings): string {
  const wait = waitWords(f.wait_minutes);
  if (f.whatsapp_opt_in)
    return `They get one message ${wait} after they leave: on WhatsApp from your number when they tick the box asking for it, else a text (or an email, when they gave only that). Texts count in your plan's texts, WhatsApp messages in its WhatsApp messages.`;
  if (f.whatsapp)
    return `They get one WhatsApp message from your number ${wait} after they leave, or a text when WhatsApp cannot take it (an email, when they gave only that). WhatsApp messages count in your plan's WhatsApp messages, texts in its texts.`;
  return `They get one text (or an email, when they gave only that) ${wait} after they leave. The texts count in your plan's texts.`;
}

/** What the form says under the contact: the line, and the box when there is one. */
export function consentWords(f: FollowupSettings): string {
  if (f.whatsapp_opt_in) return `“${CONSENT_LINE}” and a box: “${WHATSAPP_OPT_IN}”`;
  return `“${f.whatsapp ? CONSENT_LINE_WHATSAPP : CONSENT_LINE}”`;
}

