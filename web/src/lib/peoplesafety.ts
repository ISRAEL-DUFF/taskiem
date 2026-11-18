// Pure helpers for keeping people chats safe (docs/spec/people-chatting.md
// §8, "As built: stage 5a gaps, safety and moderation"): the moderators'
// screens in the operator console (src/ops/People.tsx, src/ops/Lawful.tsx),
// the person's own standing (pages/YouStanding.tsx) and the published count
// of lawful requests (pages/Transparency.tsx).

/** Why a person reported someone (api/people_chats.go reportInput). */
export const REPORT_REASONS: Record<string, string> = {
  spam: "Spam",
  scam: "A scam or fraud",
  abuse: "Abuse or harassment",
  other: "Something else",
};

export const reportReasonLabel = (r: string) => REPORT_REASONS[r] ?? r;

/** What an automated check found (engine/people screen reasons). */
export function checkReasonLabel(r: string): string {
  const known: Record<string, string> = {
    transfer: "An account number with banking words",
    send_money: "Asks for money to be sent",
    prize: "Promises a prize",
    fee: "Asks for a fee to release something",
    code: "Asks for a code, PIN, BVN or card",
    link: "Has a link",
    repeat: "Many warned messages to strangers today",
    investment: "Promises doubled money or sure returns",
  };
  return known[r] ?? r.replaceAll("_", " ");
}

export type ModerationAction = "warn" | "limit" | "suspend" | "close";

/** A moderator's action, as a verb on a button and in a list. */
export const ACTION_LABELS: Record<ModerationAction, string> = {
  warn: "Warn",
  limit: "Limit requests",
  suspend: "Suspend",
  close: "Close the account",
};

/** The actions that ask for the passkey and a confirmation (all ask for the passkey). */
export const actionIsGrave = (a: ModerationAction) => a === "suspend" || a === "close";

/** What the person is told an action does, before a moderator takes it. */
export function actionHelp(a: ModerationAction): string {
  switch (a) {
    case "warn":
      return "They are told, with your reason. Nothing else changes.";
    case "limit":
      return "They can send only one message request at a time, and two a day, for the days you choose (7 if you leave it empty).";
    case "suspend":
      return "They can't message people or get messages until the days are over (or until lifted, with 0 or empty). They can still read their chats.";
    case "close":
      return "They can't message people at once, and the account is deleted in 30 days unless an appeal is open or the closure is lifted.";
  }
}

/** The step-up operation a moderator's action is bound to (api/ops_people.go). */
export const actionOperation = (a: ModerationAction) => `ops.people.${a}`;

/** A file a report keeps, opened with the reason (each opening recorded). */
export function reportFileURL(report: string, message: string, why: string): string {
  return `/v1/ops/people/reports/${encodeURIComponent(report)}/files/${encodeURIComponent(message)}?why=${encodeURIComponent(why)}`;
}

/** A held message's file, opened with the reason. */
export function heldFileURL(id: string, why: string): string {
  return `/v1/ops/people/held/${encodeURIComponent(id)}/file?why=${encodeURIComponent(why)}`;
}

/** A reason to open something is 3 to 500 characters (api/ops_people.go opsWhy). */
export const whyOK = (why: string) => {
  const n = [...why.trim()].length;
  return n >= 3 && n <= 500;
};

/** "1.5 GB" for a number of bytes. */
export function bytesWords(n: number): string {
  if (n >= 1 << 30) return `${(n / (1 << 30)).toFixed(1)} GB`;
  if (n >= 1 << 20) return `${(n / (1 << 20)).toFixed(1)} MB`;
  if (n >= 1 << 10) return `${Math.round(n / (1 << 10))} KB`;
  return `${n} bytes`;
}

/** Whether a file type is shown in place (a photo) or downloaded. */
export const isPhoto = (type: string) => /^image\/(png|jpeg|gif|webp)$/.test(type);

