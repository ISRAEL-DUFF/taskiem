// Students' account numbers (docs/spec/schools.md 6, B4;
// docs/schools.md#account-numbers): the school's issuing connection, a
// batch's plan and progress, and a student's own account. The words and
// checks the pages use, kept here so they can be tested.

/** The connection that issues students' account numbers, and the family-credit setting. */
export interface SchoolIssuing {
  connection: string;
  account?: string;
  siblings_share_credit?: boolean;
}

/** A prod connection that can issue students' account numbers, and what its rail needs. */
export interface SchoolIssuer {
  connection: string;
  rail: string;
  requirement: string;
  fee?: string;
  names_itself: boolean;
  name_limit?: number;
  one_account_per_email?: boolean;
  needs_bvn?: boolean;
  needs_account: boolean;
  look_back: boolean;
  webhook: boolean;
}

export type AccountStatus = "issuing" | "issued" | "failed" | "unknown";

export interface StudentAccount {
  student_id: string;
  connection: string;
  status: AccountStatus;
  account_number?: string;
  bank_name?: string;
  account_name?: string;
  named_by_rail: boolean;
  reference?: string;
  shortened?: boolean;
  flagged?: boolean;
  error?: string;
  attempts: number;
  updated_at: string;
}

export interface AccountPlan {
  issuer: SchoolIssuer;
  active: number;
  already_issued: number;
  to_issue: number;
  later: number;
  waiting: {
    student_id: string;
    admission_number: string;
    status: string;
    reason?: string;
  }[];
  cannot: { student_id: string; admission_number: string; reason: string }[];
  running?: string;
  hash: string;
}

export interface AccountJob {
  id: string;
  connection: string;
  status: "running" | "done";
  total: number;
  issued: number;
  failed: number;
  unknown: number;
  pending: number;
  created_by: string;
  created_at: string;
  finished_at?: string;
  problems: {
    student_id: string;
    admission_number: string;
    status: string;
    reason: string;
  }[];
}

const STATUS: Record<AccountStatus, { label: string; tone: string }> = {
  issued: { label: "Issued", tone: "ok" },
  issuing: { label: "Being issued", tone: "warn" },
  failed: { label: "Not issued", tone: "danger" },
  unknown: { label: "Check with the rail", tone: "warn" },
};

/** How a student's account stands, in words, with a badge tone. */
export function accountStatus(s: string): { label: string; tone: string } {
  return STATUS[s as AccountStatus] ?? { label: s, tone: "" };
}

/** Why the issuing setting cannot be saved, or null. */
export function issuingProblem(v: SchoolIssuing, issuers: SchoolIssuer[]): string | null {
  if (!v.connection) return null;
  const i = issuers.find((x) => x.connection === v.connection);
  if (!i) return "Choose one of the connections listed.";
  if (i.needs_account && !(v.account ?? "").trim()) return `${i.rail} needs the wallet the numbers pay into.`;
  return null;
}

/** What the setting sends: the wallet only where the rail takes one. */
export function issuingRequest(v: SchoolIssuing, issuers: SchoolIssuer[]): SchoolIssuing {
  const i = issuers.find((x) => x.connection === v.connection);
  const out: SchoolIssuing = {
    connection: v.connection,
    siblings_share_credit: !!v.siblings_share_credit,
  };
  if (i?.needs_account && v.account?.trim()) out.account = v.account.trim();
  return out;
}

function count(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString("en-NG")} ${n === 1 ? one : many}`;
}

/** The plan in one sentence. */
export function planSummary(p: Pick<AccountPlan, "to_issue" | "already_issued" | "active" | "later" | "cannot" | "issuer">): string {
  const parts: string[] = [];
  if (p.to_issue === 0) parts.push(`Every student who can have an account number on ${p.issuer.rail} has one or is waiting.`);
  else parts.push(`This issues ${count(p.to_issue, "account number")} on ${p.issuer.rail} (${p.issuer.connection}).`);
  if (p.already_issued > 0) parts.push(`${count(p.already_issued, "student")} of ${p.active.toLocaleString("en-NG")} already ${p.already_issued === 1 ? "has one" : "have one"}.`);
  if (p.later > 0) parts.push(`${count(p.later, "more student")} can be issued by the next batch (at most 500 at a time).`);
  if (p.cannot.length > 0) parts.push(`${count(p.cannot.length, "student")} cannot be issued yet: see why below.`);
  return parts.join(" ");
}

/** A batch's progress in one line. */
export function jobLine(j: Pick<AccountJob, "status" | "total" | "issued" | "failed" | "unknown" | "pending">): string {
  const done = j.issued + j.failed + j.unknown;
  if (j.status === "running") return `Issuing: ${done.toLocaleString("en-NG")} of ${j.total.toLocaleString("en-NG")} done, ${count(j.issued, "account")} issued so far.`;
  const parts = [`Done: ${count(j.issued, "account")} issued of ${j.total.toLocaleString("en-NG")}.`];
  if (j.failed > 0) parts.push(`${count(j.failed, "student")} not issued.`);
  if (j.unknown > 0) parts.push(`${count(j.unknown, "student")} to check with the rail.`);
  return parts.join(" ");
}

/** The share of a batch done, 0 to 100. */
export function jobPercent(j: Pick<AccountJob, "total" | "issued" | "failed" | "unknown">): number {
  if (j.total <= 0) return 100;
  return Math.min(100, Math.round(((j.issued + j.failed + j.unknown) / j.total) * 100));
}

/** Why a BVN or NIN typed in cannot be sent, or null. */
export function idProblem(bvn: string, nin: string): string | null {
  const b = bvn.trim();
  const n = nin.trim();
  if (!b && !n) return "Give a parent's BVN or NIN.";
  for (const v of [b, n]) if (v && !/^[0-9]{11}$/.test(v)) return "A BVN or NIN has 11 digits.";
  return null;
}

/** What parents should expect to see when they check the number. */
export function nameNote(a: Pick<StudentAccount, "named_by_rail" | "shortened" | "flagged">, rail: string): string {
  if (a.flagged) return "The child's name alone was too long for the rail and was cut: check what parents see.";
  if (a.named_by_rail) return `${rail} names the account itself: parents may see its name rather than the school's and the child's.`;
  if (a.shortened) return "The school's name was shortened to fit the rail's limit; the child's name is whole.";
  return "";
}

/** The rail's name from a connection (iswallet/main → iswallet). */
export function railOf(connection: string): string {
  const id = connection.split("/")[0] ?? connection;
  return (
    {
      iswallet: "iswallet",
      paystack: "Paystack",
      flutterwave: "Flutterwave",
      moniepoint: "Moniepoint",
    }[id] ?? id
  );
}

/** Groups account numbers as banks print them: 0123 456 789. */
export function spacedNumber(n: string): string {
  return n.length === 10 ? `${n.slice(0, 4)} ${n.slice(4, 7)} ${n.slice(7)}` : n;
}

/** The fee sentence, or a plain one when none is documented. */
export function feeLine(i: Pick<SchoolIssuer, "fee" | "rail">): string {
  return i.fee || `${i.rail} documents no fee per account.`;
}
