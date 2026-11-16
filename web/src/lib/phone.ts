// The phone way in (docs/spec/taskiem-for-everyone.md §5.1): reading a
// typed number, showing it back, and saying what happened in plain words.

import { ApiError, type Business, type Lock } from "../api";
import { safeNext } from "./safePath";

/** The country a number without one is in: Nigeria. */
export const HOME_COUNTRY = "234";

/** What a number that cannot be one is told, before anything is sent. */
export const NOT_A_PHONE = "That doesn't look like a phone number";

/**
 * A typed number as +<country><number>, the way the server reads it (one
 * rule, the same in the app: mobile/lib/app/phone.dart). Spaces, dashes,
 * dots and brackets are ignored; a leading 00 is +; +234 then 0 drops the
 * 0; a local 0 and 10 digits, or a bare 10 digits starting 7, 8 or 9, is
 * Nigerian. A Nigerian number is +234 and 10 digits starting 7, 8 or 9;
 * another country's is + and 8 to 15 digits in all. Null when it cannot be
 * one (a bare country code with no + included).
 */
export function toE164(typed: string): string | null {
  let s = typed.trim().replace(/[\s\-.()\u00a0]/g, "");
  if (s.startsWith("00")) s = "+" + s.slice(2);
  if (s.startsWith("+")) {
    if (!/^\+\d+$/.test(s)) return null;
    if (s.startsWith("+" + HOME_COUNTRY + "0")) s = "+" + HOME_COUNTRY + s.slice(HOME_COUNTRY.length + 2);
  } else if (/^0\d{10}$/.test(s)) s = "+" + HOME_COUNTRY + s.slice(1);
  else if (/^[789]\d{9}$/.test(s)) s = "+" + HOME_COUNTRY + s;
  else return null;
  if (s.startsWith("+" + HOME_COUNTRY)) return /^\+234[789]\d{9}$/.test(s) ? s : null;
  return /^\+[1-9]\d{7,14}$/.test(s) ? s : null;
}

/** A number to read back: Nigerian numbers grouped as +234 803 123 4567. */
export function showPhone(e164: string): string {
  const m = /^\+234(\d{3})(\d{3})(\d{4})$/.exec(e164);
  return m ? `+234 ${m[1]} ${m[2]} ${m[3]}` : e164;
}

/** Where the code went, in a sentence. */
export function sentWords(via: string, phone: string): string {
  const to = showPhone(phone);
  return via === "sms"
    ? `We sent a 6-digit code by SMS to ${to}.`
    : `We sent a 6-digit code to WhatsApp on ${to}.`;
}

/** Words for a code that has run out (the server's ticket_expired). */
export const CODE_EXPIRED = "That code has expired: ask for a new one.";

/** Too many tries: a number locked, or too many asked for. */
export const TOO_MANY = "Too many tries. Wait an hour and try again.";

/** An account that is turned off, after a right code. */
export const ACCOUNT_OFF = "This account can't sign in. Contact Taskiem.";

/** Whether a 401 says the code was wrong or ran out, not the session. */
export function codeRefused(body: Record<string, unknown>): boolean {
  return !!(body.code_wrong || body.ticket_expired || body.tries_left !== undefined);
}

/** A server's own sentence, made to read as one: a capital first. */
function sentence(s: string): string {
  const t = s.trim();
  if (!t) return "Something went wrong. Try again.";
  const out = t.charAt(0).toUpperCase() + t.slice(1);
  return /[.!?:]$/.test(out) ? out : out + ".";
}

/** What went wrong with a code, in plain words. */
export function codeProblem(e: unknown): string {
  if (e instanceof ApiError) {
    const b = e.body;
    if (b.locked || e.status === 429) return TOO_MANY;
    if (b.sms_unavailable) return "We can't send codes by SMS here. Ask for the code on WhatsApp instead.";
    if (b.phone_signin_off)
      return "Signing in with a code to your phone is turned off for this account. Sign in with your email instead.";
    if (b.phone_invalid) return NOT_A_PHONE + ".";
    if (b.ticket_expired) return CODE_EXPIRED;
    if (b.code_wrong) return "That code is not right. Check it, or ask for a new one.";
    if (typeof b.tries_left === "number") return pinWrong(b.tries_left);
    if (b.account_off || (e.status === 401 && /not active|disabled/i.test(e.message))) return ACCOUNT_OFF;
    if (e.status === 503) return "We can't send codes to phones just now. Sign in with your email instead.";
    if (e.status === 400) return NOT_A_PHONE + ".";
    if (e.message) return sentence(e.message);
  }
  return e instanceof Error ? sentence(e.message) : String(e);
}

/** A wrong chat PIN, and how many tries are left. */
export function pinWrong(left: number): string {
  if (left <= 0) return TOO_MANY;
  return `That PIN is not right. ${left} ${left === 1 ? "try" : "tries"} left.`;
}

/** Runs fn, turning what went wrong into plain words about the code. */
export async function plainly<T>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn();
  } catch (e) {
    throw new Error(codeProblem(e), { cause: e });
  }
}

/** Why a business does not open yet from here, in plain words. */
export function lockWords(lock: Lock | null | undefined): string {
  switch (lock) {
    case "sso_required":
      return "Opens with your organisation's sign-in";
    case "passkey_required":
      return "Opens with your passkey";
    case "second_proof_required":
      return "New device: prove it's you to open it";
    default:
      return "";
  }
}

/** The line under a business in the switcher. */
export function businessLine(b: Business): string {
  if (b.current) return b.kind === "workflows" ? "Workflows · open now" : "Open now";
  if (b.locked) return lockWords(b.lock);
  return b.kind === "workflows" ? "Workflows" : "Business";
}

/** Seconds as "0:45", for when a new code can be asked for. */
export function countdown(s: number): string {
  const n = Math.max(0, Math.ceil(s));
  return `${Math.floor(n / 60)}:${String(n % 60).padStart(2, "0")}`;
}

/** A code typed or pasted: its digits only, at most six ("123 456" too). */
export const codeDigits = (typed: string, most = 6) => typed.replace(/\D/g, "").slice(0, most);

/** Whether a path is on the person's own side (You), which a phone
 * sign-in opens on. */
export function youPath(path: string): boolean {
  const p = path.split(/[?#]/, 1)[0] ?? "";
  return p === "/you" || p.startsWith("/you/") || p === "/second-proof";
}

/** Where /login or /signup goes for someone already signed in: ?start=
 * opens Start something with it chosen; a You page in ?next= (a personal
 * session) opens; else [fallback]. */
export function signedInTo(search: string, fallback: string, personal = true): string {
  const q = new URLSearchParams(search);
  const start = q.get("start");
  if (start === "business" || start === "workflows") return `${personal ? "/you/start" : "/start-something"}?what=${start}`;
  const next = safeNext(q.get("next"), "");
  return personal && next && youPath(next) ? next : fallback;
}

/** Where a phone sign-in goes, given ?next= and ?start=: a You page as
 * it is; Start something with A business chosen; else You. A business's
 * page is null: the business must be chosen first. */
export function afterPhone(next: string, start: string | null): string | null {
  if (start === "business" || start === "workflows") return `/you/start?what=${start}`;
  if (!next || youPath(next)) return next || "/you";
  return null;
}
