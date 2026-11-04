// Storefront trust and safety (docs/moderation.md, Phase 5 S8): the page's
// statuses as owners read them, verification and what it unlocks, the
// review queue's kinds and findings, reports, and the refusals a publish or
// a form's payment can meet.

import { ApiError } from "../api";

export type PageStatus = "draft" | "in_review" | "published" | "closed" | "suspended";

export type Tone = "ok" | "warn" | "info" | "muted" | "danger";

/** Each page status in words, with its tone for the badge. */
export const PAGE_STATUS: Record<PageStatus, { label: string; tone: Tone; help: string }> = {
  draft: { label: "Draft", tone: "warn", help: "Not on the web yet." },
  in_review: { label: "With Taskiem for a check", tone: "info", help: "Taskiem checks new pages before they go live, usually within a working day." },
  published: { label: "Live", tone: "ok", help: "Customers can see your page." },
  closed: { label: "Closed for now", tone: "muted", help: "Your card shows, with your message instead of the catalogue." },
  suspended: { label: "Suspended", tone: "danger", help: "Your page is hidden from customers until Taskiem restores it." },
};

/** A page status in words; an unknown one as it is. */
export function pageStatus(status: string): { label: string; tone: Tone; help: string } {
  return PAGE_STATUS[status as PageStatus] ?? { label: status.replaceAll("_", " "), tone: "muted", help: "" };
}

/** The page's state that trust adds to a storefront. */
export interface TrustState {
  status: string;
  review_note?: string | null;
  suspended_reason?: string | null;
  orders_paused?: string | null;
}

/** A first publish Taskiem turned down: back to a draft, with a note. */
export const turnedDown = (s: TrustState) => s.status === "draft" && !!s.review_note;

/** Whether the owner can appeal: the page is suspended or turned down. */
export const canAppeal = (s: TrustState) => s.status === "suspended" || turnedDown(s);

// --- verification ---

export interface Verification {
  phone: boolean;
  /** Checked by Taskiem, or onboarded by a field agent. */
  business: boolean;
  agent_onboarded: boolean;
}

export type VerificationLevel = keyof Verification;

/** The levels, what proves each and what each unlocks (docs/moderation.md#verification). */
export const VERIFICATION_LEVELS: { key: VerificationLevel; label: string; how: string; unlocks: string }[] = [
  {
    key: "phone",
    label: "Phone verified",
    how: "An owner or admin linked WhatsApp or Telegram, or agreed to a field agent from their number.",
    unlocks: "Publishing your page.",
  },
  {
    key: "business",
    label: "Business verified",
    how: "Taskiem checked your business's name and address, or a field agent visited.",
    unlocks: "The Verified business badge, payment when customers order, more than 50 orders and bookings a day, and going live without waiting.",
  },
  {
    key: "agent_onboarded",
    label: "Agent onboarded",
    how: "A field agent visited and recorded your place.",
    unlocks: "Counts as business verified.",
  },
];

// --- reviews ---

export type ReviewKind = "first_publish" | "edit" | "catalogue" | "reports" | "appeal" | "business";
export type ReviewStatus = "pending" | "approved" | "rejected";

/** What each kind of review is, for owners and operators. */
export const REVIEW_KIND: Record<ReviewKind, string> = {
  first_publish: "First publish",
  edit: "Change to a live page",
  catalogue: "Catalogue items",
  reports: "Reports from visitors",
  appeal: "Appeal",
  business: "Business verification",
};

export const reviewKind = (kind: string) => REVIEW_KIND[kind as ReviewKind] ?? kind.replaceAll("_", " ");

/** A review's status as the owner reads it. */
export const REVIEW_STATUS: Record<ReviewStatus, { label: string; tone: Tone }> = {
  pending: { label: "Waiting for Taskiem", tone: "warn" },
  approved: { label: "Approved", tone: "ok" },
  rejected: { label: "Turned down", tone: "danger" },
};

export const reviewStatus = (status: string) => REVIEW_STATUS[status as ReviewStatus] ?? { label: status, tone: "muted" as Tone };

/** What a check said about one piece of the page (engine/moderation Finding). */
export interface Finding {
  field: string;
  text: string;
  verdict: "allowed" | "flagged" | "blocked" | string;
  category: string;
  reason: string;
  checker: "words" | "price" | "model" | string;
}

export const CATEGORY: Record<string, string> = {
  weapons: "weapons",
  drugs: "drugs",
  counterfeit: "counterfeit goods",
  investment_scheme: "investment scheme",
  unlicensed_financial: "unlicensed financial service",
  adult: "adult content",
  phishing: "asking for passwords or bank details",
  impersonation: "pretending to be someone else",
  price: "price",
  other: "other",
};

export const categoryLabel = (c: string) => CATEGORY[c] ?? c.replaceAll("_", " ");

