// You's own settings (docs/spec/taskiem-for-everyone.md §7, §14): the
// person's photo, which pushes they get, the privacy page and deleting the
// account. What the API answers (/v1/me/photo, /v1/me/notifications,
// /v1/me/privacy, /v1/me/delete…) and the words the screens build from it.
// Pure helpers, tested on their own.

import { ApiError } from "../api";
import { codeProblem } from "./phone";

/** GET/PUT /v1/me/notifications. */
export interface YouNotifications {
  messages: boolean;
  updates: boolean;
  offers: boolean;
}

/** The three kinds of push, in the order and words the page shows. */
export const NOTIFY_KINDS: { key: keyof YouNotifications; label: string; hint: string }[] = [
  { key: "messages", label: "Messages", hint: "Someone at a business writes to you." },
  { key: "updates", label: "Order and booking news", hint: "A business's own updates: your order is on its way, your booking is confirmed." },
  { key: "offers", label: "Offers", hint: "Offers from businesses you said yes to." },
];

/** What turning a kind off means, under the switches. */
export const NOTIFY_OFF_WORDS = "A kind you turn off is not sent to your phone, by notification or by SMS. The message is still in the chat.";

/** One business that has the person's number (GET /v1/me/privacy). */
export interface PrivacyBusiness {
  business_id: string;
  business: string;
  chat_id: string | null;
  orders: number;
  last_order_at: string | null;
}

/** GET /v1/me/privacy. */
export interface YouPrivacy {
  shown_to_businesses: { name: string; phone: string | null; photo: boolean };
  businesses: PrivacyBusiness[];
  erase: { words: string; how: "ask_in_chat" };
}

/** A business's line on the privacy page: "You chat with them · 3 orders,
 * the latest 12 Oct 2026". */
export function privacyLine(b: Pick<PrivacyBusiness, "chat_id" | "orders" | "last_order_at">, date: (iso: string) => string): string {
  const parts: string[] = [];
  if (b.chat_id) parts.push("You chat with them");
  if (b.orders > 0) {
    const n = b.orders === 1 ? "1 order" : `${b.orders} orders`;
    parts.push(b.last_order_at ? `${n}, the latest ${date(b.last_order_at)}` : n);
  }
  return parts.join(" · ") || "They have your number";
}

/** How many businesses have the person's number, as the card's line. */
export function holdersWords(n: number): string {
  if (n === 0) return "No business has your number through Taskiem yet.";
  return n === 1 ? "1 business has your number." : `${n} businesses have your number.`;
}

/** A photo refused, in a person's words (the server's sentence when it
 * says why). */
export function photoProblem(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null;
  if (e.status === 429) return "Too many tries. Wait a moment and try again.";
  if (e.status === 413) return "That photo is too big. Choose one of at most 10 MB.";
  if (e.status === 400) return /^[A-Z].*\.$/.test(e.message.trim()) ? e.message.trim() : "That file can't be used as a photo. Choose a JPEG, PNG or WebP photo.";
  return null;
}

/** The photo's file checked before it is sent: a photo, at most 10 MB. */
export function photoFileProblem(f: Pick<File, "type" | "size">): string | null {
  if (!/^image\/(jpeg|png|webp)$/.test(f.type)) return "Choose a JPEG, PNG or WebP photo.";
  if (f.size > 10 * 1024 * 1024) return "That photo is too big. Choose one of at most 10 MB.";
  return null;
}

/** Why the account can't be deleted yet (409 owns_business or
 * in_business), with the businesses and what to do; null otherwise. */
export function deleteBlocked(e: unknown): { title: string; line: string; businesses: string[]; steps: string[] } | null {
  if (!(e instanceof ApiError) || e.status !== 409) return null;
  const businesses = Array.isArray(e.body.businesses) ? (e.body.businesses as unknown[]).map(String) : [];
  if (e.body.owns_business)
    return {
      title: businesses.length === 1 ? "You own a business on Taskiem" : "You own businesses on Taskiem",
      line: "Your account runs a business, so it can't be deleted yet. Its records name you as its owner.",
      businesses,
      steps: [
        "Make someone else its owner: switch to the business, open Members and give another person the owner role.",
        "Then ask them to remove you from the business.",
        "Come back here and delete your account.",
      ],
    };
  if (e.body.in_business)
    return {
      title: businesses.length === 1 ? "You are part of a business on Taskiem" : "You are part of businesses on Taskiem",
      line: "Your account has a role in a business, so it can't be deleted yet. Its records name you for what you did there.",
      businesses,
      steps: ["Ask its owner to remove you from the business.", "Then come back here and delete your account."],
    };
  return null;
}

/** A proof refused while deleting, in a person's words. */
export function deleteProblem(e: unknown): string | null {
  if (!(e instanceof ApiError)) return null;
  const b = e.body;
  if (b.password_wrong) return "That password is not right.";
  if (b.code_wrong || b.ticket_expired || b.locked) return codeProblem(e);
  if (b.no_phone) return "Your account has no phone to send a code to. Use your passkey or your password.";
  if (b.no_passkey) return "You have no passkey. Use a code to your phone, or your password.";
  if (b.reauth) return "Confirm it is you first: a code to your phone, your passkey or your password.";
  if (e.status === 429) return "Too many tries. Wait a moment and try again.";
  if (e.status === 401 && /passkey/i.test(e.message)) return "That passkey could not be used. Try again, or use another way.";
  if (e.status === 400 && /confirm/i.test(e.message)) return "Tick the box to say you understand it cannot be undone.";
  return null;
}

/** The ways a person can prove it is them to delete the account. */
export type DeleteProof = "code" | "passkey" | "password";

/** Which proofs the account has: a code to its phone, its passkey (where
 * the browser has passkeys), its password. */
export function deleteProofs(phone: string | null | undefined, factors: { passkey: boolean; password: boolean } | undefined, passkeys: boolean): DeleteProof[] {
  const out: DeleteProof[] = [];
  if (phone) out.push("code");
  if (factors?.passkey && passkeys) out.push("passkey");
  if (factors?.password) out.push("password");
  return out;
}

/** The deletion's body: confirm, and the one proof given. */
export function deleteBody(proof: { code?: string; password?: string; credential?: unknown }): Record<string, unknown> {
  const body: Record<string, unknown> = { confirm: true };
  if (proof.code) body.code = proof.code;
  else if (proof.credential) body.credential = proof.credential;
  else if (proof.password) body.password = proof.password;
  return body;
}

/** The signed-out page that says the account is gone, with how many
 * businesses were told (a count, nothing about the person). */
export const accountDeletedPath = (told: number) => `/account-deleted?told=${Math.max(0, Math.floor(told))}`;

/** The count back from that page's address; undefined without one. */
export function toldOf(search: string): number | undefined {
  const t = new URLSearchParams(search).get("told");
  return t !== null && /^\d{1,6}$/.test(t) ? Number(t) : undefined;
}

/** What the page says once the account is gone. */
export function deletedWords(told: number): string {
  const who =
    told === 0
      ? "No business was told anything."
      : told === 1
        ? "The business you chatted with was told only that you left Taskiem."
        : `The ${told} businesses you chatted with were told only that you left Taskiem.`;
  return `Your account is deleted: your chats, saved addresses, photo and settings are gone, and you are signed out everywhere. ${who} Businesses keep their own records of your orders.`;
}
