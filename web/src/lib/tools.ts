// The chat's tools (docs/spec/chat-tools.md §3): the tiles of the tools
// panel and what each one's sheet lists. Phase 1 shows each tool's list in
// the sheet frame, with "Open full page"; phase 2 makes them work in place.

import { formatPrice } from "./storefront";
import type { StockItem, StockView } from "./stock";
import type { Service } from "./bookings";
import type { PriceList } from "./pricelists";
import type { TradeOrder } from "./tradeorders";
import type { Table } from "./tables";

export type ToolKey =
  | "products"
  | "services"
  | "stock"
  | "prices"
  | "orders"
  | "tables";

export type PillTone = "warn" | "lime" | "plain";

/** One row of a tool's sheet. */
export interface ToolRow {
  id: string;
  name: string;
  sub: string;
  pill?: { text: string; tone: PillTone };
  href: string;
  /** For the chips: which it is in ("low", "hidden", "new"). */
  tags: string[];
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
    tags: [...(it.low ? ["low"] : []), ...(!it.available ? ["hidden"] : [])],
  }));
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
        .filter((r) => r.pill)
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
      })),
    line: (r) => `${tagged(r, "shared")} shared`,
    more: "Open price lists",
  },
  {
    key: "orders",
    title: "Orders",
    icon: "invoices",
    perm: "storefront.manage",
    url: "/v1/trade-orders",
    page: "/trade-orders",
    rows: (data) =>
      ((data as { orders?: TradeOrder[] } | undefined)?.orders ?? []).map(
        (o) => ({
          id: o.id,
          name: `${o.order_reference} · ${o.buyer_name}`,
          sub: formatPrice(o.total_kobo),
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
        }),
      ),
    line: () => "From businesses",
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
      })),
    line: (r) => count(r.length, "table"),
    more: "Open tables",
  },
];

/** The tools a person may open, matching what follows "/" (a word's start). */
export function toolsFor(can: (perm: string) => boolean, query = ""): Tool[] {
  const q = query.trim().toLowerCase();
  return TOOLS.filter(
    (t) =>
      can(t.perm) &&
      (q === "" ||
        t.title
          .toLowerCase()
          .split(/\s+/)
          .some((w) => w.startsWith(q)) ||
        t.key.startsWith(q)),
  );
}

/** The rows a sheet shows: those a chip picks, then those its search finds. */
export function filterRows(
  rows: ToolRow[],
  chip: string | undefined,
  search: string,
): ToolRow[] {
  const q = search.trim().toLowerCase();
  return rows.filter(
    (r) =>
      (!chip || r.tags.includes(chip)) &&
      (q === "" ||
        r.name.toLowerCase().includes(q) ||
        r.sub.toLowerCase().includes(q)),
  );
}
