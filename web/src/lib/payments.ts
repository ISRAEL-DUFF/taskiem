// Paying one person (P1; docs/spec/chat-pay-people.md): the words and
// sums the Payments pages and the Pay sheet show. A payment is prepared
// (POST /v1/payments), shown with the bank's name for the account large,
// then confirmed with a passkey (POST /v1/payments/{id}/confirm).

import { naira } from "./invoices";

export type PaymentStatus =
  | "prepared"
  | "waiting_ok"
  | "sending"
  | "paid"
  | "failed"
  | "rejected"
  | "unknown"
  | "cancelled"
  | "reversed";

export type PaymentPurpose =
  | "bill"
  | "wages"
  | "delivery"
  | "refund"
  | "stock"
  | "other";

/** A payment as the API shows it: the account masked ("••6789"). */
export interface Payment {
  id: string;
  saved_id: string | null;
  personal: boolean;
  payee: string;
  bank_name: string;
  account: string;
  account_name: string | null;
  amount_kobo: number;
  purpose: PaymentPurpose;
  purpose_note: string;
  note: string;
  first_payment: boolean;
  changed_account: boolean;
  /** "limit", "changed" or both ("limit,changed"): why it waits for the owner. */
  needs_ok: string;
  status: PaymentStatus;
  reason: string | null;
  fee_kobo: number | null;
  started_from: string;
  prepared_by: string;
  confirmed_by: string | null;
  created_at: string;
  confirmed_at: string | null;
  finished_at: string | null;
  /** The staff member paid, when it was one (to the account linked to them). */
  staff_id?: string | null;
  /** The same amount to the same payee in the last day. */
  again: boolean;
  /** Confirmed by you, and its run still waits for your own OK, which did
   * not reach it: confirming it again gives it. */
  awaits_your_ok?: boolean;
  /** The fee quoted before paying; null when the fee shows after. */
  fee_quote_kobo?: number | null;
  /** "wallet": Taskiem wallet to Taskiem wallet, at once, no fee. */
  route?: "bank" | "wallet";
  /** Tell the person paid in their chat; a receipt to send them. */
  tell_payee?: boolean;
  send_receipt?: boolean;
  /** They can be told in a chat of theirs (a rider, a staff member). */
  may_tell?: boolean;
  /** The balance when it was prepared, where the account says it. */
  balance_kobo?: number | null;
  rider_id?: string | null;
}

/** What people are paid from: GET /v1/payments/balance. */
export interface PayBalance {
  from: string;
  available_kobo: number | null;
  wallet: boolean;
  quotes_fee: boolean;
  add_money: {
    bank_name: string;
    account_number: string;
    account_name: string;
  } | null;
}

/** A paid payment's receipt: GET /v1/payments/{id}/receipt. */
export interface PayReceipt {
  business: string;
  payee: string;
  bank_name: string;
  account: string;
  amount_kobo: number;
  for: string;
  paid_at: string;
  reference: string;
  route: string;
}

/** The fee before paying, in words (Settled 4 and 7): "₦45,000 + ₦25
 * fee", or that the fee shows after paying, or the wallet's route. */
export function feeWords(
  x: Pick<Payment, "amount_kobo" | "fee_quote_kobo" | "route">,
): string {
  if (x.route === "wallet") return "Taskiem wallet · arrives at once, no fee";
  if (x.fee_quote_kobo == null) return "The fee shows after paying";
  return `${naira(x.amount_kobo)} + ${naira(x.fee_quote_kobo)} fee`;
}

/** The balance once it and its fee are paid; null when not known. */
export function balanceAfter(
  x: Pick<Payment, "amount_kobo" | "fee_quote_kobo" | "balance_kobo">,
): number | null {
  if (x.balance_kobo == null) return null;
  return x.balance_kobo - x.amount_kobo - (x.fee_quote_kobo ?? 0);
}

/** A payment's diary line's state: "Received by the bank", "Failed:
 * refunded to your balance" (with Try again), or on its way. */
