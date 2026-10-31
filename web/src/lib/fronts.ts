// Fronts (docs/spec/fronts.md F3): the storefront's pages of things and
// actions, as GET /v1/storefront/fronts gives them.
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
