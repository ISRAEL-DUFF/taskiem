// What an approval asks, in words for its card and its confirm sheet
// (pages/Approvals.tsx, Home's Needs you): an approval's subject is
// whatever its workflow put there, so the card leads with an amount and
// who it goes to only when the subject names them, and lists the rest.
import { formatPrice } from "./storefront";

export interface SubjectWords {
  /** "₦45,000" (a field in kobo), or the number as written. */
  amount?: string;
  /** Who it goes to: "Kunle Supplies". */
  to?: string;
  /** The account: "GTBank ••4821". */
  account?: string;
  /** Every other plain field, as [label, value]. */
  rest: [string, string][];
}

const AMOUNT = ["amount_kobo", "total_kobo", "amount", "total"];
const TO = ["to", "payee", "recipient", "beneficiary", "account_name", "supplier", "customer", "name"];
const BANK = ["bank", "bank_name"];
const ACCOUNT = ["account_number", "account", "nuban"];

const plain = (v: unknown): v is string | number | boolean => typeof v === "string" || typeof v === "number" || typeof v === "boolean";

/** "amount_kobo" → "Amount", "due_date" → "Due date". */
export function fieldLabel(key: string): string {
  const words = key.replace(/_kobo$/, "").replace(/[_-]+/g, " ").trim();
  return words ? words.charAt(0).toUpperCase() + words.slice(1) : key;
}

/** A field's value in words: kobo fields as naira, numbers with separators. */
export function fieldValue(key: string, v: string | number | boolean): string {
  if (typeof v === "number" && /_kobo$/.test(key)) return formatPrice(v);
  if (typeof v === "number") return v.toLocaleString("en-NG");
  if (typeof v === "boolean") return v ? "Yes" : "No";
  return v;
}

/** The subject's amount, payee, account and other fields; nothing for a subject that is not an object of fields. */
export function subjectWords(subject: unknown): SubjectWords {
  const out: SubjectWords = { rest: [] };
  if (!subject || typeof subject !== "object" || Array.isArray(subject)) return out;
  const s = subject as Record<string, unknown>;
  const used = new Set<string>();
  const first = (keys: string[], ok: (v: unknown) => boolean) => keys.find((k) => k in s && ok(s[k]));
  const amountKey = first(AMOUNT, (v) => typeof v === "number") ?? Object.keys(s).find((k) => /_kobo$/.test(k) && typeof s[k] === "number");
  if (amountKey) {
    out.amount = fieldValue(amountKey, s[amountKey] as number);
    used.add(amountKey);
  }
  const toKey = first(TO, (v) => typeof v === "string" && v.trim() !== "");
  if (toKey) {
    out.to = s[toKey] as string;
    used.add(toKey);
  }
  const bankKey = first(BANK, (v) => typeof v === "string" && v.trim() !== "");
  const accKey = first(ACCOUNT, (v) => (typeof v === "string" || typeof v === "number") && String(v).replace(/\D/g, "").length >= 4);
  if (accKey) {
    const digits = String(s[accKey]).replace(/\D/g, "");
    out.account = `${bankKey ? `${String(s[bankKey])} ` : ""}••${digits.slice(-4)}`;
    used.add(accKey);
    if (bankKey) used.add(bankKey);
  }
  for (const [k, v] of Object.entries(s)) {
    if (used.has(k) || !plain(v)) continue;
    out.rest.push([fieldLabel(k), fieldValue(k, v)]);
  }
  return out;
}
