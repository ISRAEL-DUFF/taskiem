import { describe, expect, it } from "vitest";
import { fitWithin, linkProblem } from "./photos";

describe("product photos", () => {
  it("sends a large photo at 1600 on its longest side, its shape kept", () => {
    expect(fitWithin(4032, 3024)).toEqual({ w: 1600, h: 1200 });
    expect(fitWithin(3024, 4032)).toEqual({ w: 1200, h: 1600 });
    expect(fitWithin(800, 600)).toEqual({ w: 800, h: 600 });
    expect(fitWithin(1600, 10)).toEqual({ w: 1600, h: 10 });
  });
  it("checks a pasted link before it is sent", () => {
    expect(linkProblem("")).toBe("Paste the link of a picture.");
    expect(linkProblem("shoe.jpg")).toMatch(/starts with https/);
    expect(linkProblem("ftp://x.test/a.jpg")).toMatch(/starts with https/);
    expect(linkProblem(" https://shop.test/shoe.jpg ")).toBeNull();
  });
});
