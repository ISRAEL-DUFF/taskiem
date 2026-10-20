import { describe, expect, it } from "vitest";
import {
  ORDER_FILTERS,
  ORDER_STATUS,
  actionForm,
  actionProblem,
  actionRequest,
  customerLine,
  formURL,
  isMasked,
  itemsSummary,
  orderStatus,
  ordersPath,
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
    expect(ORDER_FILTERS.map((f) => f.label)).toEqual(["Open", "All", "Completed", "Declined", "Expired", "Refund due"]);
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

  it("says what to refund", () => {
    expect(refundLine(450000)).toBe("Refund ₦4,500 to the customer in Paystack.");
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
