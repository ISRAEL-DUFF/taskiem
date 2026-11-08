// The chat's tools (docs/spec/chat-tools.md §3): the tiles of the tools
// panel and what each one's sheet lists. Phase 1 shows each tool's list in
// the sheet frame, with "Open full page"; phase 2 makes them work in place.

import { categoriesOf } from "./mentions";
import { formatPrice } from "./storefront";
import type { StockItem, StockView } from "./stock";
import type { Service } from "./bookings";
import type { PriceList } from "./pricelists";
import type { TradeOrder } from "./tradeorders";
import type { Table } from "./tables";
import type { Purchase } from "./purchases";
import type { Bill } from "./bills";
import { purchaseState } from "./buyer";

export type ToolKey =
  | "products"
  | "services"
  | "stock"
  | "prices"
  | "orders"
  | "tables"
  | "purchases"
  | "bills"
  | "order"
  | "sell"
  | "handover";

export type PillTone = "warn" | "lime" | "plain";

/** One row of a tool's sheet. */
export interface ToolRow {
  id: string;
  name: string;
  sub: string;
  pill?: { text: string; tone: PillTone };
  href: string;
  /** For the chips: which it is in ("low", "hidden", "new", and
   * "cat:Sneakers" for its category). */
  tags: string[];
  /** Its category and its words (a product's description): search reads
   * them after the name. */
  category?: string;
  words?: string;
  /** What it is, for its form (a stock item, an order, a table…). */
  data?: unknown;
  /** The linked business it is with (a buyer's orders and bills): ""
   * for one with a business not on Taskiem, so no conversation shows it. */
  link?: string;
}

export interface Tool {
  key: ToolKey;
  title: string;
  icon: string;
  /** The permission it needs. */
  perm: string;
  url: string;
  page: string;
  /** The rows, from what the url answers. */
  rows: (data: unknown) => ToolRow[];
  /** The tile's second line, from the rows. */
  line: (rows: ToolRow[]) => string;
  /** The tile's badge ("3 low", "4 new"), when there is one. */
  badge?: (rows: ToolRow[]) => { text: string; tone: PillTone } | undefined;
  /** The chips above the list: a word and the tag it shows. */
  chips?: { label: string; tag: string }[];
  /** The full page's button. */
  more: string;
  /** Rows with no name of their own to show (an order, a bill): an icon
   * on each row's tile, not letters of a reference. */
  rowIcon?: string;
  /** Only for the staff whose role gives it (Handover). */
  staffOnly?: boolean;
}

const count = (n: number, one: string, many = `${one}s`) =>
  `${n} ${n === 1 ? one : many}`;
const tagged = (rows: ToolRow[], t: string) =>
  rows.filter((r) => r.tags.includes(t)).length;

function stockPill(it: StockItem): ToolRow["pill"] {
  if (it.stock === null) return undefined;
  if (it.low || it.stock <= 0)
    return { text: it.stock <= 0 ? "Out" : `${it.stock} left`, tone: "warn" };
  return { text: `${it.stock} in stock`, tone: "plain" };
}

function stockRows(data: unknown): ToolRow[] {
  const v = data as StockView | undefined;
  return (v?.items ?? []).map((it) => ({
    id: it.id,
    name: it.name,
    sub:
      it.price_kobo !== undefined
        ? `${formatPrice(it.price_kobo)}${it.unit ? ` a ${it.unit}` : ""}`
        : "No price yet",
    pill: stockPill(it),
    href: "/storefront/stock",
    tags: [
      ...(it.low ? ["low"] : []),
      ...(!it.available ? ["hidden"] : []),
      ...(it.category ? [categoryTag(it.category)] : []),
    ],
    category: it.category,
    words: it.description,
    data: it,
  }));
}

/** A category as a chip's tag. */
export const categoryTag = (c: string) => `cat:${c.trim().toLowerCase()}`;

/** The chips for the categories of a sheet's rows, after its own: the most
 * used first; a category of one item gets none, unless it is the one shown
 * (on: the chip that is on). */
export function categoryChips(
  rows: { category?: string }[],
  on?: string,
): { label: string; tag: string }[] {
  return categoriesOf(rows)
    .filter((c) => c.count > 1 || categoryTag(c.name) === on)
    .map((c) => ({ label: c.name, tag: categoryTag(c.name) }));
}

const lowBadge = (rows: ToolRow[]) => {
  const n = tagged(rows, "low");
  return n > 0 ? { text: `${n} low`, tone: "warn" as const } : undefined;
};

const ORDER_WORD: Record<string, string> = {
  received: "New",
  changed: "Changed",
  proposed: "Draft",
  accepted: "Accepted",
  ready: "Ready",
  dispatched: "On the way",
  delivered: "Delivered",
  declined: "Declined",
  cancelled: "Cancelled",
};

