// The Schools area (decision 0046; docs/schools.md): the shapes the API
// answers, and the logic the pages share, kept here so it is tested.

import { dateLabel, plainMessage } from "./invoices";

export { dateLabel, plainMessage };

export interface SchoolClass {
  id: string;
  name: string;
  arm: string;
  order: number;
  label: string;
  students: number;
  created_at: string;
}

export type TermState = "planned" | "current" | "past";

export interface SchoolTerm {
  id: string;
  session_id: string;
  session: string;
  name: string;
  starts_on: string;
  ends_on: string;
  fees_due_on: string;
  state: TermState;
  started_at: string | null;
  enrolled: number;
}

export interface SchoolSession {
  id: string;
  label: string;
  terms: SchoolTerm[];
  promoted_at: string | null;
  created_at: string;
}

export type StudentStatus = "active" | "withdrawn" | "graduated";

export interface SchoolStudent {
  id: string;
  admission_number: string;
  name: string;
  date_of_birth?: string;
  class_id: string;
  class: string;
  status: StudentStatus;
  family_id: string | null;
  created_at: string;
  updated_at: string;
}

export interface SchoolGuardian {
  id: string;
  name: string;
  phone: string;
  email?: string;
  relationship?: string;
}

export interface SchoolFamily {
  id: string;
  guardians: SchoolGuardian[];
  students: { id: string; admission_number: string; name: string; class: string; status: StudentStatus }[];
  created_at: string;
}

export interface SchoolEnrollment {
  id: string;
  term_id: string;
  term: string;
  session: string;
  term_state: "current" | "past";
  student_id: string;
  admission_number: string;
  class_id: string;
  class: string;
  created_at: string;
  updated_at: string;
}

export interface SchoolStudentDetail extends SchoolStudent {
  family: SchoolFamily | null;
  enrollments: SchoolEnrollment[];
}

export type PromotionAction = "promote" | "repeat" | "leave" | "graduate";

export interface SchoolPromotion {
  session_id: string;
  session: string;
  classes: { class_id: string; class: string; next_id: string | null; next: string; students: number }[];
  students: { id: string; admission_number: string; name: string; class: string; action: PromotionAction; next_id: string | null; next: string }[];
  counts: Record<string, number>;
  promoted_at: string | null;
  done: boolean;
}

export interface GuardianInput {
  name: string;
  phone: string;
  email?: string;
  relationship?: string;
}

export interface ImportPlan {
  hash: string;
  added: number;
  updated: number;
  students: { row: number; admission_number: string; name: string; class: string; action: "add" | "update"; family: number }[];
  families: { index: number; family_id: string | null; existing: boolean; guardians: GuardianInput[]; students: string[] }[];
  saved: boolean;
}

export interface SchoolSettings {
  enabled: boolean;
  bursar_role: boolean;
}

// --- what a person may do ---

/** What a person may see and do in the Schools area: the sidebar's entry, the changes, the details. */
export function schoolAccess(can: (perm: string) => boolean): { see: boolean; manage: boolean; reveal: boolean } {
  return { see: can("school.read"), manage: can("school.manage"), reveal: can("pii.reveal") };
}

/** Whether the Schools entry shows in the sidebar: the area on, and the person may read it. */
export function showSchools(on: boolean | undefined, can: (perm: string) => boolean): boolean {
  return !!on && schoolAccess(can).see;
}

// --- words ---

export const STUDENT_STATUS: Record<StudentStatus, { label: string; tone: string }> = {
  active: { label: "Active", tone: "ok" },
  withdrawn: { label: "Withdrawn", tone: "muted" },
  graduated: { label: "Graduated", tone: "info" },
};

export function studentStatus(s: string): { label: string; tone: string } {
  return STUDENT_STATUS[s as StudentStatus] ?? { label: s, tone: "muted" };
}

export const STATUS_FILTERS: { value: string; label: string }[] = [
  { value: "active", label: "Active" },
  { value: "all", label: "Everyone" },
  { value: "withdrawn", label: "Withdrawn" },
  { value: "graduated", label: "Graduated" },
];

