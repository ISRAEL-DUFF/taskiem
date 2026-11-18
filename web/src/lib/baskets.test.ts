import { describe, expect, it } from "vitest";
import { basketCan, basketCardLine, basketStateWords, basketWithLive, holderWords, myLines, payChoiceWords, shareWords, type Basket } from "./baskets";

const naira = (k: number) => `₦${(k / 100).toLocaleString("en-NG")}`;

function basket(over: Partial<Basket> = {}): Basket {
  return {
    id: "b1",
    business_id: "biz",
    business: "Mama's Kitchen",
    where: "group",
    group_id: "g1",
    state: "open",
    starter_id: "ada",
    holder_id: "ada",
    holder: "You",
    you_hold: true,
    holder_gone: false,
    shop: { open: true, pickup: true, delivery: true, zones: [{ name: "Yaba", fee_kobo: 100000 }], payment: "at_order", each_offered: true },
    people: [
      {
        person_id: "ada",
        name: "You",
        you: true,
        items_kobo: 450000,
        lines: [{ id: "l1", item_id: "1", name: "Jollof rice", quantity: 1, price_kobo: 450000, added_price_kobo: 450000, price_changed: false, line_kobo: 450000, available: true }],
      },
      {
        person_id: "bola",
        name: "Bola Ade",
        you: false,
        items_kobo: 300000,
        lines: [{ id: "l2", item_id: "2", name: "Chapman", quantity: 2, price_kobo: 150000, added_price_kobo: 150000, price_changed: false, line_kobo: 300000, available: true }],
      },
    ],
    items_kobo: 750000,
    delivery_kobo: 0,
    total_kobo: 750000,
    problems: [],
    created_at: "2026-11-18T10:00:00Z",
    ...over,
  };
}

describe("ordering together", () => {
  it("says the state, the holder and the card's line", () => {
    expect(basketStateWords(basket()).label).toBe("Open: add what you want");
    expect(basketStateWords(basket({ state: "closed" })).tone).toBe("warn");
    expect(basketStateWords({ state: "placed", order: { line: "Accepted by the business" } as Basket["order"] }).label).toBe("Accepted by the business");
    expect(holderWords(basket())).toContain("You hold this basket");
    expect(holderWords(basket({ you_hold: false, holder: "Bola Ade" }))).toBe("Bola Ade holds this basket: they close it and place the order.");
    expect(holderWords(basket({ holder_gone: true }))).toContain("anyone can take it over");
    expect(holderWords(basket({ state: "placed" }))).toBe("");
    expect(basketCardLine(basket(), naira)).toBe("2 people · ₦7,500");
    expect(basketCardLine(basket({ people: [] }), naira)).toContain("Nothing in it yet");
  });

  it("knows what each may do (C1, C2)", () => {
    const open = basketCan(basket());
    expect(open.addMine && open.close && open.holder && !open.place && !open.reopen).toBe(true);
    const member = basketCan(basket({ you_hold: false }));
    expect(member.addMine && !member.close && !member.holder).toBe(true);
    const closed = basketCan(basket({ state: "closed" }));
    expect(closed.place && closed.reopen && !closed.addMine).toBe(true);
    expect(basketCan(basket({ holder_gone: true, you_hold: false })).takeOver).toBe(true);
    const share = { items_kobo: 300000, delivery_kobo: 50000, amount_kobo: 350000, paid: false, card_sent: false };
    const placedEach = basket({
      state: "placed",
      payment: "each",
      placed_by: "ada",
      order: { reference: "MA-1", status: "awaiting_payment", line: "Waiting for payment", total_kobo: 850000, paid_kobo: 0, left_kobo: 850000, payment_state: "unpaid", payable_now: true },
      people: basket().people.map((p) => ({ ...p, share })),
    });
    const each = basketCan(placedEach);
    expect(each.payMine && !each.payOrder && !each.shareCards).toBe(true);
    expect(each.payFor.map((p) => p.person_id)).toEqual(["bola"]);
    const all = basketCan({ ...placedEach, payment: "all" });
    expect(all.payOrder && all.shareCards && !all.payMine).toBe(true);
    expect(basketCan({ ...placedEach, placed_by: "bola" }).payFor).toEqual([]);
  });

  it("says the payment choice and a share", () => {
    expect(payChoiceWords(true, "Mama's Kitchen").note).toContain("equal part of the delivery");
    expect(payChoiceWords(false, "Mama's Kitchen").note).toContain("takes no payment in the app, so you pay for everyone");
    expect(shareWords({ items_kobo: 300000, delivery_kobo: 33333, amount_kobo: 333333, paid: false, card_sent: false }, naira)).toBe("₦3,333.33: items ₦3,000 and delivery ₦333.33");
    expect(shareWords({ items_kobo: 300000, delivery_kobo: 0, amount_kobo: 300000, paid: false, card_sent: false }, naira)).toBe("₦3,000: items ₦3,000");
  });

  it("keeps your own lines and takes a live basket only for itself", () => {
    expect(myLines(basket())).toEqual([{ item_id: "1", name: "Jollof rice", price_kobo: 450000, quantity: 1 }]);
    const cur = basket();
    expect(basketWithLive(cur, { basket: basket({ state: "closed" }) })?.state).toBe("closed");
    expect(basketWithLive(cur, { basket: basket({ id: "other", state: "closed" }) })).toBe(cur);
    expect(basketWithLive(undefined, { basket: cur })).toBeUndefined();
  });
});