// --- the person's standing (GET /v1/me/standing) ---

export type StandingState = "ok" | "warned" | "limited" | "suspended" | "closing";

export interface StandingAppeal {
  id: string;
  text: string;
  state: "open" | "upheld" | "overturned";
  at: string;
  decided_at: string | null;
  note: string | null;
}

export interface StandingAction {
  id: string;
  action: ModerationAction;
  reason: string;
  at: string;
  until: string | null;
  in_force: boolean;
  lifted_at: string | null;
  seen: boolean;
  words: string;
  can_appeal: boolean;
  appeal: StandingAppeal | null;
}

export interface Standing {
  state: StandingState;
  words: string;
  until: string | null;
  actions: StandingAction[];
  unseen: number;
  appeal_words: string;
}

/** The standing's title, as You shows it. */
export function standingTitle(s: StandingState): string {
  switch (s) {
    case "ok":
      return "Your account is in good standing";
    case "warned":
      return "You have a warning";
    case "limited":
      return "Your message requests are limited";
    case "suspended":
      return "Your account can't message people for now";
    case "closing":
      return "Taskiem is closing your account";
  }
}

/** What became of an appeal, in plain words. */
export function appealWords(a: StandingAppeal): string {
  switch (a.state) {
    case "open":
      return "You appealed. A moderator will read it and we will tell you what they decide.";
    case "upheld":
      return `A moderator looked at your appeal and kept the decision.${a.note ? ` They said: ${a.note}` : ""}`;
    case "overturned":
      return `A moderator agreed with your appeal and lifted the decision.${a.note ? ` They said: ${a.note}` : ""}`;
  }
}

/** The name of an action in the person's list. */
export const actionName = (a: ModerationAction) =>
  ({ warn: "Warning", limit: "Limit on message requests", suspend: "Suspension", close: "Closure" })[a];

// --- lawful requests ---

export type LawfulState = "recorded" | "approved" | "answered" | "refused";

export const LAWFUL_STATES: Record<LawfulState, string> = {
  recorded: "Waiting for a second operator",
  approved: "Approved, not yet answered",
  answered: "Answered",
  refused: "Refused",
};

/** A half-year count as published (GET /v1/transparency/lawful-requests). */
export interface LawfulPeriod {
  year: number;
  half: 1 | 2;
  label: string;
  received: number;
  answered: number;
  refused: number;
  pending: number;
}

/** The sentence under a half-year's figures. */
export function periodWords(p: LawfulPeriod): string {
  if (p.received === 0) return "No requests.";
  const parts = [`${p.received} received`, `${p.answered} answered`, `${p.refused} refused`];
  if (p.pending > 0) parts.push(`${p.pending} still being looked at`);
  return parts.join(", ") + ".";
}

/** A date input's value (YYYY-MM-DD) as the start of that day in Lagos, RFC 3339. */
export function lagosDay(day: string, end = false): string | undefined {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(day)) return undefined;
  // Lagos is UTC+1 all year (no daylight saving).
  const d = new Date(`${day}T00:00:00+01:00`);
  if (Number.isNaN(d.getTime())) return undefined;
  if (end) d.setUTCDate(d.getUTCDate() + 1);
  return d.toISOString();
}

/** The published periods with the current half-year first, at zero when nothing came in it yet. */
export function withCurrentHalf(periods: LawfulPeriod[], now = new Date()): LawfulPeriod[] {
  // Counted by Lagos time (UTC+1).
  const lagos = new Date(now.getTime() + 60 * 60 * 1000);
  const year = lagos.getUTCFullYear();
  const half: 1 | 2 = lagos.getUTCMonth() < 6 ? 1 : 2;
  if (periods.some((p) => p.year === year && p.half === half)) return periods;
  const label = half === 1 ? `January to June ${year}` : `July to December ${year}`;
  return [{ year, half, label, received: 0, answered: 0, refused: 0, pending: 0 }, ...periods];
}