export function termStateWords(s: TermState): string {
  return s === "current" ? "Current term" : s === "past" ? "Past (read-only)" : "Not started";
}

export const PROMOTION_ACTIONS: { value: PromotionAction; label: string }[] = [
  { value: "promote", label: "Move up" },
  { value: "repeat", label: "Repeat the class" },
  { value: "leave", label: "Leaving the school" },
  { value: "graduate", label: "Graduate" },
];

/** A class's label as the API writes it: the name, then the arm. */
export function classLabel(name: string, arm: string): string {
  const n = name.trim().replace(/\s+/g, " ");
  const a = arm.trim().replace(/\s+/g, " ");
  return a ? `${n} ${a}` : n;
}

/** Whether a name is masked by the server ("E•••"). */
export function isMasked(name: string): boolean {
  return name.includes("•••");
}

/** A guardian as the family page shows them: name, relationship, phone. */
export function guardianLine(g: SchoolGuardian | GuardianInput): string {
  return [g.relationship ? `${g.name} (${g.relationship})` : g.name, g.phone, g.email].filter(Boolean).join(" · ");
}

/** A family's name, from its first guardian. */
export function familyName(f: SchoolFamily): string {
  return f.guardians[0]?.name ? `${f.guardians[0].name}'s family` : "A family with no guardian yet";
}

const count = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

/** What an import's plan does, in a sentence. */
export function planLine(p: Pick<ImportPlan, "added" | "updated" | "families">): string {
  const parts: string[] = [];
  if (p.added) parts.push(`add ${count(p.added, "student", "students")}`);
  if (p.updated) parts.push(`update ${count(p.updated, "student", "students")} already here`);
  const fresh = p.families.filter((f) => !f.existing).length;
  const kept = p.families.length - fresh;
  const fam = [fresh ? count(fresh, "new family", "new families") : "", kept ? count(kept, "family already here", "families already here") : ""].filter(Boolean);
  return `This file will ${parts.join(" and ") || "change nothing"}${fam.length ? `, in ${fam.join(" and ")}` : ""}.`;
}

/** What saving did, in a sentence. */
export function savedLine(p: Pick<ImportPlan, "added" | "updated">): string {
  return `Saved: ${count(p.added, "student", "students")} added, ${p.updated} updated.`;
}

/** What starting a term did. */
export function startedLine(name: string, session: string, enrolled: number): string {
  return enrolled === 0 ? `${name} ${session} is the current term; everyone was enrolled already.` : `${name} ${session} is the current term: ${count(enrolled, "student", "students")} enrolled.`;
}

// --- the import's refusal ---

export interface ImportProblem {
  row: number;
  why: string;
}

export interface ImportRefusal {
  message: string;
  missing: string[];
  problems: ImportProblem[];
  total: number;
}

/** A 422 import_unreadable answer as the page shows it, or null when the error is something else. */
export function importRefusal(status: number, body: Record<string, unknown> | undefined, message: string): ImportRefusal | null {
  if (status !== 422 || !body || body.code !== "import_unreadable") return null;
  const missing = Array.isArray(body.missing) ? body.missing.filter((m): m is string => typeof m === "string") : [];
  const problems = Array.isArray(body.problems)
    ? body.problems
        .filter((p): p is ImportProblem => !!p && typeof p === "object" && typeof (p as ImportProblem).row === "number")
        .map((p) => ({ row: p.row, why: String(p.why ?? "") }))
    : [];
  const total = typeof body.total === "number" ? body.total : problems.length;
  return { message: plainMessage(String(body.error ?? message)), missing, problems, total };
}

// --- forms ---

export interface ClassForm {
  name: string;
  arm: string;
  order: string;
}

