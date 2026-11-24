import { describe, expect, it } from "vitest";
import {
  moved,
  photoCount,
  photoSrc,
  profileLetter,
  stepTo,
} from "./businessProfile";

describe("business profile", () => {
  it("shows a photo through the API, the small size by default", () => {
    const p = {
      id: "1",
      width: 1,
      height: 1,
      moderation: "unreviewed" as const,
      urls: { "320": "https://x/a.jpg", "800": "https://x/b.jpg" },
    };
    expect(photoSrc(p)).toBe(
      "/v1/business-profile/image?src=" + encodeURIComponent("https://x/a.jpg"),
    );
    expect(photoSrc(p, "800")).toBe(
      "/v1/business-profile/image?src=" + encodeURIComponent("https://x/b.jpg"),
    );
    expect(photoSrc(null)).toBeUndefined();
  });
  it("puts a letter where a logo would be", () => {
    expect(profileLetter("  dangote")).toBe("D");
    expect(profileLetter("")).toBe("?");
  });
  it("counts photos", () => {
    expect(photoCount(3, 12)).toBe("3 of 12 photos");
  });
  it("steps within a list and not past its ends", () => {
    expect(stepTo(0, -1, 3)).toBe(-1);
    expect(stepTo(2, 1, 3)).toBe(-1);
    expect(stepTo(1, 1, 3)).toBe(2);
  });
  it("moves one place", () => {
    expect(moved(["a", "b", "c"], 1, -1)).toEqual(["b", "a", "c"]);
    expect(moved(["a", "b", "c"], 1, 1)).toEqual(["a", "c", "b"]);
    expect(moved(["a", "b", "c"], 0, -1)).toEqual(["a", "b", "c"]);
  });
});
