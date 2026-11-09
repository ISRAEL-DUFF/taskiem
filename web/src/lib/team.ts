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

// --- a person's profile (step 2) ---

export type Weekday = "mon" | "tue" | "wed" | "thu" | "fri" | "sat" | "sun";
export const WEEKDAYS: { value: Weekday; label: string; short: string }[] = [
  { value: "mon", label: "Monday", short: "Mon" },
  { value: "tue", label: "Tuesday", short: "Tue" },
  { value: "wed", label: "Wednesday", short: "Wed" },
  { value: "thu", label: "Thursday", short: "Thu" },
  { value: "fri", label: "Friday", short: "Fri" },
  { value: "sat", label: "Saturday", short: "Sat" },
  { value: "sun", label: "Sunday", short: "Sun" },
];

export interface RotaShift {
  day: Weekday;
  start: string;
  end: string;
}

export type PayPer = "hour" | "day" | "week" | "month";

export interface StaffProfile {
  title: string;
  started_on?: string;
  note: string;
  pay_kobo?: number;
  pay_per?: PayPer;
  rota: RotaShift[];
}

export interface StaffPersonView extends StaffPerson {
  profile: StaffProfile;
}

/** "Mon to Fri, 08:00 to 16:00; Sat, 10:00 to 18:00": days in a row with
 * the same hours are said once. */
export function rotaWords(rota: RotaShift[]): string {
  if (rota.length === 0) return "No usual hours set";
  const order = WEEKDAYS.map((d) => d.value);
  const sorted = [...rota].sort((a, b) => order.indexOf(a.day) - order.indexOf(b.day));
  const runs: { from: Weekday; to: Weekday; start: string; end: string }[] = [];
  for (const r of sorted) {
    const last = runs[runs.length - 1];
    if (last && last.start === r.start && last.end === r.end && order.indexOf(r.day) === order.indexOf(last.to) + 1) last.to = r.day;
    else runs.push({ from: r.day, to: r.day, start: r.start, end: r.end });
  }
  const short = (d: Weekday) => WEEKDAYS.find((w) => w.value === d)?.short ?? d;
  return runs.map((r) => `${r.from === r.to ? short(r.from) : `${short(r.from)} to ${short(r.to)}`}, ${r.start} to ${r.end}`).join("; ");
}

const PER_WORDS: Record<PayPer, string> = { hour: "an hour", day: "a day", week: "a week", month: "a month" };

/** "₦80,000 a month", or "" when none is set. */
export function payWords(p: Pick<StaffProfile, "pay_kobo" | "pay_per">, naira: (k: number) => string): string {
  if (p.pay_kobo === undefined || !p.pay_per) return "";
  return `${naira(p.pay_kobo)} ${PER_WORDS[p.pay_per]}`;
}

/** The profile as its form holds it: pay in naira as typed, a row a day. */
export interface ProfileForm {
  title: string;
  started_on: string;
  note: string;
  pay: string;
  pay_per: PayPer;
  days: Record<Weekday, { on: boolean; start: string; end: string }>;
}

export function profileForm(p: StaffProfile): ProfileForm {
  const days = Object.fromEntries(
    WEEKDAYS.map((d) => {
      const r = p.rota.find((x) => x.day === d.value);
      return [d.value, r ? { on: true, start: r.start, end: r.end } : { on: false, start: "08:00", end: "16:00" }];
    }),
  ) as ProfileForm["days"];
  return {
    title: p.title,
    started_on: p.started_on ?? "",
    note: p.note,
    pay: p.pay_kobo !== undefined ? String(p.pay_kobo / 100) : "",
    pay_per: p.pay_per ?? "month",
    days,
  };
}

/** Why the form will be refused, or null. */
export function profileProblem(f: ProfileForm): string | null {
  if (f.title.trim().length > 60) return "A job title is at most 60 letters.";
  if (f.note.trim().length > 500) return "A note is at most 500 letters.";
  const pay = f.pay.replace(/[,\s₦]/g, "");
  if (pay !== "" && !/^\d+(\.\d{1,2})?$/.test(pay)) return "The pay is a number of naira, like 80000.";
  for (const d of WEEKDAYS) {
    const x = f.days[d.value];
    if (!x.on) continue;
    if (!/^\d{2}:\d{2}$/.test(x.start) || !/^\d{2}:\d{2}$/.test(x.end)) return `Give ${d.label}'s hours, like 08:00 and 16:00.`;
    if (x.start === x.end) return `${d.label} starts and ends at the same time.`;
  }
  return null;
}

/** The PUT /v1/staff/{user}/profile body. Call it once profileProblem is null. */
export function profileRequest(f: ProfileForm): StaffProfile {
  const pay = f.pay.replace(/[,\s₦]/g, "");
  return {
    title: f.title.trim(),
    started_on: f.started_on || undefined,
    note: f.note.trim(),
    pay_kobo: pay === "" ? undefined : Math.round(Number(pay) * 100),
    pay_per: pay === "" ? undefined : f.pay_per,
    rota: WEEKDAYS.filter((d) => f.days[d.value].on).map((d) => ({ day: d.value, start: f.days[d.value].start, end: f.days[d.value].end })),
  };
}

