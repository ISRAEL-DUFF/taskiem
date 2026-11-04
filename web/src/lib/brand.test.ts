import { describe, expect, it } from "vitest";
import { normalizeHex, requestOf, sameLook } from "./brand";

describe("normalizeHex", () => {
  it("reads the ways a colour is typed", () => {
    expect(normalizeHex("#0B6E4F")).toBe("#0b6e4f");
    expect(normalizeHex("0b6e4f")).toBe("#0b6e4f");
    expect(normalizeHex(" #abc ")).toBe("#aabbcc");
  });
  it("refuses what is not a colour", () => {
    for (const s of ["", "#12345", "red", "#gggggg", "url(x)"]) expect(normalizeHex(s)).toBeNull();
  });
});

describe("requestOf", () => {
  it("sends images by id, empty when there is none", () => {
    expect(requestOf({ theme: "bold", color: "#c2410c" })).toEqual({ theme: "bold", color: "#c2410c", logo: "", cover: "" });
    expect(sameLook(requestOf({ theme: "bold", color: "#c2410c", logo: "m1" }), requestOf({ theme: "bold", color: "#c2410c" }))).toBe(false);
  });
});
