// The Staff page (docs/spec/chat-five-more-flows.md, phase 3 plan): the
// people of the business, added by phone, and the invitations waiting for
// them to tap Join. Pure helpers, tested on their own.

import { dateLabel } from "./invoices";

export interface StaffPerson {
  user_id: string;
  name: string;
  /** None for someone who joined by phone. */
  email?: string;
  phone?: string;
  roles: string[];
  /** Their WhatsApp or Telegram is linked. */
  chat: boolean;
  you?: boolean;
}

export interface StaffInvite {
  id: string;
  name?: string;
  phone: string;
  roles: string[];
  inviter: string;
  sent_at?: string;
  expires_at: string;
  created_at: string;
}

export interface StaffView {
  people: StaffPerson[];
  invites: StaffInvite[];
}

const ROLE_WORDS: Record<string, string> = {
  owner: "Owner",
  admin: "Admin",
  cashier: "Cashier",
  shift_manager: "Shift manager",
  viewer: "Viewer",
  operator: "Operator",
  approver: "Approver",
  builder: "Builder",
  auditor: "Auditor",
};

/** A role as people say it: shift_manager is "Shift manager". */
export const roleWords = (role: string) =>
  ROLE_WORDS[role] ?? role.charAt(0).toUpperCase() + role.slice(1).replaceAll("_", " ");

/** The roles offered first when adding staff: what staff who work from
 * chat do, then the business's own roles. */
export const STAFF_ROLES: { value: string; label: string; help: string }[] = [
  { value: "cashier", label: "Cashier", help: "Sells, keeps the stock and works the orders." },
  { value: "shift_manager", label: "Shift manager", help: "A cashier's work, plus payments and handing the shift over." },
  { value: "admin", label: "Admin", help: "Almost everything, but not billing or owners." },
];

export interface StaffForm {
  name: string;
  phone: string;
  role: string;
}

export const emptyStaff = (): StaffForm => ({ name: "", phone: "", role: "cashier" });

/** Why the form will be refused, or null. */
export function staffProblem(f: StaffForm): string | null {
  if (!f.name.trim()) return "Give their name.";
  if (f.name.trim().length > 80) return "A name is at most 80 letters.";
  const digits = f.phone.replace(/\D/g, "");
  if (digits.length < 7 || digits.length > 15) return "Give their mobile number, like 0803 123 4567.";
  if (!f.role) return "Choose what they do.";
  return null;
}

/** A person's line: their roles, and how Taskiem reaches them. */
export function personSub(p: StaffPerson): string {
  const how = p.phone ?? p.email ?? "";
  return [p.roles.map(roleWords).join(", "), how].filter(Boolean).join(" · ");
}

/** An invitation's line: the role, the number, and when it was sent. */
export function inviteSub(x: StaffInvite): string {
  const sent = x.sent_at ? `sent ${dateLabel(x.sent_at)}` : "not sent yet";
  return [x.roles.map(roleWords).join(", "), x.phone, sent].join(" · ");
}
