// Discover (docs/spec/taskiem-for-everyone.md §10): the public finding
// businesses in the Taskiem app, on You, and a business's own listing in
// Settings > Discover. What the API answers (POST /v1/discover/search,
// GET /v1/discover/{business}, GET/PUT /v1/settings/discover) and the
// words the screens build from it. Pure helpers, tested on their own.
// Kept apart from Explore (businesses finding businesses): its own
// setting, its own list.

import { ApiError } from "../api";
import type { Hours } from "./storefront";

/** A photo's sizes, by width. */
export type PhotoSizes = Record<string, string>;

/** One business a search found. */
export interface DiscoverResult {
  business: string;
  name: string;
  photo: PhotoSizes;
  category: string;
  category_name: string;
  area: string;
  open_now: boolean;
  /** "Open now, closes at 18:00", "Closed now. Opens tomorrow at 09:00". */
  line: string;
  can_chat: boolean;
  km?: number;
}

export interface DiscoverPhoto {
  urls: PhotoSizes;
  caption?: string;
}

/** A business's profile in Discover. */
export interface DiscoverProfile {
  business: string;
  name: string;
  tagline?: string;
  category?: string;
  category_name: string;
  area?: string;
  address?: string;
  map_link?: string;
  photo?: DiscoverPhoto;
  photos: DiscoverPhoto[];
  hours: Hours | Record<string, unknown>;
  open_now: boolean;
  line: string;
  shop?: string;
  can_chat: boolean;
  has_shop: boolean;
  has_book: boolean;
  /** The person's own conversation with it, when they have one. */
  chat?: string;
}

export interface DiscoverCategory {
  key: string;
  name: string;
}

export interface DiscoverLocation {
  lat: number;
  lng: number;
}

/** A business's listing, as the public sees it. */
export interface DiscoverListing {
  business: string;
  name: string;
  tagline?: string;
  category?: string;
  area?: string;
  address?: string;
  map_link?: string;
  photo?: DiscoverPhoto;
  photos: DiscoverPhoto[];
  hours: Hours | Record<string, unknown>;
  location?: DiscoverLocation;
  shop?: string;
  can_chat: boolean;
  has_shop: boolean;
  has_book: boolean;
}

export type Missing = "category" | "area" | "photo" | "hours";

/** GET/PUT /v1/settings/discover. */
export interface DiscoverSettings {
  listed: boolean;
  chosen: boolean | null;
  default: boolean;
  workspace?: boolean;
  category?: string;
  area?: string;
  hours?: Hours;
  location?: DiscoverLocation;
  missing: Missing[];
  missing_line?: string;
  shown: boolean;
  hidden?: "removed" | "reported" | "suspended";
  removed_note?: string;
  chat_off?: "orders_off" | "chats_off";
  listing: DiscoverListing;
}

/** The search as the screen holds it. */
export interface DiscoverQuery {
  q: string;
  area: string;
  category: string;
}

/** The body of POST /v1/discover/search: only what was given. The place
 * travels here, in the body, never in an address. */
export function searchBody(s: DiscoverQuery, near: DiscoverLocation | null, offset: number): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  if (s.q.trim()) body.q = s.q.trim();
  if (s.area.trim()) body.area = s.area.trim();
  if (s.category) body.category = s.category;
  if (near) body.near = { lat: near.lat, lng: near.lng };
  if (offset > 0) body.offset = offset;
  return body;
}

/** A photo's address at about the width asked for (the smallest that is
 * at least that wide, else the largest). */
export function photoAt(sizes: PhotoSizes | undefined, width = 320): string | undefined {
  const all = Object.entries(sizes ?? {})
    .map(([w, url]) => [Number(w), url] as const)
    .filter(([w, url]) => Number.isFinite(w) && !!url)
    .sort((a, b) => a[0] - b[0]);
  if (all.length === 0) return Object.values(sizes ?? {}).find(Boolean);
  return (all.find(([w]) => w >= width) ?? all[all.length - 1])?.[1];
}

/** How far away: "800 m away", "2.4 km away", "15 km away". */
export function kmWords(km: number | undefined): string {
  if (km === undefined || !Number.isFinite(km) || km < 0) return "";
  if (km < 1) return `${Math.max(100, Math.round((km * 1000) / 100) * 100)} m away`;
  if (km < 10) return `${km.toFixed(1)} km away`;
  return `${Math.round(km)} km away`;
}

/** A result's line under its name: "Food and drinks · Yaba · 2.4 km away". */
export function resultLine(r: Pick<DiscoverResult, "category_name" | "area" | "km">): string {
  return [r.category_name, r.area, kmWords(r.km)].filter(Boolean).join(" · ");
}

/** "Open now" or "Closed now", for a result's badge. */
export const openNow = (open: boolean) => (open ? "Open now" : "Closed now");

/** How many were found: "1 business", "20 businesses". */
export function foundWords(n: number, more: boolean): string {
  if (n === 0) return "No businesses found";
  const words = n === 1 ? "1 business" : `${n} businesses`;
  return more ? `${words} so far` : words;
}

/** What the person searched, in words, for the empty list. */
export function nothingFound(s: DiscoverQuery, categoryName?: string, near?: boolean): string {
  const what = [s.q.trim() && `“${s.q.trim()}”`, categoryName, s.area.trim() && `in ${s.area.trim()}`, near && "near you"].filter(Boolean).join(" ");
  return what
    ? `No business found for ${what}. Try other words, or another area. Only businesses that chose to be found are listed.`
    : "No business is listed here yet. Only businesses that chose to be found are listed.";
}