export const TOOLS: Tool[] = [
  {
    key: "sell",
    title: "Sell",
    icon: "money",
    perm: "table.edit",
    url: "/v1/stock",
    page: "/storefront/stock",
    // Things that can be sold: those with a price, on sale (as the sheet).
    rows: (data) =>
      stockRows(data).filter((r) => {
        const it = r.data as StockItem;
        return it.price_kobo !== undefined && it.available;
      }),
    line: () => "Record a sale",
    more: "Open stock",
  },
  {
    key: "handover",
    title: "Handover",
    icon: "clock",
    perm: "table.read",
    url: "/v1/chat-handover",
    page: "/storefront/stock",
    rows: () => [],
    line: () => "Hand the shift over",
    staffOnly: true,
    more: "Open stock",
  },
  {
    key: "products",
    title: "Products",
    icon: "box",
    perm: "table.read",
    url: "/v1/stock",
    page: "/storefront/stock",
    rows: stockRows,
    line: (r) => count(r.length, "item"),
    chips: [
      { label: "Low stock", tag: "low" },
      { label: "Hidden", tag: "hidden" },
    ],
    more: "Open products",
  },
  {
    key: "services",
    title: "Services",
    icon: "clock",
    perm: "storefront.manage",
    url: "/v1/storefront/services",
    page: "/storefront/bookings",
    rows: (data) =>
      ((data as { items?: Service[] } | undefined)?.items ?? []).map((s) => ({
        id: s.id,
        name: s.name,
        sub: [
          s.price_kobo !== undefined && s.price_kobo !== null
            ? formatPrice(s.price_kobo)
            : "Price on the day",
          s.duration_minutes ? `${s.duration_minutes} min` : "",
        ]
          .filter(Boolean)
          .join(" · "),
        pill: s.available ? undefined : { text: "Hidden", tone: "plain" },
        href: "/storefront/bookings",
        tags: s.available ? [] : ["hidden"],
        data: s,
      })),
    line: (r) => count(r.length, "service"),
    more: "Open services",
  },
  {
    key: "stock",
    title: "Stock",
    icon: "store",
    perm: "table.read",
    url: "/v1/stock",
    page: "/storefront/stock",
    rows: (data) =>
      stockRows(data)
        // Every item, those with no count yet too (their first goods in).
        .map((r) =>
          r.pill
            ? r
            : { ...r, pill: { text: "No count", tone: "plain" as const } },
        )
        .sort(
          (a, b) =>
            Number(b.tags.includes("low")) - Number(a.tags.includes("low")),
        ),
    line: () => "In the store",
    badge: lowBadge,
    chips: [{ label: "Low stock", tag: "low" }],
    more: "Open stock",
  },
  {
    key: "prices",
    title: "Price lists",
    icon: "tag",
    perm: "storefront.manage",
    url: "/v1/price-lists",
    page: "/storefront/price-lists",
    rows: (data) =>
      ((data as { lists?: PriceList[] } | undefined)?.lists ?? []).map((l) => ({
        id: l.id,
        name: l.name,
        sub: `${count(l.items.length, "item")} · ${l.links.length > 0 ? `shared with ${count(l.links.length, "business", "businesses")}` : "not shared"}`,
        href: "/storefront/price-lists",
        tags: l.links.length > 0 ? ["shared"] : [],
        data: l,
      })),
    line: (r) => `${tagged(r, "shared")} shared`,
    more: "Open price lists",
  },
  {
    key: "orders",
    title: "Orders",
    icon: "invoices",
    // Working orders is a cashier's too (order.handle).
    perm: "storefront.manage|order.handle",
    url: "/v1/trade-orders",
    page: "/trade-orders",
    rows: (data) =>
      ((data as { orders?: TradeOrder[] } | undefined)?.orders ?? []).map(
        (o) => ({
          id: o.id,
          name: o.order_reference,
          sub: `${o.buyer_name} · ${formatPrice(o.total_kobo)}`,
          pill: {
            text: ORDER_WORD[o.status] ?? o.status,
            tone:
              o.status === "received"
                ? "lime"
                : o.status === "changed"
                  ? "warn"
                  : "plain",
          },
          href: `/trade-orders/${encodeURIComponent(o.id)}`,
          tags: o.status === "received" ? ["new"] : [],
          data: o,
        }),
      ),
    line: () => "From businesses",
    rowIcon: "invoices",
    badge: (r) => {
      const n = tagged(r, "new");
      return n > 0 ? { text: `${n} new`, tone: "lime" } : undefined;
    },
    chips: [{ label: "New", tag: "new" }],
    more: "Open orders",
  },
  {
    key: "tables",
    title: "Tables",
    icon: "tables",
    perm: "table.read",
    url: "/v1/tables",
    page: "/tables",
    rows: (data) =>
      ((data as { tables?: Table[] } | undefined)?.tables ?? []).map((t) => ({
        id: t.id,
        name: t.title || t.name,
        sub:
          t.row_count !== undefined ? count(t.row_count, "row") : t.description,
        href: `/tables/${encodeURIComponent(t.id)}`,
        tags: [],
        data: t,
      })),
    line: (r) => count(r.length, "table"),
    more: "Open tables",
  },
];

