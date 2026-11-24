import { describe, expect, it } from "vitest";
import { isGoodMoment, isNews } from "./moments";

const card = (event: string) => ({ doc: "order" as const, event, reference: "AD-1" });

describe("the chat's moments", () => {
  it("knows news from the other side", () => {
    expect(isNews(card("ordered"), false)).toBe(true);
    expect(isNews(card("declared"), false)).toBe(true);
    expect(isNews(card("ordered"), true)).toBe(false);
    expect(isNews(card("dispatched"), false)).toBe(false);
  });
  it("cheers a payment and the day's first order only", () => {
    expect(isGoodMoment(card("declared"), false, false)).toBe(true);
    expect(isGoodMoment(card("ordered"), false, true)).toBe(true);
    expect(isGoodMoment(card("ordered"), false, false)).toBe(false);
    expect(isGoodMoment(card("declared"), true, true)).toBe(false);
  });
});
