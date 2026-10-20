import { describe, expect, it } from "vitest";
import {
  ORDER_FILTERS,
  ORDER_STATUS,
  BOOKING_PAYMENT_CHOICES,
  actionForm,
  actionProblem,
  actionRequest,
  customerLine,
  formURL,
  isMasked,
  itemsSummary,
  orderStatus,
  orderTotal,
  orderWhat,
  ordersPath,
  paymentChoices,
  priceChangeLine,
  paymentSummary,
  refundLine,
  relativeAge,
  setupRequest,
  telLink,
  timeLeft,
  whatsappLink,
  type StorefrontAction,
} from "./orders";

const order: StorefrontAction = {
  kind: "order",
  enabled: false,
  label: "Order now",
  payment: "after_accept",
  options: { pickup: true },
  workflow_id: "wf-1",
};

describe("setting a form up", () => {
  it("instantiates the form's template, named for it", () => {
    expect(setupRequest("order")).toEqual({ path: "/v1/templates/storefront-order/instantiate", body: { params: {}, name: "Storefront orders" } });
    expect(setupRequest("enquiry")).toEqual({ path: "/v1/templates/storefront-enquiry/instantiate", body: { params: {}, name: "Storefront questions" } });
    expect(setupRequest("callback").body.name).toBe("Storefront call-backs");
    expect(setupRequest("callback").path).toBe("/v1/templates/storefront-callback/instantiate");
    expect(setupRequest("booking")).toEqual({ path: "/v1/templates/storefront-booking/instantiate", body: { params: {}, name: "Storefront bookings" } });
  });
});

describe("a form's settings", () => {
  it("round-trips an order's settings", () => {
    const f = actionForm({ ...order, options: { connection: "shop", pickup: true, delivery: true, delivery_note: "Yaba only" } });
    expect(actionRequest("order", { ...f, enabled: true })).toEqual({
      enabled: true,
      label: "Order now",
      payment: "after_accept",
      options: { connection: "shop", pickup: true, delivery: true, delivery_note: "Yaba only" },
    });
  });

  it("leaves out empty options and a note without delivery", () => {
    const f = { ...actionForm(order), connection: "  ", deliveryNote: "Yaba only", label: " Buy " };
    expect(actionRequest("order", f)).toEqual({ enabled: false, label: "Buy", payment: "after_accept", options: { pickup: true } });
  });

  it("sends no payment or options for a question or call-back", () => {
    const f = { ...actionForm({ ...order, kind: "enquiry", label: "Ask" }), payment: "at_order" as const, delivery: true };
    expect(actionRequest("enquiry", f)).toEqual({ enabled: false, label: "Ask", payment: "none", options: {} });
  });

  it("names what the server would refuse", () => {
    const f = actionForm(order);
    expect(actionProblem("order", f)).toBeNull();
    expect(actionProblem("order", { ...f, label: "x".repeat(41) })).toMatch(/40 characters/);
    expect(actionProblem("order", { ...f, deliveryNote: "x".repeat(121) })).toMatch(/120 characters/);
    expect(actionProblem("order", { ...f, enabled: true, pickup: false, delivery: false })).toMatch(/pickup, delivery/);
    expect(actionProblem("enquiry", { ...f, pickup: false, deliveryNote: "x".repeat(200) })).toBeNull();
  });

  it("sends a booking's payment, connection and deposit", () => {
    const booking: StorefrontAction = { kind: "booking", enabled: true, label: "Book", payment: "at_order", options: { connection: "shop", deposit_percent: 30 }, workflow_id: "wf-2" };
    const f = actionForm(booking);
    expect(f.deposit).toBe("30");
    expect(actionRequest("booking", f)).toEqual({ enabled: true, label: "Book", payment: "at_order", options: { connection: "shop", deposit_percent: 30 } });
    // Blank is the full price; paying at the appointment drops the online options.
    expect(actionRequest("booking", { ...f, deposit: "" }).options).toEqual({ connection: "shop" });
    expect(actionRequest("booking", { ...f, payment: "none", pickup: true })).toEqual({ enabled: true, label: "Book", payment: "none", options: {} });
    // Keeping the deposit on a customer's cancellation, only when paying online.
    expect(actionForm({ ...booking, options: { cancel_refund: "keep_deposit" } }).keepDeposit).toBe(true);
    expect(actionRequest("booking", { ...f, keepDeposit: true }).options.cancel_refund).toBe("keep_deposit");
    expect(actionRequest("booking", { ...f, keepDeposit: true, payment: "none" }).options).toEqual({});
  });

  it("holds a deposit to 1 to 100 per cent", () => {
    const f = actionForm({ kind: "booking", enabled: false, label: "Book", payment: "after_accept", options: {}, workflow_id: null });
    expect(actionProblem("booking", f)).toBeNull();
    expect(actionProblem("booking", { ...f, deposit: "100" })).toBeNull();
    expect(actionProblem("booking", { ...f, deposit: "1" })).toBeNull();
    for (const bad of ["0", "101", "12.5", "half"]) expect(actionProblem("booking", { ...f, deposit: bad })).toMatch(/1 to 100/);
    // No deposit when paying at the appointment.
    expect(actionProblem("booking", { ...f, payment: "none", deposit: "0" })).toBeNull();
  });

  it("words a booking's payment choices for appointments", () => {
    expect(BOOKING_PAYMENT_CHOICES.map((c) => c.label)).toEqual(["Pay when booking", "Pay after I confirm", "Pay at the appointment"]);
    expect(paymentChoices("booking")).toBe(BOOKING_PAYMENT_CHOICES);
    expect(paymentChoices("order")[0]?.label).toMatch(/after you accept/);
  });

  it("links to the public form", () => {
    expect(formURL("https://mama.taskiem.page", "order")).toBe("https://mama.taskiem.page/a/order");
    expect(formURL("https://mama.taskiem.page/", "callback")).toBe("https://mama.taskiem.page/a/callback");
  });
});