export function diaryState(x: Pick<Payment, "status">): {
  words: string;
  tone: "ok" | "danger" | "info" | "muted";
  again: boolean;
} {
  switch (x.status) {
    case "paid":
      return { words: "Received by the bank", tone: "ok", again: false };
    case "failed":
      return { words: "Failed: refunded to your balance", tone: "danger", again: true };
    case "reversed":
      return { words: "Came back: refunded to your balance", tone: "danger", again: true };
    case "waiting_ok":
      return { words: "Waiting for the owner's OK", tone: "info", again: false };
    case "rejected":
      return { words: "Refused: nothing was paid", tone: "muted", again: false };
    case "cancelled":
      return { words: "Cancelled: nothing was paid", tone: "muted", again: false };
    default:
      return { words: "Sent, waiting for the bank", tone: "info", again: false };
  }
}

/** A beneficiary's name from the bank's name for the account: its first
 * word, lowered ("CHIDI OKEKE" is chidi). */
export function beneficiaryName(accountName: string | null | undefined): string {
  const w = (accountName ?? "").trim().split(/\s+/)[0] ?? "";
  return w.toLowerCase().replace(/[^\p{L}\p{N}_-]/gu, "").slice(0, 32);
}

/** A role's limit: one payment, and a day's total. */
export interface PayLimit {
  role: string;
  one_kobo: number;
  day_kobo: number;
}

type Tone = "ok" | "warn" | "danger" | "info" | "muted";

const STATUS: Record<PaymentStatus, { label: string; tone: Tone }> = {
  prepared: { label: "To confirm", tone: "warn" },
  waiting_ok: { label: "Waiting for the owner's OK", tone: "warn" },
  sending: { label: "Sending", tone: "info" },
  unknown: { label: "Sent, waiting for the bank", tone: "info" },
  paid: { label: "Paid", tone: "ok" },
  failed: { label: "Not paid", tone: "danger" },
  reversed: { label: "Came back", tone: "danger" },
  rejected: { label: "Refused", tone: "danger" },
  cancelled: { label: "Cancelled", tone: "muted" },
};

/** A payment's status in words, with its tone. */
export const paymentStatus = (s: string): { label: string; tone: Tone } =>
  STATUS[s as PaymentStatus] ?? { label: s, tone: "muted" };

/** The statuses the list can be narrowed to, in the order people look. */
export const STATUS_FILTERS: { value: string; label: string }[] = [
  { value: "", label: "All" },
  { value: "prepared", label: "To confirm" },
  { value: "waiting_ok", label: "Waiting for an OK" },
  { value: "paid", label: "Paid" },
  { value: "failed", label: "Not paid" },
];

const PURPOSE: Record<PaymentPurpose, string> = {
  bill: "A bill",
  wages: "Wages",
  delivery: "A delivery",
  refund: "A refund",
  stock: "Stock bought",
  other: "Other",
};

/** What a payment is for, in words: "A delivery", "Other: generator fuel". */
export function purposeWords(
  p: Pick<Payment, "purpose" | "purpose_note">,
): string {
  const w = PURPOSE[p.purpose] ?? "Other";
  return p.purpose_note ? `${w}: ${p.purpose_note}` : w;
}

/** The "for" field's choice as the API's purpose: "a delivery" is
 * delivery, "stock bought" stock; undefined when it is none of them. */
export function purposeOf(typed: string): PaymentPurpose | undefined {
  const t = typed
    .trim()
    .toLowerCase()
    .replace(/^(a|an)\s+/, "");
  if (!t) return undefined;
  for (const [k, w] of Object.entries(PURPOSE) as [PaymentPurpose, string][]) {
    if (t === k || t === w.toLowerCase().replace(/^(a|an)\s+/, "")) return k;
  }
  if (t === "stock") return "stock";
  return undefined;
}

/** Naira typed as kobo, the way people say it: "45k", "1.5m", "45,000",
 * "₦45,000.50". NaN when it is not an amount; undefined when empty. */
export function payAmountKobo(text: string): number | undefined {
  const t = text
    .trim()
    .toLowerCase()
    .replace(/[₦,\s]/g, "")
    .replace(/^ngn/, "")
    .replace(/naira$/, "");
  if (t === "") return undefined;
  const m = /^(\d+(?:\.\d+)?)(k|m)?$/.exec(t);
  if (!m) return NaN;
  const n =
    Number(m[1]) * (m[2] === "k" ? 1_000 : m[2] === "m" ? 1_000_000 : 1);
  const kobo = Math.round(n * 100);
  // Plain naira takes two places at most: "45.123" is not an amount.
  if (!m[2] && /\.\d{3,}$/.test(m[1] ?? "")) return NaN;
  return kobo;
}

