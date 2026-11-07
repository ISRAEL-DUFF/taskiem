import { describe, expect, it } from "vitest";
import {
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
});
