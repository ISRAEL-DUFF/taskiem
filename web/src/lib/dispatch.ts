// Deliveries by the business's riders (dispatch D2 to D5; docs/dispatch.md):
// the API's shapes, how a delivery reads in a row and on an order's page,
// the dispatch settings form, and what riders are owed and paid.

import { lagosTime, whenLabel } from "./bookings";
import { feeInput, feeKobo } from "./orders";
import { formatPrice } from "./storefront";

export type DeliveryStatus = "offered" | "declined" | "passed" | "withdrawn" | "accepted" | "picked_up" | "arrived" | "delivered" | "released";

/** One rider's part in an order's delivery, from the offer to the door. */
export interface Delivery {
  id: string;
  order_id: string;
  reference: string;
  rider_id: string;
  rider: string;
  rider_phone?: string;
  status: DeliveryStatus;
  offered_by?: string;
  offered_at: string;
  expires_at?: string;
  answered_at?: string | null;
  accepted_at?: string | null;
  picked_up_at?: string | null;
  arrived_at?: string | null;
  delivered_at?: string | null;
  released_at?: string | null;
  fee_kobo: number;
  paid_how?: "transfer" | "cash";
  /** A photo was kept as proof; proof_url shows it for ten minutes. */
  proof?: boolean;
  proof_url?: string;
  /** Where the rider shared their location at the door. */
  location?: string;
  rating?: number | null;
  /** Running late: not picked up, or not arrived, in the business's time. */
  late?: "pickup" | "arrival";
  zone?: string;
}

/** The deliveries an order had, and why it cannot be offered now ("" when it can). */
export interface OrderDeliveries {
  deliveries: Delivery[];
  not_ready: string;
}

/** A delivery's state in words, with its badge's tone. */
export const DELIVERY_STATUS: Record<DeliveryStatus, { label: string; tone: "ok" | "warn" | "info" | "muted" | "danger" }> = {
  offered: { label: "Offered", tone: "warn" },
  declined: { label: "Said they can't", tone: "muted" },
  passed: { label: "No answer", tone: "muted" },
  withdrawn: { label: "Offer taken back", tone: "muted" },
  accepted: { label: "Accepted", tone: "info" },
  picked_up: { label: "On the way", tone: "info" },
  arrived: { label: "At the door", tone: "info" },
  delivered: { label: "Delivered", tone: "ok" },
  released: { label: "Taken back", tone: "muted" },
};

export const deliveryStatus = (s: string) => DELIVERY_STATUS[s as DeliveryStatus] ?? { label: s.replaceAll("_", " "), tone: "muted" as const };

/** Whether a rider holds the delivery now. */
export const isHeld = (d: Delivery) => d.status === "accepted" || d.status === "picked_up" || d.status === "arrived";

/** The delivery the order's rider holds or made, if any; else the live offer. */
export function currentDelivery(list: Delivery[]): Delivery | undefined {
  return list.find((d) => isHeld(d) || d.status === "delivered") ?? list.find((d) => d.status === "offered");
}

/** A held or done delivery's steps so far, oldest first, with their Lagos times. */
export function deliverySteps(d: Delivery): { label: string; at: string }[] {
  const steps: [string, string | null | undefined][] = [
    [`Offered to ${d.rider}`, d.offered_at],
    ["Accepted", d.accepted_at],
    ["Picked up", d.picked_up_at],
    ["At the door", d.arrived_at],
    [d.paid_how === "cash" ? "Delivered, paid in cash" : d.paid_how === "transfer" ? "Delivered, paid by transfer at the door" : "Delivered", d.delivered_at],
    ["Taken back", d.released_at],
  ];
  return steps.filter((s): s is [string, string] => !!s[1]).map(([label, at]) => ({ label, at: lagosTime(at) }));
}

/** A delivery's row underneath the reference: rider, area, fee and what is late. */
export function deliverySub(d: Delivery): string {
  const parts = [d.rider];
  if (d.zone) parts.push(d.zone);
  if (d.fee_kobo > 0) parts.push(`fee ${formatPrice(d.fee_kobo)}`);
  if (d.late === "pickup") parts.push("not picked up in time");
  if (d.late === "arrival") parts.push("not arrived in time");
  if (d.rating) parts.push(`rated ${d.rating} of 5`);
  return parts.join(" · ");
}

/** The words for an offer's outcome, after POST .../offer. */
export function offerOutcome(o: { status: string; rider?: string }): string {
  switch (o.status) {
    case "offered":
      return `Offered to ${o.rider ?? "a rider"}. They have a few minutes to accept.`;
    case "held":
      return `${o.rider ?? "A rider"} has it already.`;
    case "asked_owner":
      return "You were asked in chat who takes it.";
    case "no_rider":
      return "No free rider covers this area now. You were told in chat.";
    default:
      return "Done";
  }
}

// --- the dispatch settings ---

export interface DispatchSettings {
  /** rotation: offered to free riders in turn; ask: the owner is asked in chat who takes it. */
  mode: "rotation" | "ask";
  offer_minutes: number;
  pickup_minutes: number;
  arrive_minutes: number;
  /** Riders are asked for a photo at the door. */
  proof: boolean;
  owe_limit_kobo: number;
  rating_mark: number;
}

