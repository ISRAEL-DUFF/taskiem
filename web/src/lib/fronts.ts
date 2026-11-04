// Fronts (docs/spec/fronts.md F3): the storefront's pages of things and
// actions, as GET /v1/storefront/fronts gives them.
import type { Image } from "./storefront";
import type { StorefrontAction } from "./orders";

export interface FrontKind {
  name: string;
  title: string;
  line: string;
}

export interface Front {
  id: string;
  /** Its address, /<slug>; empty for the home page. */
  slug: string;
  kind: string;
  name: string;
  tagline?: string;
  position: number;
  enabled: boolean;
  actions: StorefrontAction[];
  /** The table its list shows, and how many items. */
  table_id: string | null;
  items: number;
}

export interface FrontsView {
  kinds: FrontKind[];
  fronts: Front[];
}

const plural = (n: number, one: string) => `${n} ${one}${n === 1 ? "" : "s"}`;

/** A front's line in the list: where it is, its kind, what it has. */
export function frontSub(f: Front, kinds: FrontKind[]): string {
  const kind = kinds.find((k) => k.name === f.kind)?.title ?? f.kind;
  const where = f.slug === "" ? "Your home page" : `/${f.slug}`;
  const has = [
    f.table_id ? plural(f.items, "item") : "",
    f.actions.length ? plural(f.actions.length, "form") : "",
  ].filter(Boolean);
  return [where, kind, ...has].join(" · ");
}

/** A front's public address on the published page. */
export const frontURL = (pageURL: string, f: Front) =>
  `${pageURL.replace(/\/+$/, "")}/${f.slug}`;

/** What is wrong with a new front's name, if anything. */
export function frontNameProblem(name: string): string {
  const n = name.trim();
  if (!n) return "Give the front a name.";
  if ([...n].length > 80) return "A name is up to 80 characters.";
  return "";
}

// --- a front's items, edited in place ---

/** One item on a front's list, as the table has it now. */
export interface FrontItem {
  id: string;
  name: string;
  price_kobo?: number;
  description?: string;
  category?: string;
  available: boolean;
  photo?: Image;
}

/** GET /v1/storefront/fronts/{id}/items: the items and which of the list's
 * columns exist (a list with no price takes none). */
export interface FrontItemsView {
  table_id: string;
  items: FrontItem[];
  takes: { price?: boolean; description?: boolean; category?: boolean; available?: boolean; photo?: boolean };
}

/** What the owner types for an item. */
export interface ItemForm {
  name: string;
  price: string;
  description: string;
  category: string;
}

export const emptyItem: ItemForm = { name: "", price: "", description: "", category: "" };

/** The form of an item, to be changed. */
export function itemForm(it: FrontItem): ItemForm {
  return {
    name: it.name,
    price: it.price_kobo === undefined ? "" : String(it.price_kobo / 100),
    description: it.description ?? "",
    category: it.category ?? "",
  };
}

/** A price typed in naira ("1,500" or "1500.50") as a number, or null for
 * what is not one. Empty is "no price": null too, for the caller to tell. */
export function nairaOf(raw: string): number | null {
  const s = raw.trim().replace(/^[₦N]\s*/i, "").replace(/,/g, "");
  if (!/^\d{1,9}(\.\d{1,2})?$/.test(s)) return null;
  return Number(s);
}

/** What is wrong with an item typed in, in plain words, or "". */
export function itemProblem(f: ItemForm, takes: FrontItemsView["takes"]): string {
  const name = f.name.trim();
  if (!name) return "Give the item a name.";
  if ([...name].length > 120) return "A name is up to 120 characters.";
  if (takes.price && f.price.trim() !== "" && nairaOf(f.price) === null) return "Type the price in naira, such as 1,500 or 1500.50.";
  if ([...f.description].length > 1000) return "A description is up to 1,000 characters.";
  if ([...f.category.trim()].length > 60) return "A category is up to 60 characters.";
  return "";
}

/** The request for an item typed in: only what the list takes. For a
 * change, `was` leaves out what did not change. */
export function itemRequest(f: ItemForm, takes: FrontItemsView["takes"], was?: ItemForm): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  const changed = (k: keyof ItemForm) => !was || was[k].trim() !== f[k].trim();
  if (changed("name")) out.name = f.name.trim();
  if (takes.price && changed("price") && f.price.trim() !== "") out.price_naira = nairaOf(f.price);
  if (takes.description && changed("description") && (was || f.description.trim() !== "")) out.description = f.description.trim();
  if (takes.category && changed("category") && (was || f.category.trim() !== "")) out.category = f.category.trim();
  return out;
}

/** The front's order form when customers cannot use it yet: not switched
 * on, or with no workflow behind it. Nothing to turn on when it is ready, or
 * when the front takes no orders. */
export function orderToTurnOn(f: Pick<Front, "actions">): StorefrontAction | undefined {
  return f.actions.find((a) => a.kind === "order" && !a.builtin && !(a.enabled && a.workflow_id));
}
