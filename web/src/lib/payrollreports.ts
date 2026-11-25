// Payroll settings and the tax and pension schedules (P4b-5,
// docs/spec/chat-pay-people.md): the setup read-only, each payroll's
// schedules, one line per person, pension PINs and PFAs masked until a
// passkey shows them. The words these screens say. Nothing of a schedule
// is kept in the browser: it is read when the page opens.

/** One line of the setup in words. */
export interface SettingRow {
  key: string;
  label: string;
  value: string;
  warn?: boolean;
  on?: boolean;
}

export interface PayrollSetup {
  company: { name?: string; rc_number?: string };
  rows: SettingRow[];
  deductions: SettingRow[];
  not_set_up?: string;
  since?: string;
  change_url?: string;
  separation_of_duties: boolean;
}

export interface PayrollSetupView {
  settings: PayrollSetup;
  is_owner: boolean;
}

export type ScheduleKind = "paye" | "pension" | "nhf" | "nhis";

export interface PayrollSchedule {
  id: string;
  run_id: string;
  kind: ScheduleKind;
  title: string;
  to: string;
  period: string;
  period_words: string;
  total_kobo: number;
  people: number;
  state: "to_pay" | "paid" | "preparing";
  state_words: string;
  when?: string;
  reference?: string;
}

export interface ScheduleLine {
  employee_id: string;
  name: string;
  amount_kobo: number;
  pfa?: string;
  pin?: string;
}

export interface PayrollScheduleDetail {
  schedule: PayrollSchedule;
  lines: ScheduleLine[];
  masked: boolean;
}

/** Schedules by payroll month, newest first, in the order given. */
export function byMonth(list: PayrollSchedule[]): { period: string; words: string; schedules: PayrollSchedule[] }[] {
  const out: { period: string; words: string; schedules: PayrollSchedule[] }[] = [];
  for (const s of list) {
    const last = out[out.length - 1];
    if (last && last.period === s.period) last.schedules.push(s);
    else out.push({ period: s.period, words: s.period_words, schedules: [s] });
  }
  return out;
}

/** The icon and its tone for a schedule's row. */
export function scheduleTone(s: PayrollSchedule): "warn" | "ok" | "muted" {
  if (s.state === "paid") return "ok";
  if (s.state === "to_pay") return "warn";
  return "muted";
}

export function scheduleIcon(kind: ScheduleKind): string {
  switch (kind) {
    case "paye":
      return "reports";
    case "pension":
      return "members";
    default:
      return "money";
  }
}

/** "3 people" */
export function peopleWords(n: number): string {
  return n === 1 ? "1 person" : `${n} people`;
}

/** A schedule's row: "Due 10 Dec · 3 people". */
export function scheduleSub(s: PayrollSchedule): string {
  return [s.when || s.state_words, peopleWords(s.people)].join(" · ");
}

/** What a refusal to read the schedules means for this person. */
export function scopeWords(code: string | undefined, owner: boolean): { reconnect: boolean } | null {
  if (code !== "scope_required") return null;
  return { reconnect: owner };
}

/** Saves a spreadsheet the server made, without keeping it anywhere. */
export function saveCSV(filename: string, csv: string) {
  const blob = new Blob([csv], { type: "text/csv;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 0);
}
