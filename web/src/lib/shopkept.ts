// The shop's own memory in this browser (docs/spec/mobile-app-look.md, "The
// shop"; the app keeps the same in Prefs): the items saved with the heart,
// and the ones looked at lately, by business. The server keeps neither yet.
// Storage that cannot be read or written (a private window, blocked site
// data) leaves them for while the page is open.

import type { ShopItem, ShopSize } from "./personchat";

const SAVED = "taskiem-shop-saved";
const RECENT = "taskiem-shop-recent";
/** How many looked-at items are kept, newest first. */
export const RECENT_KEPT = 12;

type Lists = Record<string, string[]>;
const memory = new Map<string, Lists>();

function read(key: string): Lists {
  try {
    const raw = JSON.parse(localStorage.getItem(key) ?? "{}") as unknown;
    if (!raw || typeof raw !== "object" || Array.isArray(raw)) return memory.get(key) ?? {};
    const out: Lists = {};
    for (const [k, v] of Object.entries(raw)) if (Array.isArray(v)) out[k] = v.filter((x): x is string => typeof x === "string");
    return out;
  } catch {
    return memory.get(key) ?? {};
  }
}

function write(key: string, lists: Lists) {
  memory.set(key, lists);
  try {
    localStorage.setItem(key, JSON.stringify(lists));
  } catch {
    // Kept only while the page is open.
  }
}

/** The items of [business]'s shop saved with the heart, newest first. */
export function savedItems(business: string): string[] {
  return read(SAVED)[business] ?? [];
}

/** The heart on an item: saved, or no longer. Returns the saved list. */
export function toggleSaved(business: string, item: string): string[] {
  const all = read(SAVED);
  const now = all[business] ?? [];
  const next = now.includes(item) ? now.filter((x) => x !== item) : [item, ...now];
  write(SAVED, { ...all, [business]: next });
  return next;
}

/** The items of [business]'s shop looked at lately, newest first. */
export function recentItems(business: string): string[] {
  return read(RECENT)[business] ?? [];
}

/** An item looked at: first of those seen lately. Returns the list. */
export function viewedItem(business: string, item: string): string[] {
  const all = read(RECENT);
  const next = [item, ...(all[business] ?? []).filter((x) => x !== item)].slice(0, RECENT_KEPT);
  write(RECENT, { ...all, [business]: next });
  return next;
}

/** Signing out: what is the person's own goes. */
export function forgetShop() {
  memory.delete(SAVED);
  memory.delete(RECENT);
  try {
    localStorage.removeItem(SAVED);
    localStorage.removeItem(RECENT);
  } catch {
    // Nothing kept to remove.
  }
}

/** The lowest price of an item, its sizes counted. */
export function fromPrice(it: ShopItem): number | undefined {
  const prices = (it.sizes ?? []).map((z) => z.price_kobo ?? it.price_kobo).filter((p): p is number => p !== undefined);
  if (prices.length === 0) return it.price_kobo;
  return Math.min(...prices);
}

/** Whether its sizes cost more than one price ("From ₦3,000"). */
export function pricesDiffer(it: ShopItem): boolean {
  return new Set((it.sizes ?? []).map((z) => z.price_kobo ?? it.price_kobo)).size > 1;
}

/** Nothing of it can be ordered: it, or every one of its sizes, gone. */
export function soldOut(it: ShopItem): boolean {
  return !it.available || ((it.sizes?.length ?? 0) > 0 && (it.sizes ?? []).every((z: ShopSize) => !z.available));
}

/** The categories of a shop, in the order its items name them. */
export function categories(items: ShopItem[]): string[] {
  const out: string[] = [];
  for (const it of items) {
    const c = (it.category ?? "").trim();
    if (c && !out.includes(c)) out.push(c);
  }
  return out;
}

/** The items shown: by filter ("all", "saved" or "cat:<name>") and words. */
export function shownItems(items: ShopItem[], filter: string, words: string, saved: string[]): ShopItem[] {
  const q = words.trim().toLowerCase();
  return items.filter((it) => {
    if (filter === "saved" && !saved.includes(it.id)) return false;
    if (filter.startsWith("cat:") && (it.category ?? "").trim() !== filter.slice(4)) return false;
    if (!q) return true;
    return `${it.name} ${it.description ?? ""} ${it.category ?? ""}`.toLowerCase().includes(q);
  });
}
