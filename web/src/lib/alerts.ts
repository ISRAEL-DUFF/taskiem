// Alerts you can act on (docs/spec/chat-tools.md §6): cards at the end of
// the Taskiem conversation, each with its next step. What the API gives
// (GET /v1/chat-alerts), and the words and looks of each kind.
import type { Approval } from "../api";
import { formatPrice } from "./storefront";

export type AlertKind =
  | "low_stock"
  | "overdue"
  | "new_order"
  | "rider"
  | "confirm"
  | "unmatched"
  | "approval"
  | "school_approval"
  | "deal"
  | "order_draft"
  | "customer_waiting"
  | "page";

export interface ChatAlert {
  key: string;
  kind: AlertKind;
  at: string;
  title: string;
  line: string;
  amount_kobo?: number;
  /** The stock item, invoice, order, payment entry or run; for an
   * order read from a customer's message, their thread. */
  id?: string;
  reference?: string;
  reorder?: {
    link: string;
    supplier: string;
    seller_item_id: string;
    quantity: number;
    price_kobo: number;
    /** A product sold in sizes and colours: how many of each of the
     * supplier's sizes (seller_item_id is then their product). */
    sizes?: Record<string, number>;
  };
  approval?: Approval;
  who?: string;
  /** A Confirm card's proof of delivery (device fields §6). */
  proof?: AlertProof;
}

/** A delivery's proof: the pin at its masked point (about 100 m), the
 * photo, and how each was got. */
export interface AlertProof {
  where?: {
    lat: number;
    lng: number;
    source: "device" | "sent" | "typed";
    coarse?: boolean;
    accuracy_m?: number;
  };
  mark?:
    | "confirmed_at_door"
    | "far_from_address"
    | "said_by_rider"
    | "read_by_phone"
    | "no_location";
  photo_url?: string;
  photo_source?: "camera" | "sent";
}

/** Which was read by the phone and which was sent, in words. */
export function proofWords(p: AlertProof): { where?: string; photo?: string } {
  const where = {
    confirmed_at_door: "Location read by the rider's phone, at the door",
    far_from_address:
      "Location read by the rider's phone, far from the address",
    read_by_phone: "Location read by the rider's phone",
    said_by_rider: "Location sent by the rider (a pin they placed)",
    no_location: undefined,
  }[p.mark ?? "no_location"];
  const photo =
    p.photo_source === "camera"
      ? "Photo taken in the app"
      : p.photo_source === "sent"
        ? "Photo sent by the rider"
        : undefined;
  return { where, photo };
}

/** The masked pin on a map: never the coordinates in words. */
export const pinLink = (w: NonNullable<AlertProof["where"]>) =>
  `https://www.google.com/maps?q=${w.lat.toFixed(3)},${w.lng.toFixed(3)}`;

/** Each kind's tile: its icon, and whether it is a warning. */
export const ALERT_LOOK: Record<
  AlertKind,
  { icon: string; tone: "warn" | "lime" | "plain" }
> = {
  low_stock: { icon: "warn", tone: "warn" },
  overdue: { icon: "invoices", tone: "warn" },
  new_order: { icon: "orders", tone: "lime" },
  rider: { icon: "store", tone: "plain" },
  confirm: { icon: "money", tone: "lime" },
  unmatched: { icon: "money", tone: "plain" },
  approval: { icon: "approvals", tone: "plain" },
  school_approval: { icon: "approvals", tone: "plain" },
  deal: { icon: "money", tone: "warn" },
  order_draft: { icon: "chat", tone: "lime" },
  customer_waiting: { icon: "chat", tone: "warn" },
  page: { icon: "dashboard", tone: "warn" },
};

/** "Reorder 20 from Bayo Rice Mills". */
export const reorderWords = (r: NonNullable<ChatAlert["reorder"]>) =>
  `Reorder ${r.quantity} from ${r.supplier}`;

/** What a reorder comes to: "20 × ₦78,000 = ₦1,560,000". */
export const reorderTotal = (r: NonNullable<ChatAlert["reorder"]>) =>
  formatPrice(r.quantity * r.price_kobo);

/** "Just now", "2 min ago", "3 h ago", "Yesterday", "4 Nov". */
export function ago(iso: string, now = Date.now()): string {
  const s = Math.max(0, (now - Date.parse(iso)) / 1000);
  if (s < 60) return "Just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  if (s < 2 * 86400) return "Yesterday";
  return new Date(iso).toLocaleDateString("en-GB", {
    day: "numeric",
    month: "short",
  });
}

/** The cards in their order, without those just dealt with here. */
export const shownAlerts = (all: ChatAlert[] | undefined, gone: Set<string>) =>
  (all ?? []).filter((a) => !gone.has(a.key));
