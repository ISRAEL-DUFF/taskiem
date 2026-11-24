import { describe, expect, it, vi } from "vitest";
import {
  chipOrder,
  forgetViewed,
  popularLine,
  recentlyViewed,
  rememberViewed,
  replaceViewed,
  type ExploreCard,
  cardLine,
  cardOffer,
  exploreLogo,
  explorePhoto,
  slideAt,
  slideNext,
} from "./explore";

describe("explore", () => {
  it("offers what the link allows", () => {
    expect(cardOffer("none")).toBe("connect");
    expect(cardOffer("ended")).toBe("connect");
    expect(cardOffer("not_accepted")).toBe("connect");
    expect(cardOffer("requested")).toBe("requested");
    expect(cardOffer("active")).toBe("message");
    expect(cardOffer("asked_us")).toBe("asked");
    expect(cardOffer("blocked")).toBeNull();
  });
  it("says the line under a name", () => {
    expect(cardLine({ tagline: "Cement and sand", area: "Ikeja" })).toBe(
      "Cement and sand · Ikeja",
    );
    expect(cardLine({ area: "Ikeja" })).toBe("Ikeja");
    expect(cardLine({})).toBe("");
  });
  it("names a photo by its place", () => {
    expect(explorePhoto("b1", 2)).toBe("/v1/explore/b1/photos/2?size=800");
    expect(explorePhoto("b1", 2, "ab12")).toBe(
      "/v1/explore/b1/photos/2?size=800&v=ab12",
    );
    expect(exploreLogo("b1", "ab12")).toBe(
      "/v1/explore/b1/logo?size=320&v=ab12",
    );
  });
  it("finds the slide a scroll stopped at, within the photos", () => {
    expect(slideAt(0, 300, 4)).toBe(0);
    expect(slideAt(310, 300, 4)).toBe(1);
    expect(slideAt(5000, 300, 4)).toBe(3);
    expect(slideAt(10, 0, 4)).toBe(0);
  });
  it("steps one slide and stops at the ends", () => {
    expect(slideNext(0, -1, 4)).toBe(0);
    expect(slideNext(3, 1, 4)).toBe(3);
    expect(slideNext(1, 1, 4)).toBe(2);
  });

  it("says Popular's count, never under three", () => {
    expect(popularLine({ links: 2 })).toBeUndefined();
    expect(popularLine({})).toBeUndefined();
    expect(popularLine({ links: 3 })).toBe("Linked with 3+ businesses");
    expect(popularLine({ links: 20 })).toBe("Linked with 20+ businesses");
  });

  it("puts the categories with businesses first, in the list's order", () => {
    const c = (key: string, n: number) => ({ key, name: key, n });
    expect(chipOrder([c("a", 0), c("b", 2), c("c", 0), c("d", 1)]).map((x) => x.key)).toEqual(["b", "d", "a", "c"]);
  });

  it("keeps the last ten viewed, newest first, for the business signed in only", () => {
    const store = new Map<string, string>();
    vi.stubGlobal("localStorage", {
      getItem: (k: string) => store.get(k) ?? null,
      setItem: (k: string, v: string) => void store.set(k, v),
      removeItem: (k: string) => void store.delete(k),
    });
    const card = (n: number): ExploreCard => ({ business: `b${n}`, name: `B${n}`, logo: false, photos: 0, state: "none" });
    for (let n = 0; n < 12; n++) rememberViewed("t1", card(n));
    rememberViewed("t1", card(5));
    const kept = recentlyViewed("t1").map((c) => c.business);
    expect(kept).toHaveLength(10);
    expect(kept[0]).toBe("b5");
    expect(kept.filter((b) => b === "b5")).toHaveLength(1);
    expect(recentlyViewed("t2")).toEqual([]);
    expect(recentlyViewed(null)).toEqual([]);
    // Checked with the server: only those still shown, as they are now.
    replaceViewed("t1", [{ ...card(5), state: "active" }]);
    expect(recentlyViewed("t1").map((c) => [c.business, c.state])).toEqual([["b5", "active"]]);
    forgetViewed();
    expect(recentlyViewed("t1")).toEqual([]);
    vi.unstubAllGlobals();
  });
});
