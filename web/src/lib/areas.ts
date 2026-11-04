// Business areas (docs/spec/business-areas.md): which parts of Taskiem a
// business uses. The server refuses an area that is off; this decides what
// the app shows, and the plain words for the settings page.

import type { Me } from "../api";

export type AreaKey = "storefront" | "orders" | "stock" | "money" | "links" | "schools";

/** One row of GET /v1/settings/areas. */
export interface AreaRow {
  area: AreaKey;
  name: string;
  covers: string;
  depends_on: AreaKey[];
  enabled: boolean;
}

/** Whether an area is on. Without word from the server (an older server, a
 * failed read) the area shows: the server still refuses what is off. */
export function areaOn(areas: Me["areas"], key: AreaKey): boolean {
  return areas?.[key] !== false;
}

/** The plain name of an area, for sentences. */
export const AREA_NAMES: Record<AreaKey, string> = {
  storefront: "Online shop",
  orders: "Orders",
  stock: "Stock",
  money: "Money and invoices",
  links: "Linked businesses",
  schools: "Schools",
};

/** Which area a page belongs to, by its address: the longest prefix wins
 * (/storefront/orders is Orders although /storefront is the shop). It
 * mirrors the server's list (api/areas.go); the server is what refuses. */
const PAGE_AREAS: [string, AreaKey][] = [
  ["/storefront/orders", "orders"],
  ["/trade-orders", "orders"],
  ["/storefront/stock", "stock"],
  ["/storefront/price-lists", "stock"],
  ["/storefront/suppliers", "stock"],
  ["/purchases", "stock"],
  ["/storefront", "storefront"],
  ["/invoices", "money"],
  ["/money", "money"],
  ["/bills", "money"],
  ["/settings/links", "links"],
  ["/link-chat", "links"],
  ["/links", "links"],
];

export function areaOfPage(pathname: string): AreaKey | undefined {
  const path = pathname.replace(/\/+$/, "");
  let best: [string, AreaKey] | undefined;
  for (const x of PAGE_AREAS) {
    if ((path === x[0] || path.startsWith(x[0] + "/")) && (!best || x[0].length > best[0].length)) best = x;
  }
  return best?.[1];
}

/** The area a page needs that is off, if any. */
export function areaOffFor(pathname: string, areas: Me["areas"]): AreaKey | undefined {
  const a = areaOfPage(pathname);
  return a && !areaOn(areas, a) ? a : undefined;
}

/** What turning an area off hides, in a sentence. */
export function offWords(area: AreaKey): string {
  return `${AREA_NAMES[area]} will disappear from your menu and the pages that use it. Nothing is deleted: turn it on again any time and it is all back.`;
}

/** Areas that need this one while they are on. */
export function neededBy(rows: AreaRow[], area: AreaKey): AreaRow[] {
  return rows.filter((r) => r.enabled && r.depends_on.includes(area));
}
