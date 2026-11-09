import { describe, expect, it } from "vitest";
import {
  cardPill,
  cardRefs,
  docMentions,
  knownRefs,
  paidShare,
  productRefs,
  refParts,
  refSources,
  refSuggestion,
  shortDate,
  type ChatRef,
} from "./refs";
import { naira } from "./invoices";
import { insertMention, mentionQuery } from "./mentions";
import type { StockItem } from "./stock";

const now = new Date("2026-11-09T12:00:00Z");
const ref = (o: Partial<ChatRef>): ChatRef => ({ kind: "invoice", id: "i1", name: "INV-0001", party: "Ada's Kitchen", when: "2026-11-03T10:00:00Z", total_kobo: 4_500_000, status: "overdue", ...o });

describe("/ references", () => {
  it("reads each word with the words after", () => {
    expect(mentionQuery("/invoice ada", 12)).toEqual({ mark: "/", query: "ada", start: 0, kind: "invoice" });
    expect(mentionQuery("see /invoices/INV-00", 20)).toEqual({ mark: "/", query: "INV-00", start: 4, kind: "invoice" });
    expect(mentionQuery("/bill dang", 10)?.kind).toBe("bill");
    expect(mentionQuery("/purchase P-", 12)?.kind).toBe("purchase");
    expect(mentionQuery("/product ", 9)).toEqual({ mark: "/", query: "", start: 0, kind: "product" });
    expect(mentionQuery("/customer bisi ok", 17)).toEqual({ mark: "/", query: "bisi ok", start: 0, kind: "customer" });
    // Not a reference: a tool's name alone, a fraction, a word not offered.
    expect(mentionQuery("/invoice", 8)).toBeNull();
    expect(mentionQuery("1/2 bags", 8)).toBeNull();
    expect(mentionQuery("/stock rice", 11)).toBeNull();
    expect(mentionQuery("/invoice INV-0001 @Da", 21)?.mark).toBe("@");
  });
  it("writes the thing picked as # writes an item", () => {
    const q = mentionQuery("Did /invoice ad", 15);
    expect(q && insertMention("Did /invoice ad", q, 15, "INV-0001")).toEqual({ text: "Did #INV-0001 ", caret: 14 });
  });
  it("says who, when, how much and where it stands on each row", () => {
    expect(refSuggestion(ref({}), now)).toEqual({
      kind: "invoice", id: "i1", name: "INV-0001", badge: "Invoice", words: "Ada's Kitchen",
      sub: `Ada's Kitchen · 3 Nov · ${naira(4_500_000)} · overdue`,
    });
    expect(refSuggestion(ref({ kind: "customer", name: "Bisi Okafor", party: "•••0555", total_kobo: undefined, status: undefined }), now).sub).toBe("•••0555 · last wrote 3 Nov");
    expect(shortDate("2025-12-31T10:00:00Z", now)).toBe("31 Dec 2025");
  });
  it("gives /invoice invoices and bills, /order orders and purchases", () => {
    const src = refSources([ref({}), ref({ kind: "bill", id: "b1", name: "INV-0420" }), ref({ kind: "order", id: "o1" }), ref({ kind: "purchase", id: "p1" }), ref({ kind: "customer", id: "c1" })], now);
    expect(src.invoice.map((s) => s.id)).toEqual(["i1", "b1"]);
    expect(src.bill.map((s) => s.id)).toEqual(["b1"]);
    expect(src.order.map((s) => s.id)).toEqual(["o1", "p1"]);
    expect(src.purchase.map((s) => s.badge)).toEqual(["Your order"]);
    expect(src.customer.map((s) => s.id)).toEqual(["c1"]);
  });
  it("offers the business's own products, not their sizes", () => {
    const items = [
      { id: "s1", name: "Rice", price_kobo: 7800000, unit: "bag" },
      { id: "s2", name: "Rice 50kg", variant_of: "s1" },
    ] as StockItem[];
    expect(productRefs(items)).toEqual([
      { kind: "item", id: "own:s1", name: "Rice", sub: `Yours · ${naira(7800000)} a bag`, category: undefined, words: undefined },
    ]);
  });
});

describe("references in messages", () => {
  const known = knownRefs([ref({}), ref({ kind: "customer", id: "c1", name: "Bisi Okafor" })], [
    { kind: "item", id: "own:s1", name: "Rice" },
    { kind: "item", id: "own:s2", name: "Rice 50kg" },
  ]);
  it("marks each #name known, the longest first, at a word's end", () => {
    expect(refParts("Send #INV-0001 and #rice 50kg, #Rice.", known).filter((p) => p.ref).map((p) => [p.text, p.ref?.kind, p.ref?.id])).toEqual([
      ["#INV-0001", "invoice", "i1"],
      ["#rice 50kg", "product", "s2"],
      ["#Rice", "product", "s1"],
    ]);
    expect(refParts("#INV-00012", known)).toEqual([{ text: "#INV-00012" }]);
    expect(refParts("no tags", known)).toEqual([{ text: "no tags" }]);
  });
  it("cards each invoice, order or customer once, not products", () => {
    expect(cardRefs("#INV-0001 for #Bisi Okafor, again #INV-0001 and #Rice", known).map((r) => r.kind)).toEqual(["invoice", "customer"]);
  });
  it("cards a business conversation's invoices and orders, not items", () => {
    expect(docMentions([{ kind: "invoice", name: "INV-0001" }, { kind: "item", name: "Rice" }, { kind: "invoice", name: "INV-0001" }, { kind: "purchase", name: "P-ABC123" }])).toEqual([
      { kind: "invoice", name: "INV-0001" },
      { kind: "purchase", name: "P-ABC123" },
    ]);
  });
});

describe("the card", () => {
  it("says where it stands", () => {
    expect(cardPill({ kind: "invoice", status: "paid" })).toEqual({ text: "Paid", tone: "lime" });
    expect(cardPill({ kind: "invoice", status: "overdue", due_kobo: 1 })).toEqual({ text: "Overdue", tone: "danger" });
    expect(cardPill({ kind: "invoice", status: "sent", due_kobo: 100 })).toEqual({ text: "Sent", tone: "warn" });
    expect(cardPill({ kind: "bill", status: "open", due_kobo: 100 })?.text).toBe("To pay");
    expect(cardPill({ kind: "customer" })).toBeUndefined();
  });
  it("fills the bar by what is paid", () => {
    expect(paidShare({ total_kobo: 4000, paid_kobo: 1000 })).toBe(0.25);
    expect(paidShare({ total_kobo: 0 })).toBe(0);
    expect(paidShare({ total_kobo: 100, paid_kobo: 500 })).toBe(1);
  });
});
