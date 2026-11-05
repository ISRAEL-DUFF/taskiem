import { describe, expect, it } from "vitest";
import { HEIC_MESSAGE, isHeic, jpegName, preparePhoto } from "./photo";

const file = (name: string, type: string) => new File(["x"], name, { type });

describe("isHeic", () => {
  it("knows an iPhone photo by its type or, with none, its name", () => {
    expect(isHeic(file("a.heic", "image/heic"))).toBe(true);
    expect(isHeic(file("a.HEIF", "image/heif"))).toBe(true);
    expect(isHeic(file("IMG_1.HEIC", ""))).toBe(true);
    expect(isHeic(file("a.jpg", "image/jpeg"))).toBe(false);
    expect(isHeic(file("a.png", "image/png"))).toBe(false);
    expect(isHeic(file("notes.txt", ""))).toBe(false);
  });
});

describe("jpegName", () => {
  it("ends the name in .jpg", () => {
    expect(jpegName("IMG_0042.HEIC")).toBe("IMG_0042.jpg");
    expect(jpegName("a.b.heif")).toBe("a.b.jpg");
    expect(jpegName("noext")).toBe("noext.jpg");
  });
});

describe("preparePhoto", () => {
  it("leaves a photo the server takes as it is", async () => {
    const f = file("a.jpg", "image/jpeg");
    expect(await preparePhoto(f)).toBe(f);
  });
  it("says what to do when the browser cannot read HEIC", async () => {
    // These tests run without a browser: there is nothing to decode with.
    await expect(preparePhoto(file("a.heic", "image/heic"))).rejects.toThrow(
      HEIC_MESSAGE,
    );
  });
});
