// A business's profile and gallery (GET /v1/business-profile): what it says
// about itself to the businesses that find it. The limits match
// engine/links/profile.go.

export const MAX_NAME = 80;
export const MAX_TAGLINE = 140;
export const MAX_AREA = 80;
export const MAX_ABOUT = 1000;
export const MAX_CAPTION = 120;
export const MAX_VEHICLE = 40;
export const MAX_DELIVERY_AREAS = 20;

export interface Photo {
  id: string;
  width: number;
  height: number;
  urls: Record<string, string>;
  moderation: "unreviewed" | "approved" | "rejected";
}

export interface GalleryPhoto {
  id: string;
  caption: string;
  photo: Photo;
}

export interface BusinessProfile {
  name: string;
  tagline: string;
  area: string;
  about: string;
  logo: Photo | null;
  gallery: GalleryPhoto[];
  max_gallery: number;
  /** It delivers for other businesses (a rider): with what, and where. */
  delivers: boolean;
  vehicle: string;
  delivery_areas: string[];
}

/** The words of the profile the owner edits (PUT /v1/business-profile). */
export interface ProfileWords {
  name: string;
  tagline: string;
  area: string;
  about: string;
  delivers: boolean;
  vehicle: string;
  delivery_areas: string[];
}

/** The delivery areas typed as one line, "Lekki, Ajah, VI", as a list. */
export function splitAreas(line: string): string[] {
  const out: string[] = [];
  for (const raw of line.split(/[,\n;]/)) {
    const a = raw.trim();
    if (a && !out.some((x) => x.toLowerCase() === a.toLowerCase()))
      out.push(a);
  }
  return out.slice(0, MAX_DELIVERY_AREAS);
}

/** Whether two lists of areas say the same thing. */
export function sameAreas(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

/** A photo's picture through the API (the app's content policy allows images from its own origin only). */
export function photoSrc(
  p: Photo | null | undefined,
  size: "320" | "800" = "320",
): string | undefined {
  const u =
    p?.urls[size] ?? p?.urls["800"] ?? p?.urls["320"] ?? p?.urls["2000"];
  return u
    ? `/v1/business-profile/image?src=${encodeURIComponent(u)}`
    : undefined;
}

/** The letter that stands where a logo would be. */
export function profileLetter(name: string): string {
  const c = name.trim().charAt(0);
  return c ? c.toUpperCase() : "?";
}

/** "3 of 12 photos". */
export function photoCount(n: number, max: number): string {
  return `${n} of ${max} photo${max === 1 ? "" : "s"}`;
}

/** The place one step earlier or later in a list, or -1 when there is none. */
export function stepTo(index: number, by: -1 | 1, length: number): number {
  const to = index + by;
  return to < 0 || to >= length ? -1 : to;
}

/** The ids in the order after moving the one at `index` by one place. */
export function moved<T>(items: T[], index: number, by: -1 | 1): T[] {
  const to = stepTo(index, by, items.length);
  if (to < 0) return items;
  const out = items.slice();
  const [it] = out.splice(index, 1);
  if (it !== undefined) out.splice(to, 0, it);
  return out;
}