/** An account number typed with spaces or dashes, as its digits. */
export const accountDigits = (typed: string) => typed.replace(/[\s-]/g, "");

/** What is wrong with what a field holds, or null. */
export function payFieldProblem(kind: string, value: string): string | null {
  const v = value.trim();
  if (!v) return null;
  switch (kind) {
    case "money": {
      const k = payAmountKobo(v);
      if (k === undefined || Number.isNaN(k))
        return "Type an amount, like 45,000 or 45k.";
      if (k < 100) return "Pay at least ₦1.";
      return null;
    }
    case "account":
      return /^\d{10}$/.test(accountDigits(v))
        ? null
        : "An account number is 10 digits.";
    case "payee":
      return /^@?[\p{L}\p{N}][\p{L}\p{N}_ -]{0,31}$/u.test(v)
        ? null
        : "Choose one of your saved accounts, like @bayo.";
    default:
      return null;
  }
}

/** A pay form's values as the body of POST /v1/payments: a rider (by
 * id), an account number and its bank (pay.account), or a saved account
 * by its @name. */
export function payBody(
  values: Record<string, string>,
  from: string,
  where?: string,
): Record<string, unknown> {
  const body: Record<string, unknown> = { from };
  // The chat its diary line goes to: the Taskiem chat, or a business's.
  if (where) body.where = where;
  const number = accountDigits(values.number ?? "");
  // A staff member: paid to the saved account linked to them.
  if (values.staff) body.staff_id = values.staff;
  // A rider: paid to the bank account kept for their pay.
  else if (values.rider) body.rider_id = values.rider;
  else if (number) {
    body.account_number = number;
    body.bank_name = (values.bank ?? "").trim();
  } else {
    body.payee = (values.who ?? "").trim();
  }
  const kobo = payAmountKobo(values.amount ?? "");
  if (kobo !== undefined && !Number.isNaN(kobo)) body.amount_kobo = kobo;
  const typedFor = (values.for ?? "").trim();
  const purpose = purposeOf(typedFor);
  body.purpose = purpose ?? "other";
  // A word for "other", or what was typed when it is none of the choices.
  if (!purpose && typedFor) body.purpose_note = typedFor.slice(0, 80);
  else if (values.for_note?.trim())
    body.purpose_note = values.for_note.trim().slice(0, 80);
  if (values.note?.trim()) body.note = values.note.trim().slice(0, 100);
  return body;
}

export interface PayWarning {
  key: "changed" | "first" | "again" | "unchecked";
  tone: "danger" | "warn";
  words: string;
}

/** What the person confirming must see before they do, worst first. */
export function payWarnings(x: Payment): PayWarning[] {
  const out: PayWarning[] = [];
  if (x.changed_account)
    out.push({
      key: "changed",
      tone: "danger",
      words: `Not the account you paid ${x.payee} before (${x.bank_name} ${x.account}). Call them on a number you know before paying. The owner's OK is asked for a changed account.`,
    });
  if (x.again)
    out.push({
      key: "again",
      tone: "warn",
      words: `You paid ${x.payee} ${naira(x.amount_kobo)} in the last day. Pay again only if you mean to.`,
    });
  if (x.first_payment)
    out.push({
      key: "first",
      tone: "warn",
      words: "First payment to this account.",
    });
  if (!x.account_name)
    out.push({
      key: "unchecked",
      tone: "warn",
      words:
        "The bank's name for this account is checked before the money goes.",
    });
  return out;
}

/** Why a payment waits for the owner: "over the limit", "a changed account". */
export function needsOkWords(needs: string): string {
  const why = needs
    .split(",")
    .map(
      (n) =>
        ({
          limit: "it is over the payer's limit",
          changed: "the account changed since it was last paid",
          first: "the business never paid this account before",
        })[n.trim()],
    )
    .filter(Boolean);
  return why.length
    ? `It waits for the owner's OK in Approvals: ${why.join(", and ")}.`
    : "It waits for the owner's OK in Approvals.";
}

