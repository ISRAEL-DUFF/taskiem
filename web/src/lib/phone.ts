// The phone way in (docs/spec/taskiem-for-everyone.md §5.1): reading a
// typed number, showing it back, and saying what happened in plain words.

import { ApiError, type Business, type Lock } from "../api";

/** The country a number without one is in: Nigeria. */
export const HOME_COUNTRY = "234";

const E164 = /^\+[1-9]\d{7,14}$/;

/**
 * A typed number as +<country><number>, the way the server reads it: a
 * local number (0803 123 4567) is Nigerian, 00 is the same as +, and
 * spaces, dashes, dots and brackets are ignored. Null when it cannot be one.
 */
export function toE164(typed: string, country = HOME_COUNTRY): string | null {
  let s = typed.trim().replace(/[\s\-.()\u00a0]/g, "");
  if (s.startsWith("+")) {
    // as it is
  } else if (s.startsWith("00")) s = "+" + s.slice(2);
  else if (s.startsWith("0")) s = "+" + country + s.slice(1);
  else s = "+" + s;
  return E164.test(s) ? s : null;
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

/** What went wrong with a code, in plain words. */
export function codeProblem(e: unknown): string {
  if (e instanceof ApiError) {
    if (e.body.locked) return "Too many wrong codes. This number is locked for an hour: try again later.";
    if (e.body.sms_unavailable) return "We can't send codes by SMS here. Ask for the code on WhatsApp instead.";
    if (e.body.phone_signin_off)
      return "Signing in with a code to your phone is turned off for this account. Sign in with your email instead.";
    if (e.status === 401) return "That code is not right, or it has expired. Check it, or ask for a new one.";
    if (e.status === 429) return e.message || "Too many tries. Wait a little, then try again.";
    if (e.status === 503) return "We can't send codes to phones just now. Sign in with your email instead.";
    if (e.status === 400) return "Enter your phone number, with its country code if it is not Nigerian.";
    if (e.message) return e.message.charAt(0).toUpperCase() + e.message.slice(1);
  }
  return e instanceof Error ? e.message : String(e);
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
