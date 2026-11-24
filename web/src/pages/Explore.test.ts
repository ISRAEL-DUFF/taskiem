import { describe, expect, it } from "vitest";
import type { ExploreCard, ExploreResult } from "../lib/explore";
import { firstPicks } from "./Explore";

const card = (business: string, state: ExploreCard["state"] = "none"): ExploreCard => ({ business, name: business, logo: false, photos: 0, state });
const empty: ExploreResult = { results: [], near: [], near_more: false, fresh: [], more: false, delivery: [], delivery_more: false };

describe("firstPicks (§2.3)", () => {
  it("offers Featured first, then near us or across Nigeria, then delivery and the newest, each once, only those that can be asked", () => {
    const r: ExploreResult = {
      ...empty,
      featured: [card("f1"), card("n1")],
      near: [card("n1"), card("n2", "active")],
      across: [card("a1")],
      fresh: [card("x1")],
    };
    expect(firstPicks(r).map((c) => c.business)).toEqual(["f1", "n1", "a1"]);
    expect(firstPicks(empty)).toEqual([]);
  });
});