export function classProblem(f: ClassForm): string | null {
  if (!f.name.trim()) return "Give the class a name, such as JSS 1 or Primary 4.";
  if (f.name.trim().length > 60) return "A class's name has up to 60 characters.";
  if (f.arm.trim().length > 20) return "An arm has up to 20 characters, such as A or Gold.";
  const n = Number(f.order);
  if (f.order.trim() === "" || !Number.isInteger(n) || n < 0 || n > 1000) return "Give the order as a whole number from 0 to 1000: classes go up in this order.";
  return null;
}

export function classRequest(f: ClassForm): { name: string; arm: string; order: number } {
  return { name: f.name.trim(), arm: f.arm.trim(), order: Number(f.order) };
}

/** The next order after the classes there are, for a new class's form. */
export function nextOrder(list: Pick<SchoolClass, "order">[]): number {
  return list.length ? Math.min(1000, Math.max(...list.map((c) => c.order)) + 1) : 1;
}

export function sessionProblem(label: string): string | null {
  const m = /^(\d{4})\/(\d{4})$/.exec(label.trim());
  if (!m) return "Write the session as two years, such as 2026/2027.";
  if (Number(m[2]) !== Number(m[1]) + 1) return "The second year follows the first, such as 2026/2027.";
  return null;
}

/** The session after the last one: 2027/2028 after 2026/2027, else this school year. */
export function nextSessionLabel(list: Pick<SchoolSession, "label">[], now = new Date()): string {
  const years = list.map((s) => Number(s.label.slice(0, 4))).filter((y) => Number.isFinite(y));
  const start = years.length ? Math.max(...years) + 1 : now.getMonth() >= 7 ? now.getFullYear() : now.getFullYear() - 1;
  return `${start}/${start + 1}`;
}

export interface TermForm {
  name: string;
  starts_on: string;
  ends_on: string;
  fees_due_on: string;
}

const DATE = /^\d{4}-\d{2}-\d{2}$/;

export function termProblem(f: TermForm): string | null {
  if (!f.name.trim()) return "Give the term a name, such as First term.";
  if (!DATE.test(f.starts_on) || !DATE.test(f.ends_on) || !DATE.test(f.fees_due_on)) return "Give the term's start, end and the date fees are due.";
  if (f.starts_on >= f.ends_on) return "The term ends after it starts.";
  if (f.fees_due_on > f.ends_on) return "Fees are due by the end of the term.";
  return null;
}

/** The terms a session has, named in order for the next one's form. */
export function nextTermName(terms: Pick<SchoolTerm, "name">[]): string {
  const names = ["First term", "Second term", "Third term"];
  return names.find((n) => !terms.some((t) => t.name.toLowerCase() === n.toLowerCase())) ?? "";
}

export interface StudentForm {
  name: string;
  admission_number: string;
  class_id: string;
  date_of_birth: string;
  guardian: GuardianInput;
}

export function emptyStudentForm(classID = ""): StudentForm {
  return { name: "", admission_number: "", class_id: classID, date_of_birth: "", guardian: { name: "", phone: "", email: "", relationship: "" } };
}

const ADMISSION = /^[A-Za-z0-9][A-Za-z0-9/_.-]{0,39}$/;

/** A phone the API takes: 0803…, +234… or another country's +… number. */
export function phoneOK(p: string): boolean {
  const d = p.replace(/[\s().-]/g, "");
  return /^0[789]\d{9}$/.test(d) || /^\+234[789]\d{9}$/.test(d) || /^234[789]\d{9}$/.test(d) || (/^\+[1-9]\d{7,14}$/.test(d) && !d.startsWith("+234"));
}

export function guardianProblem(g: GuardianInput, optional = false): string | null {
  const any = g.name.trim() || g.phone.trim() || (g.email ?? "").trim() || (g.relationship ?? "").trim();
  if (optional && !any) return null;
  if (!g.name.trim()) return "Give the guardian's name.";
  if (!g.phone.trim()) return "Give the guardian's phone number.";
  if (!phoneOK(g.phone)) return "Write the phone as 08031234567 or +2348031234567.";
  if (g.email && g.email.trim() && !/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(g.email.trim())) return "That email address does not look right.";
  return null;
}

