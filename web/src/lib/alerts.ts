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
  | "unmatched"
  | "approval"
  | "deal";

export interface ChatAlert {
  key: string;
  kind: AlertKind;
  at: string;
  title: string;
  line: string;
  amount_kobo?: number;
  /** The stock item, invoice, order, payment entry or run. */
  id?: string;
  reference?: string;
  reorder?: {
    link: string;
    supplier: string;
    seller_item_id: string;
    quantity: number;
    price_kobo: number;
  };
  approval?: Approval;
  who?: string;
}

/** Each kind's tile: its icon, and whether it is a warning. */
export const ALERT_LOOK: Record<
  AlertKind,
  { icon: string; tone: "warn" | "lime" | "plain" }
> = {
  low_stock: { icon: "warn", tone: "warn" },
  overdue: { icon: "invoices", tone: "warn" },
  new_order: { icon: "orders", tone: "lime" },
  rider: { icon: "store", tone: "plain" },
  unmatched: { icon: "money", tone: "plain" },
  approval: { icon: "approvals", tone: "plain" },
  deal: { icon: "money", tone: "warn" },
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
