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
  state: LinkState | "none";
}

export interface ExploreResult {
  results: ExploreCard[];
  near: ExploreCard[];
  fresh: ExploreCard[];
  more: boolean;
  blind?: BlindOffer;
}

/** A photo a business shows, through the API (the app's content policy allows images from its own origin only). */
export const explorePhoto = (
  business: string,
  n: number,
  size: "320" | "800" = "800",
) => `/v1/explore/${business}/photos/${n}?size=${size}`;

/** A business's logo through the API. */
export const exploreLogo = (business: string) =>
  `/v1/explore/${business}/logo?size=320`;

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

/** "Cement and sand · Ikeja": the line under a name. */
export function cardLine(c: Pick<ExploreCard, "tagline" | "area">): string {
  return [c.tagline, c.area].filter(Boolean).join(" · ");
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
