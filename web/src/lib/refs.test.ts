import { describe, expect, it } from "vitest";
import { customerRefs, invoiceRefs, knownRefs, linkRefSuggestions, orderRefs, productRefs, refParts } from "./refs";
import { naira, type CustomerInvoice } from "./invoices";
import { mentionQuery, insertMention } from "./mentions";
import type { StockItem } from "./stock";

describe("/ references", () => {
  it("reads /invoice, /product, /order and /customer with the words after", () => {
    expect(mentionQuery("/invoice ada", 12)).toEqual({ mark: "/", query: "ada", start: 0, kind: "invoice" });
    expect(mentionQuery("see /invoices/INV-00", 20)).toEqual({ mark: "/", query: "INV-00", start: 4, kind: "invoice" });
    expect(mentionQuery("/product ", 9)).toEqual({ mark: "/", query: "", start: 0, kind: "product" });
    expect(mentionQuery("/order AK", 9)?.kind).toBe("order");
    expect(mentionQuery("/customer bisi ok", 17)).toEqual({ mark: "/", query: "bisi ok", start: 0, kind: "customer" });
    // Not a reference: a tool's name alone, a fraction, a word not offered.
    expect(mentionQuery("/invoice", 8)).toBeNull();
    expect(mentionQuery("1/2 bags", 8)).toBeNull();
    expect(mentionQuery("/stock rice", 11)).toBeNull();
    // "@" after a reference is its own.
    expect(mentionQuery("/invoice INV-0001 @Da", 21)?.mark).toBe("@");
  });
  it("writes the thing picked as # writes an item", () => {
    const q = mentionQuery("Did /invoice ad", 15);
    expect(q && insertMention("Did /invoice ad", q, 15, "INV-0001")).toEqual({ text: "Did #INV-0001 ", caret: 14 });
  });
  it("offers invoices, orders, customers and products", () => {
    const inv = { id: "i1", number: "INV-0001", status: "sent", total_kobo: 4500000, customer: { name: "B•••" } } as CustomerInvoice;
    expect(invoiceRefs([inv, { ...inv, id: "i2", status: "draft" }])).toEqual([
      { kind: "invoice", id: "i1", name: "INV-0001", sub: `B••• · ${naira(4500000)} · sent`, words: "B•••" },
    ]);
    expect(orderRefs([{ id: "o1", reference: "AK-7QX4KD", total_kobo: 900000, status: "paid", customer: { name: "A•••" } }])[0]?.sub).toBe(
      `A••• · ${naira(900000)} · paid`,
    );
    expect(customerRefs([{ id: "c1", name: "Bisi" }, { id: "c2", name: "" }])).toHaveLength(1);
    const items = [
      { id: "s1", name: "Rice", price_kobo: 7800000, unit: "bag" },
      { id: "s2", name: "Rice 50kg", variant_of: "s1" },
    ] as StockItem[];
    expect(productRefs(items)).toEqual([
      { kind: "item", id: "own:s1", name: "Rice", sub: `Yours · ${naira(7800000)} a bag`, category: undefined, words: undefined },
    ]);
  });
  it("says whose a shared invoice or order is", () => {
    const got = linkRefSuggestions([
      { kind: "invoice", id: "i1", name: "INV-0001", total_kobo: 100, status: "sent", side: "ours" },
      { kind: "invoice", id: "b1", name: "INV-0420", total_kobo: 100, status: "open", side: "theirs" },
      { kind: "order", id: "p1", name: "P-ABC123", total_kobo: 100, status: "confirmed", side: "theirs" },
    ]);
    expect(got.invoice.map((s) => s.sub?.split(" · ")[0])).toEqual(["Your invoice", "Their invoice"]);
    expect(got.order[0]?.sub).toBe(`Your order with them · ${naira(100)} · confirmed`);
  });
});

describe("references in the Taskiem chat's words", () => {
  const known = knownRefs({
    invoice: [{ kind: "invoice", id: "i1", name: "INV-0001" }],
    product: [
      { kind: "item", id: "own:s1", name: "Rice" },
      { kind: "item", id: "own:s2", name: "Rice 50kg" },
    ],
  });
  it("marks each #name known, the longest first, at a word's end", () => {
    expect(refParts("Send #INV-0001 and #rice 50kg, #Rice.", known)).toEqual([
      { text: "Send " },
      { text: "#INV-0001", ref: { kind: "invoice", id: "i1", name: "INV-0001" } },
      { text: " and " },
      { text: "#rice 50kg", ref: { kind: "product", id: "s2", name: "Rice 50kg" } },
      { text: ", " },
      { text: "#Rice", ref: { kind: "product", id: "s1", name: "Rice" } },
      { text: "." },
    ]);
    expect(refParts("#INV-00012", known)).toEqual([{ text: "#INV-00012" }]);
    expect(refParts("no tags", known)).toEqual([{ text: "no tags" }]);
  });
});
