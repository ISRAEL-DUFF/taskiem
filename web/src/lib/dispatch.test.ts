import { describe, expect, it } from "vitest";
import {
  bankProblem,
  currentDelivery,
  deliveryStatus,
  deliverySteps,
  deliverySub,
  offerOutcome,
  payLineSub,
  remitProblem,
  settingsForm,
  settingsProblem,
  settingsRequest,
  type Delivery,
  type DispatchSettings,
} from "./dispatch";
import { actionForm, actionRequest, orderStatus, paymentSummary, type StorefrontAction } from "./orders";
import { riderSub, type Rider } from "./riders";

const held: Delivery = {
  id: "d2",
  order_id: "o1",
  reference: "ADA-7K2Q",
  rider_id: "r1",
  rider: "Musa",
  status: "picked_up",
  offered_at: "2026-10-28T09:00:00Z",
  accepted_at: "2026-10-28T09:02:00Z",
  picked_up_at: "2026-10-28T09:20:00Z",
  fee_kobo: 100000,
  zone: "Lekki",
};

describe("dispatch", () => {
  it("pick the delivery a rider holds over earlier offers", () => {
    const passed: Delivery = { ...held, id: "d1", status: "passed", rider: "Tunde" };
    expect(currentDelivery([passed, held])?.id).toBe("d2");
    expect(currentDelivery([passed])).toBeUndefined();
    expect(currentDelivery([passed, { ...held, status: "offered" }])?.status).toBe("offered");
  });

  it("say how far a delivery got, in Lagos time", () => {
    expect(deliverySteps(held)).toEqual([
      { label: "Offered to Musa", at: "10:00" },
      { label: "Accepted", at: "10:02" },
      { label: "Picked up", at: "10:20" },
    ]);
    const cash = { ...held, status: "delivered" as const, delivered_at: "2026-10-28T10:00:00Z", paid_how: "cash" as const };
    expect(deliverySteps(cash).at(-1)).toEqual({ label: "Delivered, paid in cash", at: "11:00" });
    expect(deliveryStatus("picked_up").label).toBe("On the way");
    expect(deliverySub({ ...held, late: "arrival", rating: 4 })).toBe("Musa · Lekki · fee ₦1,000 · not arrived in time · rated 4 of 5");
  });

  it("say what came of an offer", () => {
    expect(offerOutcome({ status: "offered", rider: "Musa" })).toMatch(/^Offered to Musa/);
    expect(offerOutcome({ status: "no_rider" })).toMatch(/No free rider/);
    expect(offerOutcome({ status: "asked_owner" })).toMatch(/asked in chat/);
  });

  it("check and build the settings", () => {
    const s: DispatchSettings = { mode: "rotation", offer_minutes: 5, pickup_minutes: 20, arrive_minutes: 90, proof: false, owe_limit_kobo: 2000000, rating_mark: 3 };
    const f = settingsForm(s);
    expect(f.oweLimit).toBe("20000");
    expect(settingsProblem(f)).toBeNull();
    expect(settingsRequest(f)).toEqual(s);
    expect(settingsProblem({ ...f, offer: "0" })).toMatch(/accept/);
    expect(settingsProblem({ ...f, arrive: "5" })).toMatch(/customer/);
    expect(settingsProblem({ ...f, oweLimit: "lots" })).toMatch(/cash/);
    expect(settingsProblem({ ...f, ratingMark: "6" })).toMatch(/rating/);
  });

  it("say what riders are due and check what is entered", () => {
    expect(payLineSub({ rider_id: "r1", rider: "Musa", deliveries: 2, fees_kobo: 800000, owed_kobo: 650000, net_kobo: 150000, bank: "" })).toBe(
      "2 deliveries, fees ₦8,000 · less ₦6,500 cash they owe · No bank account yet",
    );
    expect(bankProblem("058", "0123456789")).toBeNull();
    expect(bankProblem("", "0123456789")).toMatch(/bank/);
    expect(bankProblem("058", "12345")).toMatch(/10 digits/);
    expect(remitProblem("6500", 650000)).toBeNull();
    expect(remitProblem("7000", 650000)).toMatch(/owe ₦6,500/);
    expect(remitProblem("", 650000)).toMatch(/amount/);
  });

  it("read the dispatch statuses and paying at the door on orders", () => {
    expect(orderStatus("on_the_way").label).toBe("On the way");
    expect(orderStatus("dispatched").label).toBe("With a rider");
    expect(paymentSummary({ kind: "order", payment: "on_delivery", paid_at: null })).toBe("Pays at the door · not paid");
    const a: StorefrontAction = { kind: "order", enabled: true, label: "Order", payment: "after_accept", workflow_id: null, options: { delivery: true, pay_on_delivery: true } };
    const f = actionForm(a);
    expect(f.payOnDelivery).toBe(true);
    expect(actionRequest("order", f).options.pay_on_delivery).toBe(true);
    expect(actionRequest("order", { ...f, delivery: false, pickup: true }).options.pay_on_delivery).toBeUndefined();
  });

  it("show a rider's rating and not answering", () => {
    const r: Rider = {
      id: "r1", name: "Musa", phone: "+2348031234567", zones: [], fee_kobo: 0, active: true, reached: "whatsapp",
      presence: { state: "free" }, created_at: "2026-10-28T09:00:00Z", rating: 4.25, ratings: 8, not_answering: true,
    };
    expect(riderSub(r)).toBe("All areas · On WhatsApp · rated 4.3 of 5 (8) · not answering offers today");
  });
});

describe("riders on the Money page", () => {
  it("say a rider's period in a line", async () => {
    const { riderDayLine } = await import("./money");
    expect(
      riderDayLine({ rider_id: "r1", rider: "Musa", delivered: 4, door_transfer_kobo: 850000, cash_kobo: 300000, remitted_kobo: 0, owed_kobo: 300000, fees_kobo: 400000, rating: 4.5, ratings: 2 }),
    ).toBe("4 delivered · ₦8,500 paid at the door · ₦3,000 cash · fees ₦4,000 · rated 4.5");
  });
});

describe("payouts stuck being paid", () => {
  it("say a payout and ask before marking it not paid", async () => {
    const { payoutSub, notPaidQuestion } = await import("./dispatch");
    const x = { payout_id: "p1", rider_id: "r1", rider: "Musa", deliveries: 2, fees_kobo: 200000, owed_kobo: 50000, amount_kobo: 150000, status: "pending" as const };
    expect(payoutSub(x)).toBe("2 deliveries, ₦500 cash taken off");
    expect(payoutSub({ ...x, created_at: "2026-10-28T09:00:00Z" })).toMatch(/^2 deliveries, ₦500 cash taken off · started .+, 10:00$/);
    expect(notPaidQuestion(x)).toMatch(/^Mark Musa's payout of ₦1,500 as not paid\? Only once your bank shows it did not go/);
  });
});
