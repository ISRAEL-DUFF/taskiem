/**
 * Settings > Notifications (no-technical-steps N12; docs/alerts.md): one
 * row per group the server lists, with a chat and an email choice. The
 * server may list groups this page does not know yet; they are shown with
 * plain labels rather than breaking the page.
 */

export interface NotificationSetting {
  group: string;
  chat: boolean;
  email: boolean;
}

/** One choice in a row: its accessible name, and words shown beside the box. */
export interface Choice {
  label: string;
  caption?: string;
}

export interface GroupRow {
  setting: NotificationSetting;
  title: string;
  hint: string;
  chat: Choice;
  email: Choice;
}

interface Known {
  title: string;
  hint: string;
  chat?: Choice;
  email?: Choice;
}

const KNOWN: Record<string, Known> = {
  failures: { title: "Failed runs", hint: "A run failed, or needs a person to check something. Owners, whoever started it and whoever published it." },
  setup: { title: "Setup problems", hint: "Something Taskiem needs you to fix: a domain, a connection, your data tables. Owners." },
  finished: { title: "Your runs that finish", hint: "A run you started yourself finished." },
  // Listed only for people who can see invoices (docs/money-reports.md).
  money: {
    title: "Money summaries",
    hint: "Yesterday's money each morning from 08:00 on WhatsApp or Telegram, and last month's on the 1st by email. Owners get them unless they turn them off; anyone else who can see invoices can turn them on.",
    chat: { label: "Daily money summary (chat)", caption: "Daily money summary" },
    email: { label: "Monthly money email", caption: "Monthly money email" },
  },
};

/** A group's name as words: "late_payments" is "Late payments". */
export function groupTitle(group: string): string {
  const words = group.replace(/[_-]+/g, " ").trim();
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : "Other";
}

/** The rows to show, in the server's order, unknown groups included. */
export function groupRows(settings: readonly NotificationSetting[] | undefined): GroupRow[] {
  return (settings ?? [])
    .filter((s) => s && typeof s.group === "string")
    .map((s) => {
      const k = Object.hasOwn(KNOWN, s.group) ? KNOWN[s.group] : undefined;
      const title = k?.title ?? groupTitle(s.group);
      return {
        setting: { group: s.group, chat: !!s.chat, email: !!s.email },
        title,
        hint: k?.hint ?? "",
        chat: k?.chat ?? { label: `${title} on WhatsApp or Telegram` },
        email: k?.email ?? { label: `${title} by email` },
      };
    });
}
