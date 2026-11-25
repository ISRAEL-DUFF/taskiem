// What search everywhere finds on the server besides the refs
// (docs/spec/menus-everywhere.md §6, g-search-saved): the business's
// services, and saved things, the person's own and the business's, which
// come by their name, kind and whose they are only, never what they hold.
// Pure, tested.

import { naira } from "./invoices";
import type { Suggestion } from "./mentions";
import type { ChatRef } from "./refs";
import { KIND_WORDS, type SavedKind } from "./saved";

/** One thing GET /v1/chat-refs answered: a ref, a service or a saved thing. */
export type Found = Omit<ChatRef, "kind"> & {
  kind: ChatRef["kind"] | "service" | "saved";
  minutes?: number;
  saved_kind?: SavedKind | "custom";
  kind_name?: string;
  owner?: "mine" | "business";
};

/** A service's row: its price and how long it takes. */
export const serviceSub = (r: Pick<Found, "total_kobo" | "minutes">) =>
  [r.total_kobo !== undefined ? naira(r.total_kobo) : "Price on the day", r.minutes ? `${r.minutes} min` : ""].filter(Boolean).join(" · ");

/** A saved thing's row: its kind and whose it is, nothing it holds. */
export function savedSub(r: Pick<Found, "saved_kind" | "kind_name" | "owner">): string {
  const kind =
    r.saved_kind === "custom"
      ? r.kind_name
        ? `a ${r.kind_name.toLowerCase()}`
        : "one of your own kinds"
      : (KIND_WORDS[r.saved_kind as SavedKind]?.one ?? "a saved thing");
  return `${kind.charAt(0).toUpperCase()}${kind.slice(1)} · ${r.owner === "mine" ? "Yours" : "The business's"}`;
}

/** A service or a saved thing as a row of the search; undefined for a ref. */
export function foundSuggestion(r: Found): Suggestion | undefined {
  if (r.kind === "service") return { kind: "service", id: r.id, name: r.name, sub: serviceSub(r) };
  if (r.kind === "saved")
    return { kind: "saved", id: r.id, name: `@${r.name}`, sub: savedSub(r), savedKind: r.saved_kind, savedOwner: r.owner };
  return undefined;
}
