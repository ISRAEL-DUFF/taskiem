import { describe, expect, it } from "vitest";
import { amountKobo, matchThing, mightBeRequest, parseRequest } from "./intents";

const products = [
  { id: "p1", name: "Rice, 50kg bag" },
  { id: "p2", name: "Rice, 25kg bag" },
  { id: "p3", name: "Palm oil, 25L" },
];
const orders = [{ id: "o1", reference: "AD-7Q4K2P", buyer: "Rivers Foods" }];

describe("typed requests", () => {
  it("reads amounts", () => {
    expect(amountKobo("80k")).toBe(8_000_000);
    expect(amountKobo("₦80,000")).toBe(8_000_000);
    expect(amountKobo("1.5m")).toBe(150_000_000);
    expect(amountKobo("eighty")).toBeNaN();
  });
  it("finds the thing, never guessing between two", () => {
    expect(matchThing("rice 50kg", products)?.id).toBe("p1");
    expect(matchThing("palm oil", products)?.id).toBe("p3");
    expect(matchThing("rice", products)).toBeUndefined();
    expect(matchThing("beans", products)).toBeUndefined();
  });
  it("offers a price change", () => {
    expect(parseRequest("change the price of rice 50kg to 80k", products, [])).toEqual({
      tool: "products",
      rowId: "p1",
      prefill: { priceKobo: 8_000_000 },
      words: "Change the price of Rice, 50kg bag to ₦80,000",
    });
    expect(parseRequest("palm oil price to ₦65,000", products, [])?.rowId).toBe("p3");
    expect(parseRequest("change the price of rice to 80k", products, [])).toBeNull();
  });
  it("offers stock in", () => {
    expect(parseRequest("20 bags of rice 25kg came in", products, [])).toMatchObject({ tool: "stock", rowId: "p2", prefill: { count: 20 } });
    expect(parseRequest("received 6 palm oil", products, [])).toMatchObject({ tool: "stock", rowId: "p3", prefill: { count: 6 } });
  });
  it("offers a new service", () => {
    expect(parseRequest("add a service: rice milling, 30 min, 2500", products, [])).toEqual({
      tool: "services",
      add: true,
      prefill: { name: "rice milling", minutes: 30, priceKobo: 250_000 },
      words: "Add the service rice milling, 30 min, ₦2,500",
    });
    expect(parseRequest("new service braids 2 hours", products, [])?.prefill).toEqual({ name: "braids", minutes: 120 });
  });
  it("offers an order to accept", () => {
    expect(parseRequest("accept order ad-7q4k2p", products, orders)).toMatchObject({ tool: "orders", rowId: "o1" });
    expect(parseRequest("accept order AD-0000AA", products, orders)).toBeNull();
  });
  it("leaves ordinary messages alone", () => {
    expect(parseRequest("how much is rice 50kg?", products, orders)).toBeNull();
    expect(mightBeRequest("Morning! How are you?")).toBe(false);
    expect(mightBeRequest("change the price of rice to 80k")).toBe(true);
  });
});