/** A one-line summary of findings: "1 blocked, 2 flagged: weapons, price". */
export function findingsSummary(findings: Finding[] | null | undefined): string {
  const list = (findings ?? []).filter((f) => f.verdict === "blocked" || f.verdict === "flagged");
  if (list.length === 0) return "Nothing found";
  const blocked = list.filter((f) => f.verdict === "blocked").length;
  const flagged = list.length - blocked;
  const counts = [blocked ? `${blocked} blocked` : "", flagged ? `${flagged} flagged` : ""].filter(Boolean).join(", ");
  const cats = [...new Set(list.map((f) => categoryLabel(f.category)).filter(Boolean))];
  return cats.length ? `${counts}: ${cats.join(", ")}` : counts;
}

/** A finding's field as people read it: "item 12" is a catalogue item. */
export const fieldLabel = (field: string) => (field.startsWith("item ") ? `Catalogue item ${field.slice(5)}` : field.replaceAll("_", " "));

/** What an owner asked Taskiem to check about their business. */
export interface BusinessDetail {
  legal_name: string;
  address: string;
  cac_number: string;
  note: string;
}

/** A review's detail as a business check, when it is one. */
export function businessDetail(detail: unknown): BusinessDetail | null {
  if (!detail || typeof detail !== "object") return null;
  const d = detail as Record<string, unknown>;
  if (typeof d.legal_name !== "string") return null;
  const s = (v: unknown) => (typeof v === "string" ? v : "");
  return { legal_name: d.legal_name, address: s(d.address), cac_number: s(d.cac_number), note: s(d.note) };
}

/** An appeal's message, when the detail has one. */
export function appealMessage(detail: unknown): string | null {
  const m = detail && typeof detail === "object" ? (detail as Record<string, unknown>).message : undefined;
  return typeof m === "string" ? m : null;
}

/** What a reviewer is deciding, for each kind: [approve, reject]. */
export const DECISION_HELP: Record<ReviewKind, [string, string]> = {
  first_publish: ["The page goes live.", "It goes back to a draft with your note."],
  edit: ["Nothing changes.", "The page is suspended with your note."],
  catalogue: ["Nothing changes.", "The page is suspended with your note."],
  reports: ["The page is restored and the reports dismissed.", "It stays suspended and the reports are marked acted on."],
  appeal: ["A suspended page is restored.", "It stays as it is."],
  business: ["The business is verified.", "It is not verified; the owner gets your note."],
};

// --- reports ---

export const REPORT_REASON: Record<string, string> = {
  scam: "Looks like a scam",
  impersonation: "Pretends to be someone else",
  prohibited: "Sells something not allowed",
  offensive: "Offensive",
  other: "Something else",
};

export const reportReason = (r: string) => REPORT_REASON[r] ?? r;

export const REPORT_STATUS: Record<string, string> = { open: "Open", dismissed: "Dismissed", actioned: "Acted on" };

// --- refusals ---

export type Refusal = "verification" | "moderation" | "plan" | "suspended";

/** What kind of refusal an error is, for publishing and form payment. */
export function refusalOf(e: unknown): Refusal | null {
  if (!(e instanceof ApiError)) return null;
  if (e.status === 402 && e.body.code === "plan_feature_required") return "plan";
  if (e.status === 409 && /^verification needed/i.test(e.message)) return "verification";
  if (e.status === 409 && /suspended/i.test(e.message)) return "suspended";
  if (e.status === 400 && /cannot go on a public page/i.test(e.message)) return "moderation";
  return null;
}

/** A refusal's message without its "verification needed: " prefix, capitalised. */
export function refusalMessage(msg: string): string {
  const m = msg.replace(/^verification needed:\s*/i, "");
  return m ? m.charAt(0).toUpperCase() + m.slice(1) : msg;
}

/** What to do about each refusal. */
export const REFUSAL_HELP: Record<Refusal, string> = {
  verification: "Verify first: link WhatsApp or Telegram under Account for your phone, or ask Taskiem to verify your business below.",
  moderation: "Change the words named above, then publish again. If you think this is wrong, publish without them and appeal.",
  plan: "Your plan does not include this. Change plan under Billing.",
  suspended: "Your page is suspended. Appeal below, and Taskiem looks at it again.",
};

/** What the owner is told after a publish. */
export function publishNotice(sf: { status: string; published_version: number }): string {
  if (sf.status === "in_review") return "Sent to Taskiem for a quick check. Your page goes live once it is approved, usually within a working day.";
  return `Published version ${sf.published_version}.`;
}

/** The owner's trust, from GET /v1/storefront/trust. */
export interface Trust extends TrustState {
  verification: Verification;
  reviews: {
    id: string;
    kind: ReviewKind;
    status: ReviewStatus;
    findings: Finding[];
    decision_note: string | null;
    created_at: string;
    decided_at: string | null;
  }[];
}

/** A pending review of that kind, if one waits. */
export const pendingOf = (t: Pick<Trust, "reviews"> | undefined, kind: ReviewKind) => t?.reviews.find((r) => r.kind === kind && r.status === "pending");

/** A stored image's smallest URL (by width), or undefined. */
export function smallest(urls: Record<string, string> | null | undefined): string | undefined {
  const keys = Object.keys(urls ?? {}).sort((a, b) => Number(a) - Number(b));
  const k = keys[0];
  return k !== undefined ? urls?.[k] : undefined;
}