/** What a payment's page says under its status, by how it ended. */
export function outcomeWords(x: Payment): string {
  switch (x.status) {
    case "prepared":
      return "Check the name the bank gave, then confirm with your passkey.";
    case "waiting_ok":
      return needsOkWords(x.needs_ok);
    case "sending":
      return "Sent to the bank. This page shows when it is received.";
    case "unknown":
      return "Sent. The bank has not said yet whether it arrived; Taskiem keeps asking.";
    case "paid":
      return x.fee_kobo
        ? `Received by the bank. The fee was ${naira(x.fee_kobo)}.`
        : "Received by the bank.";
    case "failed":
      return x.reason
        ? `Not paid: ${x.reason}. Any money taken is returned to your balance.`
        : "Not paid. Any money taken is returned to your balance.";
    case "rejected":
      return x.reason
        ? `The owner refused it: ${x.reason}.`
        : "The owner refused it. Nothing was paid.";
    case "cancelled":
      return "Cancelled before it was sent. Nothing was paid.";
    case "reversed":
      return x.reason
        ? `The bank sent it back: ${x.reason}. It is in your balance again.`
        : "The bank sent it back. It is in your balance again.";
    default:
      return "";
  }
}

/** The line under a payment's row: "GTBank ••6789 · A delivery". */
export const paymentSub = (x: Payment) =>
  `${x.bank_name} ${x.account} · ${purposeWords(x)}`;

/** Who a payment is to, as the bank names them when it has. */
export const payeeName = (x: Payment) => x.account_name || x.payee;

/** A limit typed in naira for PUT /v1/pay-limits: kobo, or a problem. */
export function limitProblem(one: string, day: string): string | null {
  const o = payAmountKobo(one);
  const d = payAmountKobo(day);
  if (o === undefined || d === undefined)
    return "Type both amounts; ₦0 means every payment needs the owner's OK.";
  if (Number.isNaN(o) || Number.isNaN(d))
    return "Type amounts in naira, like 50,000 or 50k.";
  if (d > 0 && o > d) return "One payment can't be more than a day's total.";
  return null;
}

/** A role's limit in words: "Up to ₦50,000 a payment, ₦200,000 a day",
 * or that everything needs the owner's OK. */
export function limitWords(
  l: Pick<PayLimit, "one_kobo" | "day_kobo"> | undefined,
): string {
  if (!l || (l.one_kobo === 0 && l.day_kobo === 0))
    return "Every payment waits for the owner's OK";
  return `Up to ${naira(l.one_kobo)} a payment, ${naira(l.day_kobo)} a day`;
}

/** The kinds of field a pay form has, and the fields of its two ways in,
 * as the chat's catalogue gives them (api/chat_actions.go). */
export interface PayField {
  key: string;
  label: string;
  hint?: string;
  kind: string;
  choices?: string[];
  optional?: boolean;
}

const FOR: PayField = {
  key: "for",
  label: "What is it for?",
  kind: "choice",
  choices: [
    "a bill",
    "wages",
    "a delivery",
    "a refund",
    "stock bought",
    "other",
  ],
};

/** The fields of Pay someone (a saved account) and Pay an account number,
 * for the Payments page, which opens the sheet without the chat. */
export const PAY_FIELDS: Record<"person" | "account", PayField[]> = {
  person: [
    {
      key: "who",
      label: "Who are you paying?",
      hint: "One of your saved accounts, like @chidi.",
      kind: "payee",
    },
    { key: "amount", label: "How much?", kind: "money" },
    FOR,
    {
      key: "note",
      label: "A few words for their bank statement",
      kind: "text",
      optional: true,
    },
  ],
  account: [
    {
      key: "number",
      label: "Account number",
      hint: "10 digits.",
      kind: "account",
    },
    { key: "bank", label: "Which bank is it with?", kind: "bank" },
    { key: "amount", label: "How much?", kind: "money" },
    FOR,
  ],
};

/** The Pay sheet's button: "Check and pay ₦45,000", once the amount is there. */
export function payButtonWords(values: Record<string, string>): string {
  const k = payAmountKobo(values.amount ?? "");
  return k && !Number.isNaN(k) ? `Check and pay ${naira(k)}` : "Check and pay";
}
