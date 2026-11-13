import type { BlindOffer, BusinessLink, LinkState } from "./links";

// Explore (GET /v1/explore): finding a business by browsing. The limits and
// the paths match api/explore.go.

export interface ExploreCard {
  business: string;
  name: string;
  slug?: string;
  area?: string;
  tagline?: string;
  about?: string;
  logo: boolean;
  /** How many photos it shows, at most eight. */
  photos: number;
  /** Its photos' words, in order ("" for none). */
  captions?: string[];
  state: LinkState | "none";
  /** Changes when its logo or photos do (keys the pictures' cache). */
  v?: string;
  /** It delivers for other businesses (a rider): with what, and where. */
  delivers?: boolean;
  vehicle?: string;
  delivery_areas?: string[];
  /** A delivery business's state, when a business uses it as a rider. */
  available?: "free" | "busy" | "off";
  /** The one or two categories it picked (keys of CATEGORY_NAMES). */
  categories?: string[];
  /** Popular only: its links made in the last 90 days, rounded down (3, 5, then tens); never under 3. */
  links?: number;
}

/** A category with how many businesses we may find in it. */
export interface ExploreCategory {
  key: string;
  name: string;
  n: number;
}

export interface ExploreResult {
  results: ExploreCard[];
  near: ExploreCard[];
  near_more: boolean;
  fresh: ExploreCard[];
  more: boolean;
  /** Businesses that deliver for others, those covering our area first. */
  delivery: ExploreCard[];
  delivery_more: boolean;
  /** Our own area, as near you reads it. */
  area?: string;
  blind?: BlindOffer;
  /** Across Nigeria: in Near you's place when no one in our area can be shown, or our area is not set. */
  across?: ExploreCard[];
  across_more?: boolean;
  /** At most six: Taskiem's picks, then businesses Trusted on Taskiem. */
  featured?: ExploreCard[];
  trusted?: ExploreCard[];
  trusted_more?: boolean;
  popular?: ExploreCard[];
  popular_more?: boolean;
  categories?: ExploreCategory[];
  /** section=category: one category's businesses. */
  in_category?: ExploreCard[];
  category_more?: boolean;
}

/** The categories a business picks (engine/links/categories.go), by key. */
export const CATEGORY_NAMES: Record<string, string> = {
  food: "Food and drinks",
  building: "Building materials",
  fashion: "Fashion",
  phones: "Phones and gadgets",
  electrical: "Electrical and electronics",
  beauty: "Beauty and personal care",
  farm: "Farm produce",
  health: "Health and pharmacy",
  home: "Home and furniture",
  auto: "Cars and spare parts",
  logistics: "Delivery and logistics",
  wholesale: "Wholesale and distribution",
  services: "Services",
};

/** How many a business picks at most. */
export const MAX_CATEGORIES = 2;

/** Popular's count: "Linked with 3+ businesses", "Linked with 20+ businesses" (sent rounded down: 3, 5, then tens). */
export function popularLine(c: Pick<ExploreCard, "links">): string | undefined {
  const n = c.links ?? 0;
  if (n < 3) return undefined;
  return `Linked with ${n}+ businesses`;
}

/** The categories with businesses in them first, each group in the list's order. */
export function chipOrder(cats: ExploreCategory[]): ExploreCategory[] {
  return [...cats.filter((c) => c.n > 0), ...cats.filter((c) => c.n === 0)];
}

// Recently viewed (docs/spec/mobile-app-explore-and-offline.md §2.1): the
// last ten businesses looked at, in this browser only, for the business
// signed in; never sent to the server, forgotten on signing out.
const RECENT = "taskiem-explore-recent";
export const RECENT_LIMIT = 10;

export function recentlyViewed(owner: string | null | undefined): ExploreCard[] {
  if (!owner) return [];
  try {
    const kept = JSON.parse(localStorage.getItem(RECENT) ?? "null") as { owner?: string; cards?: ExploreCard[] } | null;
    return kept?.owner === owner && Array.isArray(kept.cards) ? kept.cards : [];
  } catch {
    return [];
  }
}

/** A business looked at: first in the list, the list kept to ten. */
export function rememberViewed(owner: string | null | undefined, card: ExploreCard): ExploreCard[] {
  const now = [card, ...recentlyViewed(owner).filter((c) => c.business !== card.business)].slice(0, RECENT_LIMIT);
  if (!owner) return now;
  try {
    localStorage.setItem(RECENT, JSON.stringify({ owner, cards: now }));
  } catch {
    // Storage refused (a private window): kept for this page only.
  }
  return now;
}

export function forgetViewed(): void {
  try {
    localStorage.removeItem(RECENT);
  } catch {
    // Nothing kept.
  }
}

/** A photo a business shows, through the API (the app's content policy allows images from its own origin only). `v` is the card's version: a changed picture is asked for again. */
export const explorePhoto = (
  business: string,
  n: number,
  v?: string,
  size: "320" | "800" = "800",
) => `/v1/explore/${business}/photos/${n}?size=${size}${v ? `&v=${v}` : ""}`;

/** A business's logo through the API. */
export const exploreLogo = (business: string, v?: string) =>
  `/v1/explore/${business}/logo?size=320${v ? `&v=${v}` : ""}`;

/** What a card offers, from our link with the business. */
export type CardAction = "connect" | "requested" | "message" | "asked" | null;

export function cardOffer(state: ExploreCard["state"]): CardAction {
  switch (state) {
    case "none":
    case "ended":
    case "not_accepted":
      return "connect";
    case "requested":
      return "requested";
    case "active":
      return "message";
    case "asked_us":
      return "asked";
    default:
      return null; // blocked by us: nothing to offer here
  }
}

/** "Cement and sand · Ikeja": the line under a name. A business that
 * delivers says so, with what and where, when it has no line of its own:
 * "Delivers by bike · Lekki, Ajah". */
export function cardLine(
  c: Pick<
    ExploreCard,
    "tagline" | "area" | "delivers" | "vehicle" | "delivery_areas" | "available"
  >,
): string {
  const line = [c.tagline, c.area].filter(Boolean).join(" · ");
  if (!c.delivers) return line;
  const how = c.vehicle ? `Delivers by ${c.vehicle}` : "Delivers";
  const where = (c.delivery_areas ?? []).slice(0, 3).join(", ");
  const now =
    c.available === "free"
      ? "Free now"
      : c.available === "busy"
        ? "Busy"
        : c.available === "off"
          ? "Off today"
          : "";
  return [c.tagline ? line : how, c.tagline ? how : where || c.area, now]
    .filter(Boolean)
    .join(" · ");
}

/** The pending request we sent a business, when there is one (the one Withdraw takes back). */
export function pendingTo(
  links: BusinessLink[],
  business: string,
): BusinessLink | undefined {
  return links.find(
    (l) => l.business === business && l.state === "requested" && l.asked_by_us,
  );
}

/** Which photo a track scrolled to shows: its offset over one slide's width. */
export function slideAt(
  scrollLeft: number,
  width: number,
  count: number,
): number {
  if (width <= 0 || count <= 0) return 0;
  return Math.min(count - 1, Math.max(0, Math.round(scrollLeft / width)));
}

/** The slide one step earlier or later, kept within the photos. */
export function slideNext(at: number, by: -1 | 1, count: number): number {
  return Math.min(count - 1, Math.max(0, at + by));
}
