// Tasks for staff (docs/spec/chat-five-more-flows.md, flow 2; phase 3
// plan, step 4): the words the Tasks page and the chat's Tasks sheet show,
// and the form for giving one.

import { WEEKDAYS, roleWords, type Weekday } from "./team";

export type TaskState = "open" | "done" | "cant" | "cancelled";
export type TaskLinkKind = "order" | "invoice" | "bill" | "purchase" | "stock";

export interface TaskLink {
  kind: TaskLinkKind;
  id: string;
  name?: string;
}

export interface StaffTask {
  id: string;
  what: string;
  given_by: string;
  giver: string;
  to_user?: string;
  to_role?: string;
  taken_by?: string;
  person?: string;
  due_at?: string;
  photo: boolean;
  state: TaskState;
  done_at?: string;
  photo_url?: string;
  cant_why?: string;
  link?: TaskLink;
  repeat_id?: string;
  created_at: string;
  late: boolean;
}

export interface TaskRepeat {
  id: string;
  what: string;
  to_user?: string;
  to_role?: string;
  person?: string;
  photo: boolean;
  every: "day" | "week";
  day?: Weekday;
  at: string;
}

export interface TasksView {
  tasks: StaffTask[];
  repeats: TaskRepeat[];
  give: boolean;
}

const LAGOS = "Africa/Lagos";
const dayKey = (d: Date) => d.toLocaleDateString("en-CA", { timeZone: LAGOS });
const hm = (d: Date) => d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit", hour12: false, timeZone: LAGOS });

/** "any cashier", "Tunde", or "Tunde (any cashier)". */
export function taskWho(t: Pick<StaffTask, "to_role" | "person" | "taken_by">): string {
  if (!t.to_role) return t.person || "Someone";
  const r = `any ${roleWords(t.to_role).toLowerCase()}`;
  return t.taken_by && t.person ? `${t.person} (${r})` : r;
}

/** "by 15:00", "by tomorrow 15:00", "by Mon 10 Nov, 15:00". */
export function dueWords(dueAt: string, now = new Date()): string {
  const d = new Date(dueAt);
  const k = dayKey(d);
  const day = (n: number) => dayKey(new Date(now.getTime() + n * 86_400_000));
  if (k === day(0)) return `by ${hm(d)}`;
  if (k === day(1)) return `by tomorrow ${hm(d)}`;
  if (k === day(-1)) return `by yesterday ${hm(d)}`;
  const date = d.toLocaleDateString("en-GB", { weekday: "short", day: "numeric", month: "short", timeZone: LAGOS });
  return `by ${date}, ${hm(d)}`;
}

/** What a task's row says under it. */
export function taskSub(t: StaffTask, mine: boolean, now = new Date()): string {
  const parts: string[] = [];
  if (!mine) parts.push(taskWho(t));
  else if (t.to_role && !t.taken_by) parts.push(`for ${taskWho(t)}`);
  if (t.state === "open" && t.due_at) parts.push(dueWords(t.due_at, now));
  if (t.state === "done") parts.push(t.done_at ? `done at ${hm(new Date(t.done_at))}` : "done");
  if (t.state === "cant") parts.push(t.cant_why ? `can't: "${t.cant_why}"` : "sent back");
  if (t.state === "cancelled") parts.push("called off");
  if (t.photo && t.state === "open") parts.push("photo when done");
  if (mine && t.giver) parts.push(`from ${t.giver}`);
  return parts.join(" · ");
}

/** The badge a task carries, if any. */
export function taskBadge(t: StaffTask): { tone: "ok" | "warn" | "danger" | "muted"; text: string } | null {
  if (t.state === "done") return { tone: "ok", text: "Done" };
  if (t.state === "cant") return { tone: "danger", text: "Can't" };
  if (t.state === "cancelled") return { tone: "muted", text: "Called off" };
  if (t.late) return { tone: "warn", text: "Late" };
  return null;
}

/** "Every day at 07:00", "Every Monday at 09:00". */
export function repeatWords(r: Pick<TaskRepeat, "every" | "day" | "at">): string {
  if (r.every === "day") return `Every day at ${r.at}`;
  const day = WEEKDAYS.find((w) => w.value === r.day)?.label ?? "week";
  return `Every ${day} at ${r.at}`;
}

/** The form for giving a task. who is "user:<id>" or "role:<role>". */
export interface TaskForm {
  what: string;
  who: string;
  when: "none" | "by" | "day" | "week";
  date: string; // YYYY-MM-DD, for "by"
  time: string; // HH:MM, for "by" and the repeats
  day: Weekday;
  photo: boolean;
}

export function emptyTask(today: string): TaskForm {
  return { what: "", who: "", when: "none", date: today, time: "", day: "mon", photo: false };
}

/** Today in Lagos, as YYYY-MM-DD. */
export const lagosToday = (now = new Date()) => dayKey(now);

export function taskProblem(f: TaskForm): string | null {
  if (!f.what.trim()) return "Say what is to be done.";
  if (f.what.trim().length > 200) return "Keep what is to be done to 200 letters.";
  if (!f.who) return "Choose who it is for.";
  if (f.when !== "none" && !/^([01]\d|2[0-3]):[0-5]\d$/.test(f.time)) return "Choose the time it is due.";
  if (f.when === "by" && !/^\d{4}-\d{2}-\d{2}$/.test(f.date)) return "Choose the day it is due.";
  return null;
}

/** The time a date and an HH:MM are in Lagos (UTC+1, no summer time). */
export function lagosTime(date: string, time: string): string {
  return new Date(`${date}T${time}:00+01:00`).toISOString();
}

export function taskRequest(f: TaskForm, link?: TaskLink): Record<string, unknown> {
  const [kind, id] = [f.who.slice(0, f.who.indexOf(":")), f.who.slice(f.who.indexOf(":") + 1)];
  const body: Record<string, unknown> = { what: f.what.trim(), photo: f.photo };
  if (kind === "user") body.to_user = id;
  else body.to_role = id;
  if (f.when === "by") body.due_at = lagosTime(f.date, f.time);
  if (f.when === "day") body.repeat = { every: "day", at: f.time };
  if (f.when === "week") body.repeat = { every: "week", day: f.day, at: f.time };
  if (link) body.link = link;
  return body;
}

/** What a card's "Give this to…" starts the task with. */
export function linkedWhat(link: TaskLink): string {
  switch (link.kind) {
    case "order":
      return `Pack order ${link.name ?? link.id}`;
    case "invoice":
      return `Follow up invoice ${link.name ?? link.id}`;
    case "bill":
      return `Check bill ${link.name ?? link.id}`;
    case "purchase":
      return `Receive order ${link.name ?? link.id}`;
    case "stock":
      return `Restock ${link.name ?? "this item"}`;
  }
}

/** The briefing's tasks tile: "3 today · 1 late". */
export function tasksTile(n: { today: number; late: number }): string {
  const parts: string[] = [];
  if (n.today > 0) parts.push(`${n.today} today`);
  if (n.late > 0) parts.push(`${n.late} late`);
  return parts.join(" · ") || "None due";
}