export interface SettingsForm {
  mode: "rotation" | "ask";
  offer: string;
  pickup: string;
  arrive: string;
  proof: boolean;
  oweLimit: string;
  ratingMark: string;
}

export const settingsForm = (s: DispatchSettings): SettingsForm => ({
  mode: s.mode,
  offer: String(s.offer_minutes),
  pickup: String(s.pickup_minutes),
  arrive: String(s.arrive_minutes),
  proof: s.proof,
  oweLimit: feeInput(s.owe_limit_kobo),
  ratingMark: String(s.rating_mark),
});

const whole = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : NaN);

/** Why the settings will be refused, or null. */
export function settingsProblem(f: SettingsForm): string | null {
  const offer = whole(f.offer);
  if (!(offer >= 1 && offer <= 60)) return "Riders have 1 to 60 minutes to accept an offer.";
  const pickup = whole(f.pickup);
  if (!(pickup >= 5 && pickup <= 240)) return "Picking up takes 5 to 240 minutes.";
  const arrive = whole(f.arrive);
  if (!(arrive >= 10 && arrive <= 600)) return "Getting to the customer takes 10 to 600 minutes.";
  if (Number.isNaN(feeKobo(f.oweLimit)) || f.oweLimit.trim() === "") return "Enter the cash a rider may owe, like 20000.";
  const mark = Number(f.ratingMark);
  if (!(mark >= 1 && mark <= 5)) return "The rating mark is 1 to 5.";
  return null;
}

/** The PUT /v1/dispatch/settings body. */
export const settingsRequest = (f: SettingsForm): DispatchSettings => ({
  mode: f.mode,
  offer_minutes: whole(f.offer),
  pickup_minutes: whole(f.pickup),
  arrive_minutes: whole(f.arrive),
  proof: f.proof,
  owe_limit_kobo: feeKobo(f.oweLimit),
  rating_mark: Number(f.ratingMark),
});

// --- what riders owe and are paid ---

/** Cash a rider took at the door and still owes. */
export interface Owed {
  rider_id: string;
  rider: string;
  owed_kobo: number;
  deliveries: number;
  since?: string;
}

/** What a rider is due now: their fees less the cash they owe. */
export interface PayLine {
  rider_id: string;
  rider: string;
  deliveries: number;
  fees_kobo: number;
  owed_kobo: number;
  net_kobo: number;
  /** Their bank account as it may be shown; "" until set. */
  bank: string;
}

export interface Payout {
  payout_id: string;
  rider_id: string;
  rider: string;
  deliveries: number;
  fees_kobo: number;
  owed_kobo: number;
  amount_kobo: number;
  status: "pending" | "paid" | "failed" | "cancelled";
  created_at?: string;
}

export interface RidersPay {
  lines: PayLine[];
  payouts: Payout[];
  workflow_id: string | null;
}

/** A pay line underneath the rider's name. */
export function payLineSub(l: PayLine): string {
  const parts = [`${l.deliveries} ${l.deliveries === 1 ? "delivery" : "deliveries"}, fees ${formatPrice(l.fees_kobo)}`];
  if (l.owed_kobo > 0) parts.push(`less ${formatPrice(l.owed_kobo)} cash they owe`);
  parts.push(l.bank || "No bank account yet");
  return parts.join(" · ");
}

/** A payout underneath the rider's name: its deliveries, the cash taken off, and when it started. */
export function payoutSub(x: Payout): string {
  let s = `${x.deliveries} ${x.deliveries === 1 ? "delivery" : "deliveries"}`;
  if (x.owed_kobo) s += `, ${formatPrice(x.owed_kobo)} cash taken off`;
  if (x.created_at) s += ` · started ${whenLabel(x.created_at)}`;
  return s;
}

/** What the owner is asked before marking a payout stuck being paid as not paid. */
export const notPaidQuestion = (x: Payout) =>
  `Mark ${x.rider}'s payout of ${formatPrice(x.amount_kobo)} as not paid? Only once your bank shows it did not go: their deliveries are then paid in the next payout, so marking a payment that went through pays them twice.`;

export const PAYOUT_STATUS: Record<Payout["status"], { label: string; tone: "ok" | "warn" | "muted" | "danger" }> = {
  pending: { label: "Being paid", tone: "warn" },
  paid: { label: "Paid", tone: "ok" },
  failed: { label: "Not paid", tone: "danger" },
  cancelled: { label: "Not approved", tone: "muted" },
};

export const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"] as const;

/** Why a rider's bank account will be refused, or null. */
export function bankProblem(code: string, number: string): string | null {
  if (!/^[0-9A-Za-z]{3,6}$/.test(code.trim())) return "Choose the rider's bank.";
  if (!/^\d{10}$/.test(number.trim())) return "An account number is 10 digits.";
  return null;
}

/** Why a cash amount handed in will be refused, or null. */
export function remitProblem(amount: string, owedKobo: number): string | null {
  const k = feeKobo(amount);
  if (Number.isNaN(k) || k <= 0) return "Enter the amount, like 6500.";
  if (k > owedKobo) return `They owe ${formatPrice(owedKobo)}, less than that.`;
  return null;
}
