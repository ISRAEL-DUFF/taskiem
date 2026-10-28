// The business's own riders (dispatch D1; GET/POST /v1/riders): the API's
// shapes, how a rider reads in a row, and the add and change forms.

import { lagosTime } from "./bookings";
import { feeInput, feeKobo, type DeliveryZone } from "./orders";
import { formatPrice } from "./storefront";

export interface Rider {
  id: string;
  name: string;
  phone: string;
  zones: string[];
  fee_kobo: number;
  /** False while on leave: kept, offered nothing. */
  active: boolean;
  /** The chat app they tapped in on, or sms until they do. */
  reached: "whatsapp" | "telegram" | "sms";
  /** What they said to Taskiem, whichever business they ride for. */
  presence: { state: "free" | "busy" | "off"; until?: string; since?: string };
  invited_at?: string | null;
  created_at: string;
  retired_at?: string | null;
}

export interface RidersView {
  riders: Rider[];
  /** The order form's delivery areas, which riders cover. */
  zones: DeliveryZone[];
  /** The page a rider opens to get deliveries on WhatsApp or Telegram. */
  tap_in_url?: string;
}

/** The limits the server holds a rider to (engine/riders). */
export const RIDER_LIMITS = { name: 60 } as const;

/** A rider's state in a few words: Free, Busy till 15:00, Off, On leave. */
export function stateLine(r: Rider): string {
  if (!r.active) return "On leave";
  if (r.presence.state === "off") return "Off";
  if (r.presence.state === "busy") return r.presence.until ? `Busy till ${lagosTime(r.presence.until)}` : "Busy";
  return "Free";
}

/** The badge's tone for a rider's state. */
export function stateTone(r: Rider): "ok" | "warn" | "muted" {
  if (!r.active || r.presence.state === "off") return "muted";
  return r.presence.state === "busy" ? "warn" : "ok";
}

/** Where Taskiem reaches a rider. */
export function reachedLine(r: Rider): string {
  if (r.reached === "whatsapp") return "On WhatsApp";
  if (r.reached === "telegram") return "On Telegram";
  return "By SMS until they tap the link";
}

/** A rider's row underneath their name: areas, fee and where reached. */
export function riderSub(r: Rider): string {
  const parts = [r.zones.length > 0 ? r.zones.join(", ") : "All areas"];
  if (r.fee_kobo > 0) parts.push(`${formatPrice(r.fee_kobo)} a delivery`);
  parts.push(reachedLine(r));
  return parts.join(" · ");
}

/** What the link says when the owner shares it with a rider themselves. */
export const shareRiderText = (url: string) => `Tap to get our deliveries on WhatsApp: ${url}`;

/** A rider as the add or change form holds them. */
export interface RiderForm {
  name: string;
  phone: string;
  zones: string[];
  fee: string;
}

export const emptyRider = (): RiderForm => ({ name: "", phone: "", zones: [], fee: "" });

export const riderForm = (r: Rider): RiderForm => ({ name: r.name, phone: r.phone, zones: [...r.zones], fee: r.fee_kobo ? feeInput(r.fee_kobo) : "" });

/** The POST /v1/riders body (with the phone) or PATCH body (without). */
export function riderRequest(f: RiderForm, adding: boolean): { name: string; phone?: string; zones: string[]; fee_kobo: number } {
  const out: { name: string; phone?: string; zones: string[]; fee_kobo: number } = {
    name: f.name.trim().replace(/\s+/g, " "),
    zones: f.zones,
    fee_kobo: feeKobo(f.fee),
  };
  if (adding) out.phone = f.phone.trim();
  return out;
}

/** Why the form will be refused, or null. */
export function riderProblem(f: RiderForm, adding: boolean): string | null {
  const name = f.name.trim();
  if (!name) return "Enter the rider's name.";
  if (name.length > RIDER_LIMITS.name) return `A name is up to ${RIDER_LIMITS.name} characters.`;
  if (adding && f.phone.replace(/[^\d]/g, "").length < 10) return "Enter the rider's mobile number, like 0803 123 4567.";
  const fee = feeKobo(f.fee);
  if (Number.isNaN(fee) || fee > 10_000_000) return "The fee per delivery is an amount from ₦0 to ₦100,000.";
  return null;
}