// --- clocking in (step 3) ---

export interface StaffShift {
  id: string;
  user_id: string;
  name?: string;
  in_at: string;
  out_at?: string;
  out_how?: "chat" | "web" | "handover" | "auto";
  /** A maps link of where they clocked in, when they shared it. */
  in_where?: string;
}

export interface StaffLate {
  user_id: string;
  name: string;
  /** "08:00" */
  due: string;
}

export interface StaffDay {
  day: string;
  shifts: StaffShift[];
  in: StaffShift[];
  late: StaffLate[];
}

const hm = (iso: string) =>
  new Date(iso).toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: "Africa/Lagos" });

/** "8 hours 5 minutes", "45 minutes". */
export function lengthWords(ms: number): string {
  const mins = Math.round(ms / 60_000);
  const h = Math.floor(mins / 60);
  const m = mins % 60;
  const part = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;
  if (h === 0) return part(m, "minute");
  if (m === 0) return part(h, "hour");
  return `${part(h, "hour")} ${part(m, "minute")}`;
}

/** A shift's line: "08:25 to 16:30 · 8 hours 5 minutes", "Since 08:25". */
export function shiftWords(s: StaffShift): string {
  if (!s.out_at) return `Since ${hm(s.in_at)}`;
  const how = s.out_how === "auto" ? " · closed by Taskiem" : s.out_how === "handover" ? " · handed over" : "";
  return `${hm(s.in_at)} to ${hm(s.out_at)} · ${lengthWords(Date.parse(s.out_at) - Date.parse(s.in_at))}${how}`;
}

/** The briefing's staff tile: "2 in · 1 late". */
export function staffTile(n: { in: unknown[]; late: unknown[] }): string {
  const parts = [`${n.in.length} in`];
  if (n.late.length > 0) parts.push(`${n.late.length} late`);
  return parts.join(" · ");
}

/** One person's figures for a period (phase 3, step 5). */
export interface StaffFigures {
  period: "day" | "week" | "month";
  from: string;
  to: string;
  minutes: number;
  shifts: number;
  late_days: string[];
  missed_days: string[];
  sales: { count: number; total_kobo: number; by_how: Record<string, number> };
  handovers: { at: string; counted_kobo?: number; cash_kobo?: number; diff_kobo?: number }[];
  orders: number;
  tasks: { done: number; done_late: number; cant: number; open: number; late: number };
}

const HOW_WORDS: Record<string, string> = { cash: "Cash", transfer: "Transfer", pos: "POS" };
const count = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** The figures as label and words, in the order the page shows them. */
export function figureRows(f: StaffFigures, money: (kobo: number) => string): [string, string][] {
  const rows: [string, string][] = [];
  const hours = f.shifts === 0 ? "None clocked" : `${lengthWords(f.minutes * 60_000)} over ${count(f.shifts, "shift")}`;
  rows.push(["Hours", hours]);
  const late = [f.late_days.length > 0 && `late on ${count(f.late_days.length, "day")}`, f.missed_days.length > 0 && `did not come on ${count(f.missed_days.length, "day")} they were due`]
    .filter(Boolean)
    .join(", ");
  if (late) rows.push(["Time keeping", late.charAt(0).toUpperCase() + late.slice(1)]);
  if (f.sales.count > 0) {
    const how = ["cash", "transfer", "pos"].filter((k) => f.sales.by_how[k] !== undefined).map((k) => `${HOW_WORDS[k]} ${money(f.sales.by_how[k] ?? 0)}`);
    rows.push(["Sales", `${money(f.sales.total_kobo)} in ${count(f.sales.count, "sale")} (${how.join(", ")})`]);
  } else rows.push(["Sales", "None recorded"]);
  if (f.handovers.length > 0) {
    // Handovers from before the cash sold was kept have no difference to show.
    const known = f.handovers.filter((h) => h.diff_kobo !== undefined);
    const diff = known.reduce((t, h) => t + (h.diff_kobo ?? 0), 0);
    const cash = known.length === 0 ? "" : diff < 0 ? `, ${money(-diff)} short` : diff > 0 ? `, ${money(diff)} over` : ", the cash right";
    rows.push(["Handovers", `${f.handovers.length}${cash}`]);
  }
  rows.push(["Orders worked", f.orders === 0 ? "None" : String(f.orders)]);
  const t = f.tasks;
  const parts = [
    t.done > 0 && `${t.done} done${t.done_late > 0 ? ` (${t.done_late} late)` : ""}`,
    t.open > 0 && `${t.open} open${t.late > 0 ? ` (${t.late} late)` : ""}`,
    t.cant > 0 && `${t.cant} sent back`,
  ].filter(Boolean);
  rows.push(["Tasks", parts.length ? parts.join(", ") : "None"]);
  return rows;
}