/** A refused search or report, in a person's words. */
export function discoverProblem(e: unknown, what: "search" | "report" | "profile"): string | null {
  if (!(e instanceof ApiError)) return null;
  if (e.status === 429)
    return what === "report"
      ? "You have sent a lot of reports. Please wait a while before you send another."
      : "You are searching too fast. Wait a moment and try again.";
  if (e.status === 404 && what !== "search") return "This business is not in Discover any more.";
  if (e.status === 403) return "Discover is for people signed in to their own account.";
  return null;
}

/** Where the browser's place came from, refused or not available. */
export function placeProblem(code: number | undefined): string {
  if (code === 1) return "Your browser did not share where you are. You can still search by area.";
  if (code === 3) return "Finding where you are took too long. Try again, or search by area.";
  return "Your place could not be found. You can still search by area.";
}

/** A place on the map, as the server keeps it: three decimals (about 100 m). */
export function roundPlace(lat: number, lng: number): DiscoverLocation {
  const r = (n: number) => Math.round(n * 1000) / 1000;
  return { lat: r(lat), lng: r(lng) };
}

/** The reasons a person reports a business in Discover. */
export const DISCOVER_REPORT_REASONS: { key: "scam" | "impersonation" | "prohibited" | "offensive" | "other"; words: string }[] = [
  { key: "scam", words: "They are trying to cheat people" },
  { key: "impersonation", words: "They are pretending to be someone else" },
  { key: "prohibited", words: "They sell something not allowed" },
  { key: "offensive", words: "Rude or offensive" },
  { key: "other", words: "Something else" },
];

/** Where the chat with a business found in Discover opens: its own chat
 * when the person has one, else it is started (from: discover). A tool
 * (Their shop, Book) opens in it. */
export function chatPath(p: Pick<DiscoverProfile, "business" | "chat">, tool?: "shop" | "book"): { to: string; tool?: "shop" | "book" } {
  if (p.chat) return { to: `/you/chats/${encodeURIComponent(p.chat)}`, tool };
  const q = new URLSearchParams({ business: p.business, from: "discover" });
  if (tool) q.set("tool", tool);
  return { to: `/you/chats/new?${q.toString()}` };
}

// --- the business's own listing ---

/** The switch's line under it: the default, or the business's choice. */
export function listedWords(s: Pick<DiscoverSettings, "listed" | "chosen" | "default" | "workspace">): string {
  if (s.workspace) return "A workflows workspace is not listed in Discover.";
  if (s.chosen === null) return s.default ? "On by default, because your shop's page is published." : "Off until you turn it on.";
  if (s.chosen === s.default) return s.listed ? "On, as you chose." : "Off, as you chose.";
  return s.listed ? "On, as you chose. Without your choice it would be off, because your shop's page is not published." : "Off, as you chose. Without your choice it would be on, because your shop's page is published.";
}

const MISSING_WORDS: Record<Missing, string> = { category: "a category", area: "an area", photo: "a photo", hours: "opening hours" };

/** What the listing lacks, in words (the server's line, else ours). */
export function missingWords(s: Pick<DiscoverSettings, "missing" | "missing_line">): string {
  if (s.missing_line) return s.missing_line;
  if (s.missing.length === 0) return "";
  const parts = s.missing.map((m) => MISSING_WORDS[m]);
  const list = parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
  return `Customers can't find you in Discover until you add ${list}.`;
}

/** Why the listing is hidden, in words. */
export function hiddenWords(hidden: DiscoverSettings["hidden"], note?: string): string {
  switch (hidden) {
    case "removed":
      return `Taskiem took your listing out of Discover${note ? `: ${note.replace(/\.$/, "")}.` : "."} Customers can still reach you through your chat link.`;
    case "reported":
      return "Your listing is hidden while Taskiem looks at reports about it. It comes back if they find nothing wrong.";
    case "suspended":
      return "Your listing is hidden while your shop's page is suspended.";
    default:
      return "";
  }
}

/** Why the listing has no Chat button. */
export function chatOffWords(off: DiscoverSettings["chat_off"]): string {
  if (off === "orders_off") return "Your Orders area is off, so your listing has no Chat button. Customers see your hours and address only.";
  if (off === "chats_off") return "You are not taking chats, so your listing has no Chat button. Customers see your hours and address only.";
  return "";
}

/** Where the listing stands, in one line and a tone for the hero tag. */
export function standing(s: Pick<DiscoverSettings, "listed" | "shown" | "hidden" | "missing">): { tag: string; line: string } {
  if (!s.listed) return { tag: "Off", line: "Customers can't find you in Discover." };
  if (s.hidden) return { tag: "Hidden", line: "Your listing is hidden for now." };
  if (!s.shown || s.missing.length > 0) return { tag: "Not shown yet", line: "Your listing needs a little more before customers see it." };
  return { tag: "Listed", line: "Customers can find you in Discover." };
}

/** A map link for a place: opens the phone's map app. */
export const mapLink = (p: DiscoverLocation) => `https://www.google.com/maps/search/?api=1&query=${p.lat},${p.lng}`;
