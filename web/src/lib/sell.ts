// Forms that sell (docs/spec/forms-that-sell.md §5): an order form that
// sells one product in packages ("1 for ₦25,000, 2 for ₦45,000"), made
// from a hosted form (Sell something with this form) or on a front (Add an
// order form), and the code that shows one order form on a website. The
// server prices every order from the published packages; what is here is
// only what the owner types and sees.
import { ApiError } from "../api";
import { formatPrice } from "./storefront";
import { nairaOf } from "./fronts";

export const MAX_PACKAGES = 6;
export const MAX_PACKAGE_QTY = 20;
export const MAX_LABEL = 60;
export const MAX_NOTICE = 160;

export interface Package {
  qty: number;
  price_kobo: number;
  label?: string;
}

/** What an order form sells: one item in packages, or a basket of items. */
export interface SellSpec {
  item?: string;
  packages?: Package[];
  choose?: "all" | "each";
  items?: string[];
}

/** A product a front's order form can sell (GET …/sellable). */
export interface Sellable {
  id: string;
  name: string;
  price_kobo: number | null;
  photo?: string;
  stock: number | null;
  sizes: number;
}

/** One order form's code for a website (GET /v1/storefront/embed). */
export interface ActionEmbed {
  action_id: string;
  label: string;
  on: boolean;
  url: string;
  embed_url: string;
  frame: string;
  script: string;
  plain: string;
}

/** A package row as typed: how many and the price in naira, as text. */
export interface PackageRow {
  qty: string;
  price: string;
  label: string;
}

/** Three packages to start from: one, two and three of the item, the
 * bigger ones a little cheaper each, rounded to ₦500. */
export function startingRows(priceKobo: number | null): PackageRow[] {
  const each = priceKobo ? priceKobo / 100 : 0;
  const round = (n: number) => String(Math.round(n / 500) * 500 || "");
  return [
    { qty: "1", price: each ? String(each) : "", label: "" },
    { qty: "2", price: each ? round(each * 2 * 0.9) : "", label: "" },
    { qty: "3", price: each ? round(each * 3 * 0.85) : "", label: "" },
  ];
}

/** A package's words when it has none of its own: "2 items · ₦45,000". */
export function packageLabel(qty: number, priceKobo: number): string {
  return `${qty} ${qty === 1 ? "item" : "items"} · ${formatPrice(priceKobo)}`;
}

const hasLink = (s: string) =>
  /(https?:\/\/|www\.|[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,})/i.test(s);

/** The packages typed, read; or what is wrong with them, in plain words. */
export function readPackages(rows: PackageRow[]): {
  packages: Package[];
  problem: string;
} {
  const packages: Package[] = [];
  if (rows.length === 0)
    return { packages, problem: "Add at least one package." };
  if (rows.length > MAX_PACKAGES)
    return { packages, problem: `Up to ${MAX_PACKAGES} packages.` };
  const seen = new Set<number>();
  for (const r of rows) {
    const qty = Number(r.qty.trim());
    if (!Number.isInteger(qty) || qty < 1 || qty > MAX_PACKAGE_QTY)
      return {
        packages,
        problem: `A package is 1 to ${MAX_PACKAGE_QTY} items.`,
      };
    if (seen.has(qty))
      return {
        packages,
        problem: `There are two packages of ${qty}: each number of items once.`,
      };
    seen.add(qty);
    const naira = nairaOf(r.price);
    if (naira === null || naira < 1)
      return {
        packages,
        problem: `Give the package of ${qty} a price, from ₦1.`,
      };
    const label = r.label.trim();
    if ([...label].length > MAX_LABEL || hasLink(label))
      return {
        packages,
        problem: `A package's words are up to ${MAX_LABEL} characters, with no links.`,
      };
    packages.push({
      qty,
      price_kobo: Math.round(naira * 100),
      ...(label ? { label } : {}),
    });
  }
  return { packages, problem: "" };
}

/** A package that costs more per item than the item's own price: said,
 * not refused (a bundle may carry a gift). Empty when it does not. */
export function dearerEach(
  p: Package,
  priceKobo: number | null,
  item: string,
): string {
  if (!priceKobo || p.qty < 1 || p.price_kobo <= priceKobo * p.qty) return "";
  const each = Math.ceil(p.price_kobo / p.qty);
  return `The ${p.qty}-item package costs ${formatPrice(each)} an item, more than ${item}'s own ${formatPrice(priceKobo)}. That is fine if it carries something extra.`;
}

/** The area a refusal says is off ("storefront", "orders"), if that is why. */
export function areaOffOf(e: unknown): string {
  if (
    e instanceof ApiError &&
    (e.status === 409 || e.status === 404) &&
    typeof e.body.area_off === "string"
  )
    return e.body.area_off;
  return "";
}

/** The area's name as Settings has it. */
export function areaName(area: string): string {
  return area === "orders"
    ? "Orders"
    : area === "storefront"
      ? "Storefront"
      : area;
}
