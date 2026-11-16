import { describe, expect, it } from "vitest";
import {
  aboutOrderDraft,
  basketTotal,
  cardFigure,
  cardKicker,
  changeBasket,
  chatAboutWords,
  chatPreview,
  closedNote,
  fewLeftWords,
  headLine,
  hoursLines,
  itemsWords,
  mergeMessages,
  minutesWords,
  offersQuestion,
  openWords,
  payWords,
  personFileURL,
  plainItem,
  sizeWords,
  startProblem,
  stateWords,
  stopOffersWords,
  withLatest,
  type PersonMessage,
} from "./personchat";

const msg = (id: string, at: string, extra: Partial<PersonMessage> = {}): PersonMessage => ({
  id,
  mine: false,
  kind: "text",
  body: id,
  read_at: null,
  created_at: at,
  ...extra,
});

describe("a person's chats with businesses", () => {
  it("asks about offers once, and stops them by name", () => {
    expect(offersQuestion("Mama's Kitchen")).toBe("Get offers from Mama's Kitchen?");
    expect(stopOffersWords("Mama's Kitchen")).toBe("Stop offers from Mama's Kitchen");
  });

  it("says why nothing can be written", () => {
    expect(closedNote({ blocked: true, blocked_you: false, name: "Bola" })).toBe("You blocked Bola. Unblock them to write to them.");
    expect(closedNote({ blocked: false, blocked_you: true, name: "Bola" })).toBe("Bola is not taking messages from you.");
    expect(closedNote({ blocked: false, blocked_you: false, name: "Bola" })).toBeNull();
  });

  it("puts typing before the hours under the name", () => {
    expect(headLine({ typing: true, hours: { open: true, line: "Open now" } })).toBe("typing…");
    expect(headLine({ hours: { open: true, line: "Open now, closes at 18:00" } })).toBe("Open now, closes at 18:00");
    expect(headLine({})).toBe("");
  });

  it("previews a row", () => {
    expect(chatPreview({ last: "Is it ready?", last_mine: true, blocked: false, blocked_you: false })).toBe("You: Is it ready?");
    expect(chatPreview({ last: "Yes", last_mine: false, blocked: false, blocked_you: false })).toBe("Yes");
    expect(chatPreview({ last: "", last_mine: false, blocked: false, blocked_you: false })).toBe("No messages yet");
    expect(chatPreview({ last: "Yes", last_mine: false, blocked: true, blocked_you: false })).toBe("You blocked them");
  });

  it("says an order's state in a customer's words", () => {
    expect(stateWords("received")).toEqual({ label: "Sent", tone: "plain" });
    expect(stateWords("awaiting_payment")).toEqual({ label: "To pay", tone: "warn" });
    expect(stateWords("completed").label).toBe("Done");
    expect(stateWords("something_new").label).toBe("Something new");
    expect(stateWords(undefined).label).toBe("");
  });

  it("names a card's kind and its figure", () => {
    expect(cardKicker({ type: "order", title: "Order MK-1" })).toBe("Order");
    expect(cardKicker({ type: "order", title: "Booking MK-2" })).toBe("Booking");
    expect(cardKicker({ type: "booking", title: "Book Braids" })).toBe("Book");
    expect(cardKicker({ type: "booking", title: "Booking MK-3", reference: "MK-3" })).toBe("Booking");
    expect(cardKicker({ type: "pay", title: "Pay for MK-1" })).toBe("Pay");
    expect(cardKicker({ type: "product", title: "Jollof" })).toBe("Product");
    expect(cardFigure({ type: "booking", title: "Book Braids", total_kobo: 1_500_000, minutes: 90 })).toBe("₦15,000 · 1 h 30 min");
    expect(cardFigure({ type: "product", title: "Jollof" })).toBe("");
    expect(minutesWords(45)).toBe("45 min");
    expect(minutesWords(60)).toBe("1 h");
  });

  it("keeps a basket: more, fewer, gone at none, totals with the delivery fee", () => {
    const rice = { item_id: "r", name: "Rice", price_kobo: 250_000 };
    let b = changeBasket([], rice, 1);
    b = changeBasket(b, rice, 1);
    b = changeBasket(b, { item_id: "s", name: "Stew", price_kobo: 100_000 }, 1);
    expect(b).toEqual([
      { ...rice, quantity: 2 },
      { item_id: "s", name: "Stew", price_kobo: 100_000, quantity: 1 },
    ]);
    expect(basketTotal(b)).toBe(600_000);
    expect(basketTotal(b, 50_000)).toBe(650_000);
    expect(itemsWords(b)).toBe("3 items");
    b = changeBasket(b, rice, -2);
    expect(b.map((l) => l.item_id)).toEqual(["s"]);
    expect(itemsWords(b)).toBe("1 item");
    expect(changeBasket([], rice, -1)).toEqual([]);
  });

  it("names sizes and how few are left", () => {
    expect(sizeWords({ colour: "Red", size: "42" })).toBe("Red, 42");
    expect(fewLeftWords(3)).toBe("Only 3 left");
    expect(fewLeftWords(undefined)).toBe("");
    expect(fewLeftWords(0)).toBe("");
    expect(plainItem({ id: "a", name: "A", available: true })).toBe(true);
    expect(plainItem({ id: "a", name: "A", available: true, sizes: [{ id: "a1", variant: { size: "S" }, available: true }] })).toBe(false);
  });

  it("says how an order is paid", () => {
    expect(payWords("at_order", "Bola")).toBe("You pay when you order, by a pay link.");
    expect(payWords("after_accept", "Bola")).toBe("You pay once Bola accepts your order.");
    expect(payWords("none", "Bola")).toBe("You pay Bola as you agree with them.");
  });

  it("merges a thread by id, oldest first, keeping sends under way", () => {
    const a = msg("a", "2026-11-16T10:00:00Z");
    const b = msg("b", "2026-11-16T10:01:00Z");
    const c = msg("c", "2026-11-16T10:02:00Z");
    expect(mergeMessages([c, a], [b, a]).map((m) => m.id)).toEqual(["a", "b", "c"]);
    const local = msg("local-1", "2026-11-16T10:03:00Z", { mine: true, local: true });
    const older = msg("old", "2026-11-16T09:00:00Z");
    expect(withLatest([older, a, local], [b, c], true).map((m) => m.id)).toEqual(["old", "a", "b", "c", "local-1"]);
    expect(withLatest([older, a, local], [b, c], false).map((m) => m.id)).toEqual(["b", "c", "local-1"]);
  });

  it("reads a shop's hours and whether it is open", () => {
    const day = [{ open: "08:00", close: "18:00" }];
    expect(hoursLines({ days: [day, day, day, day, day, [{ open: "10:00", close: "14:00" }], []] })).toEqual([
      "Monday 08:00 to 18:00",
      "Tuesday 08:00 to 18:00",
      "Wednesday 08:00 to 18:00",
      "Thursday 08:00 to 18:00",
      "Friday 08:00 to 18:00",
      "Saturday 10:00 to 14:00",
      "Sunday closed",
    ]);
    expect(hoursLines(undefined)).toEqual([]);
    expect(openWords(true)).toBe("Open now");
    expect(openWords(false)).toBe("Closed now");
    expect(openWords(undefined)).toBe("");
  });

  it("says a refused start plainly", () => {
    expect(startProblem(410, { link_replaced: true }, "x")).toBe("This link no longer works. Ask the business for its new link.");
    expect(startProblem(403, { blocked: true }, "x")).toBe("This business is not taking messages from you.");
    expect(startProblem(409, {}, "Add your phone number first.")).toBe("Add your phone number first.");
  });

  it("chats about an order, and reads files from the person's own copy", () => {
    expect(chatAboutWords("Mama's Kitchen")).toBe("Chat with Mama's Kitchen about this order");
    expect(aboutOrderDraft("MK-2X4F")).toBe("Hello, about my order MK-2X4F: ");
    expect(personFileURL("c 1", "m/2")).toBe("/v1/me/chats/c%201/files/m%2F2");
  });
});