describe("order statuses", () => {
  it("labels and colours every status", () => {
    for (const s of Object.keys(ORDER_STATUS)) {
      const v = orderStatus(s);
      expect(v.label).not.toBe("");
      expect(["ok", "info", "warn", "danger", "muted"]).toContain(v.tone);
    }
    expect(orderStatus("refund_due")).toEqual({ label: "Refund due", tone: "danger" });
    expect(orderStatus("completed").tone).toBe("ok");
    expect(orderStatus("received").tone).toBe("warn");
  });

  it("shows an unknown status in words", () => {
    expect(orderStatus("on_hold")).toEqual({ label: "on hold", tone: "muted" });
  });

  it("asks the API for the filter's status", () => {
    expect(ordersPath("open")).toBe("/v1/storefront/orders?status=open");
    expect(ordersPath("")).toBe("/v1/storefront/orders?status=open");
    expect(ordersPath("all")).toBe("/v1/storefront/orders");
    expect(ordersPath("refund_due")).toBe("/v1/storefront/orders?status=refund_due");
    expect(ORDER_FILTERS.map((f) => f.label)).toEqual(["Open", "All", "Completed", "Declined", "Expired", "Refund due", "New prices to confirm", "Cancelled", "No-show", "Refunded"]);
  });

  it("labels the booking and price statuses", () => {
    expect(orderStatus("no_show")).toEqual({ label: "No-show", tone: "muted" });
    expect(orderStatus("price_changed")).toEqual({ label: "Waiting for the customer to confirm new prices", tone: "warn" });
  });
});

describe("ages", () => {
  const now = Date.parse("2026-10-20T12:00:00Z");
  const ago = (s: number) => new Date(now - s * 1000).toISOString();
  it("says how long ago", () => {
    expect(relativeAge(ago(10), now)).toBe("just now");
    expect(relativeAge(ago(-30), now)).toBe("just now");
    expect(relativeAge(ago(5 * 60), now)).toBe("5 min ago");
    expect(relativeAge(ago(3600), now)).toBe("1 hour ago");
    expect(relativeAge(ago(5 * 3600), now)).toBe("5 hours ago");
    expect(relativeAge(ago(30 * 3600), now)).toBe("yesterday");
    expect(relativeAge(ago(3 * 86400), now)).toBe("3 days ago");
    expect(relativeAge(ago(30 * 86400), now)).toBe("20 Sept 2026");
    expect(relativeAge("not a date", now)).toBe("");
  });

  it("says how long is left", () => {
    expect(timeLeft(new Date(now + 5 * 60_000).toISOString(), now)).toBe("in 5 min");
    expect(timeLeft(new Date(now + 3 * 3600_000).toISOString(), now)).toBe("in 3 hours");
    expect(timeLeft(new Date(now - 1000).toISOString(), now)).toBe("now");
  });
});