const day = (iso: string) =>
  new Date(iso).toLocaleDateString("en-GB", { day: "numeric", month: "short" });

/** The buyer's tools (phase 3): the orders sent to suppliers, and their bills. */
TOOLS.push(
  {
    key: "purchases",
    title: "Your orders",
    icon: "orders",
    perm: "table.read",
    url: "/v1/stock/purchases",
    page: "/storefront/suppliers",
    rows: (data) =>
      ((data as { purchases?: Purchase[] } | undefined)?.purchases ?? []).map(
        (p) => {
          const st = purchaseState(p);
          return {
            id: p.id,
            name: p.reference,
            sub: `${p.supplier} · ${formatPrice(p.total_kobo)} · ${day(p.created_at)}`,
            pill: {
              text: st.word,
              tone: st.tone === "danger" ? "warn" : st.tone,
            },
            href: `/purchases/${encodeURIComponent(p.id)}`,
            tags: st.quote
              ? ["quote"]
              : p.status === "received" || p.status === "cancelled"
                ? []
                : ["open"],
            data: p,
            link: p.link_id ?? "",
          };
        },
      ),
    line: (r) => count(r.length, "order"),
    rowIcon: "orders",
    badge: (r) => {
      const n = tagged(r, "quote");
      return n > 0 ? { text: `${n} to confirm`, tone: "warn" } : undefined;
    },
    chips: [
      { label: "Open", tag: "open" },
      { label: "Quotes", tag: "quote" },
    ],
    more: "Open suppliers",
  },
  {
    key: "bills",
    title: "Bills",
    icon: "money",
    perm: "invoice.read",
    url: "/v1/bills?status=all",
    page: "/bills",
    rows: (data) =>
      ((data as { bills?: Bill[] } | undefined)?.bills ?? []).map((b) => ({
        id: b.id,
        name: b.reference,
        sub: `${b.supplier_name} · ${
          b.due_kobo > 0
            ? `${formatPrice(b.due_kobo)} to pay${b.due_on ? ` · due ${day(b.due_on)}` : ""}`
            : formatPrice(b.amount_kobo)
        }`,
        pill:
          b.status === "paid"
            ? { text: "Paid", tone: "lime" }
            : b.disputed
              ? { text: "Questioned", tone: "warn" }
              : b.status === "void"
                ? { text: "Cancelled", tone: "plain" }
                : { text: "To pay", tone: "warn" },
        href: `/bills/${encodeURIComponent(b.id)}`,
        tags: b.due_kobo > 0 && b.status !== "void" ? ["due"] : [],
        data: b,
        link: b.link,
      })),
    line: (r) => `${tagged(r, "due")} to pay`,
    rowIcon: "money",
    badge: (r) => {
      const n = tagged(r, "due");
      return n > 0 ? { text: `${n} to pay`, tone: "warn" } : undefined;
    },
    chips: [{ label: "To pay", tag: "due" }],
    more: "Open bills",
  },
);

/** Staff who work from chat see their role's tools only (chat-tools.md §7):
 * a cashier sells, keeps the stock and works the orders; a shift manager
 * also hands the shift over. Anyone with another role sees every tool
 * their permissions allow. */
export const ROLE_TOOLS: Record<string, ToolKey[]> = {
  cashier: ["sell", "orders", "stock"],
  shift_manager: ["handover", "sell", "orders", "stock", "bills"],
};

/** The tools a person's roles shape them to, or undefined for all. */
export function roleTools(roles: string[] | undefined): Set<ToolKey> | undefined {
  if (!roles || roles.length === 0 || roles.some((r) => !(r in ROLE_TOOLS))) return undefined;
  return new Set(roles.flatMap((r) => ROLE_TOOLS[r] ?? []));
}

/** The tools a person may open, matching what follows "/" (a word's start),
 * shaped to their role when they are staff. */
export function toolsFor(can: (perm: string) => boolean, query = "", roles?: string[]): Tool[] {
  const q = query.trim().toLowerCase();
  const only = roleTools(roles);
  return TOOLS.filter(
    (t) =>
      can(t.perm) &&
      (only ? only.has(t.key) : !t.staffOnly) &&
      (q === "" ||
        t.title
          .toLowerCase()
          .split(/\s+/)
          .some((w) => w.startsWith(q)) ||
        t.key.startsWith(q)),
  );
}

/** The rows a sheet shows: those a chip picks, then those its search
 * finds, by name first, then by the rest (its line, category, words). */
export function filterRows(
  rows: ToolRow[],
  chip: string | undefined,
  search: string,
): ToolRow[] {
  const q = search.trim().toLowerCase();
  const picked = rows.filter((r) => !chip || r.tags.includes(chip));
  if (q === "") return picked;
  const named = picked.filter((r) => r.name.toLowerCase().includes(q));
  const rest = picked.filter(
    (r) =>
      !r.name.toLowerCase().includes(q) &&
      [r.sub, r.category ?? "", r.words ?? ""].some((t) =>
        t.toLowerCase().includes(q),
      ),
  );
  return [...named, ...rest];
}
