// A business's side of customers chatting in the app (docs/spec/taskiem-
// for-everyone.md §8.2, §8.3, §8.6, §15): its chat link and settings, what
// happens when nobody answers, offers, and the words for an app thread.
// Pure helpers, tested on their own.

/** The business's chat link (GET /v1/customers/chat-link). */
export interface ChatLink {
  code: string;
  url: string;
  created_at: string;
  /** "Chat with {business} on Taskiem: {link}". */
  words: string;
  /** Where its QR code is. */
  qr: string;
  settings: ChatLinkSettings;
}

export interface ChatLinkSettings {
  takes_chats: boolean;
  storefront_button: "both" | "taskiem" | "whatsapp";
  /** Null: the default (on with no WhatsApp number of its own). */
  link_in_updates: boolean | null;
  /** As it applies now. */
  updates_link: boolean;
  /** The Orders area is off: no chats are taken, whatever takes_chats says. */
  orders_off?: boolean;
  /** The chat link offers Chat on WhatsApp beside Chat on Taskiem (§11). */
  link_whatsapp?: boolean;
  /** The business's own WhatsApp number, when it has one. */
  own_number?: string;
}

/** What the business does when nobody answers (GET /v1/settings/customer-chats). */
export interface CustomerChatSettings {
  closed_reply: boolean;
  closed_words: string;
  needs_you: boolean;
  tell_owner: boolean;
}

/** The storefront's button, as the choice reads. */
export function buttonWords(b: ChatLinkSettings["storefront_button"]): string {
  switch (b) {
    case "taskiem":
      return "Chat with us (in Taskiem) only";
    case "whatsapp":
      return "Chat on WhatsApp only";
    default:
      return "Both: Chat with us and Chat on WhatsApp";
  }
}

/** "Sent to 3 customers", with who was skipped. */
export function offerCount(sent: number, skipped: number): string {
  const who = (n: number) => `${n} ${n === 1 ? "customer" : "customers"}`;
  if (sent === 0 && skipped === 0) return "No customer has said yes to your offers yet.";
  const out = `Sent to ${who(sent)}.`;
  return skipped > 0 ? `${out} ${who(skipped)} skipped: they had two offers this week, or stopped messages.` : out;
}

/** A card a business sends in an app thread (POST …/messages {card}). */
export type CardAsk =
  | { type: "order" | "pay"; reference: string }
  | { type: "booking"; service_id: string }
  | { type: "product"; item_id: string }
  | { type: "invoice"; invoice_id: string }
  | { type: "delivery"; reference: string };

/** Taking chats while the Orders area is off, in words. */
export const ORDERS_OFF_WORDS =
  "Your Orders area is off, so chats are closed: customers cannot start a chat with you, and your link says so. Turn Orders on to take chats again.";

/** The line under an app customer's name. */
export function appThreadLine(c: { line?: string; typing?: boolean; blocked?: boolean; stopped?: boolean; lines?: unknown[] }): string {
  if (c.typing) return "typing…";
  if ((c.lines?.length ?? 0) > 1) return "On WhatsApp and in the app";
  if (c.line !== "app") return "Customer on WhatsApp";
  if (c.blocked) return "In the app · you blocked them";
  if (c.stopped) return "In the app · stopped messages";
  return "Customer in the app";
}

/** What the box says about an app thread, or null when a reply goes. */
export function appNote(c: { blocked?: boolean; stopped?: boolean; notice?: string }): { tone: "stop"; text: string } | null {
  if (c.blocked) return { tone: "stop", text: "You blocked this customer. Unblock them to write to them." };
  if (c.stopped) return { tone: "stop", text: c.notice || "This customer has stopped messages." };
  return null;
}

/** Whether an offer may go to this customer: they said yes. */
export const mayOffer = (c: { line?: string; offers?: string }) => c.line === "app" && c.offers === "yes";