describe("an order as the list reads it", () => {
  it("summarises the basket", () => {
    expect(itemsSummary([{ name: "Jollof rice", quantity: 2 }, { name: "Chapman", quantity: 1 }, { quantity: 3 }])).toBe("2 × Jollof rice, 1 × Chapman");
    expect(itemsSummary(null)).toBe("");
  });

  it("says how the customer pays", () => {
    expect(paymentSummary({ kind: "order", payment: "after_accept", paid_at: null })).toBe("Pays after you accept · not paid");
    expect(paymentSummary({ kind: "order", payment: "at_order", paid_at: "2026-10-20T12:00:00Z" })).toBe("Pays when ordering · paid");
    expect(paymentSummary({ kind: "order", payment: "none" })).toBe("No online payment");
    expect(paymentSummary({ kind: "enquiry", payment: "after_accept" })).toBe("No online payment");
  });

  it("shows a booking's service and time instead of a basket", () => {
    const booking = { kind: "booking" as const, basket: null, service_name: "Haircut", starts_at: "2026-10-24T09:30:00Z", total_kobo: 0 };
    expect(orderWhat(booking)).toBe("Haircut · Sat 24 Oct, 10:30");
    expect(orderWhat({ kind: "order", basket: [{ name: "Chapman", quantity: 2 }] })).toBe("2 × Chapman");
    expect(orderTotal(booking)).toBe("Price on the day");
    expect(orderTotal({ ...booking, total_kobo: 500000 })).toBe("₦5,000");
    expect(orderTotal({ kind: "enquiry", total_kobo: 0 })).toBe("—");
  });

  it("says how a booking is paid", () => {
    expect(paymentSummary({ kind: "booking", payment: "none" })).toBe("Pays at the appointment");
    expect(paymentSummary({ kind: "booking", payment: "at_order", paid_at: null, deposit_kobo: 150000 })).toBe("Pays when booking · deposit ₦1,500 · not paid");
    expect(paymentSummary({ kind: "booking", payment: "after_accept", paid_at: "2026-10-20T12:00:00Z" })).toBe("Pays after you confirm · paid");
  });

  it("shows the new prices", () => {
    expect(priceChangeLine({ basket: [], total_kobo: 520000 })).toBe("New total ₦5,200.");
    expect(priceChangeLine({ basket: [], total_kobo: 520000, gone: ["Chapman"] })).toBe("New total ₦5,200. No longer offered: Chapman.");
    expect(priceChangeLine(null)).toBe("");
  });

  it("says what to refund", () => {
    expect(refundLine(450000)).toBe("₦4,500 is due back to the customer. Refund it here through Paystack, or refund it yourself in Paystack.");
  });
});

describe("the customer, masked or shown", () => {
  it("knows a masked value", () => {
    expect(isMasked("A•••")).toBe(true);
    expect(isMasked("•••4567")).toBe(true);
    expect(isMasked("Ada")).toBe(false);
    expect(isMasked(undefined)).toBe(false);
  });

  it("shows the masked name and phone", () => {
    expect(customerLine({ name: "A•••", phone: "•••4567" })).toBe("A••• · •••4567");
    expect(customerLine({})).toBe("—");
  });

  it("links a shown phone, never a masked one", () => {
    expect(telLink("+234 803 123 4567")).toBe("tel:+2348031234567");
    expect(telLink("•••4567")).toBeNull();
    expect(whatsappLink("0803 123 4567")).toBe("https://wa.me/2348031234567");
    expect(whatsappLink("+2348031234567")).toBe("https://wa.me/2348031234567");
    expect(whatsappLink("•••4567")).toBeNull();
    expect(whatsappLink("")).toBeNull();
  });
});