export function studentProblem(f: StudentForm): string | null {
  if (!f.name.trim()) return "Give the student's name.";
  if (!f.admission_number.trim()) return "Give the student's admission number.";
  if (!ADMISSION.test(f.admission_number.trim())) return "An admission number has letters, digits and / _ . or -, such as GCA/2026/014.";
  if (!f.class_id) return "Choose the student's class.";
  if (f.date_of_birth && !DATE.test(f.date_of_birth)) return "Give the date of birth as a date.";
  return guardianProblem(f.guardian, true);
}

export function studentRequest(f: StudentForm): Record<string, unknown> {
  const out: Record<string, unknown> = { name: f.name.trim(), admission_number: f.admission_number.trim(), class_id: f.class_id };
  if (f.date_of_birth) out.date_of_birth = f.date_of_birth;
  const g = f.guardian;
  if (g.name.trim() || g.phone.trim()) {
    const guardian: Record<string, string> = { name: g.name.trim(), phone: g.phone.trim() };
    if (g.email?.trim()) guardian.email = g.email.trim();
    if (g.relationship?.trim()) guardian.relationship = g.relationship.trim();
    out.guardians = [guardian];
  }
  return out;
}

// --- the students list ---

export interface StudentFilter {
  class: string;
  status: string;
  q: string;
  reveal: boolean;
}

/** The students list's address: GET /v1/schools/students with its filters. */
export function studentsPath(f: StudentFilter): string {
  const q = new URLSearchParams();
  if (f.class) q.set("class", f.class);
  if (f.status) q.set("status", f.status);
  if (f.q.trim()) q.set("q", f.q.trim());
  if (f.reveal) q.set("reveal", "true");
  q.set("limit", "1000");
  return `/v1/schools/students?${q.toString()}`;
}

/** A student's page: by id. */
export function studentPath(id: string, reveal = false): string {
  return `/v1/schools/students/${encodeURIComponent(id)}${reveal ? "?reveal=true" : ""}`;
}

// --- promotion ---

/** What a promotion does with the exceptions chosen: how many move up, repeat, leave and graduate. */
export function promotionCounts(p: Pick<SchoolPromotion, "students">, exceptions: Record<string, PromotionAction>): Record<PromotionAction, number> {
  const out: Record<PromotionAction, number> = { promote: 0, repeat: 0, leave: 0, graduate: 0 };
  for (const s of p.students) {
    let a = exceptions[s.id] ?? s.action;
    // Moving up from the last class is graduating.
    if (a === "promote" && !s.next_id && s.action === "graduate") a = "graduate";
    out[a]++;
  }
  return out;
}

/** The exceptions to send: only students whose choice differs from the plan's. */
export function promotionRequest(p: Pick<SchoolPromotion, "students">, exceptions: Record<string, PromotionAction>): { exceptions: Record<string, PromotionAction> } {
  const out: Record<string, PromotionAction> = {};
  for (const s of p.students) {
    const a = exceptions[s.id];
    if (a && a !== s.action) out[s.id] = a;
  }
  return { exceptions: out };
}

export function promotionLine(c: Record<string, number>): string {
  const parts = [
    c.promote ? `${count(c.promote, "student moves", "students move")} up` : "",
    c.repeat ? `${c.repeat} ${c.repeat === 1 ? "repeats" : "repeat"}` : "",
    c.leave ? `${c.leave} ${c.leave === 1 ? "leaves" : "leave"}` : "",
    c.graduate ? `${c.graduate} ${c.graduate === 1 ? "graduates" : "graduate"}` : "",
  ].filter(Boolean);
  return parts.length ? `${parts.join(", ")}.` : "No active students to promote.";
}

/** The current term of the school, if one runs. */
export function currentTerm(sessions: SchoolSession[] | undefined): SchoolTerm | undefined {
  for (const s of sessions ?? []) {
    const t = s.terms.find((x) => x.state === "current");
    if (t) return t;
  }
  return undefined;
}
