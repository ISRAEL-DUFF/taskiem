import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  addPage,
  addressBody,
  addressFormProblem,
  addressLine,
  addressProblem,
  addressSub,
  dueWords,
  emptyAddress,
  fulfilmentWords,
  orderAPI,
  orderLine,
  orderPath,
  ordersPage,
  payProblem,
  type PersonOrder,
} from "./youorders";

const row = (ref: string, business = "b1"): PersonOrder => ({
  business_id: business,
  business: "Mama's Kitchen",
  reference: ref,
  kind: "order",
  state: "accepted",
  summary: "2 × Jollof rice",
  total_kobo: 900_000,
  due_kobo: 0,
  pay: false,
  made_at: "2026-11-17T10:00:00Z",
  chat_id: null,
});

describe("Orders on You", () => {
  it("pages by the cursor", () => {
    expect(ordersPage(null)).toBe("/v1/me/orders?limit=30");
    expect(ordersPage("2026-11-17T10:00:00Z|MK-1")).toBe("/v1/me/orders?limit=30&before=2026-11-17T10%3A00%3A00Z%7CMK-1");
  });

  it("adds a page without a row twice", () => {
    expect(addPage([row("A"), row("B")], [row("B"), row("C"), row("B", "b2")]).map((o) => `${o.business_id}/${o.reference}`)).toEqual(["b1/A", "b1/B", "b1/C", "b2/B"]);
  });

  it("has the order's paths", () => {
    expect(orderPath({ business_id: "b 1", reference: "MK/1" })).toBe("/you/orders/b%201/MK%2F1");
    expect(orderAPI({ business_id: "b1", reference: "MK-1" })).toBe("/v1/me/orders/b1/MK-1");
  });

  it("says a row's line and what is due", () => {
    expect(orderLine(row("MK-1"), "Today, 10:00")).toBe("2 × Jollof rice · MK-1 · Today, 10:00");
    expect(orderLine({ ...row("MK-2"), summary: "", kind: "booking" }, "")).toBe("A booking · MK-2");
    expect(dueWords({ due_kobo: 0, total_kobo: 100, pay: false })).toBe("");
    expect(dueWords({ due_kobo: 100, total_kobo: 100, pay: true })).toBe("To pay");
    expect(dueWords({ due_kobo: 100, total_kobo: 100, pay: false })).toBe("Not paid yet");
    expect(dueWords({ due_kobo: 50_000, total_kobo: 100_000, pay: true })).toBe("₦500 still to pay");
    expect(fulfilmentWords("delivery", "Yaba")).toBe("Delivered to you in Yaba");
    expect(fulfilmentWords("pickup")).toBe("You pick it up from them");
  });

  it("says a refused pay link plainly", () => {
    expect(payProblem(new ApiError(403, "blocked", [], { blocked: true }))).toMatch(/not taking/);
    expect(payProblem(new ApiError(409, "off", [], { area_off: true }))).toMatch(/not taking orders/);
    expect(payProblem(new ApiError(409, "Nothing is due on this order."))).toBeNull();
  });
});

describe("saved addresses", () => {
  it("shows an address in a line and its details", () => {
    expect(addressLine({ line: "12 Herbert Macaulay Way ", area: "Yaba" })).toBe("12 Herbert Macaulay Way, Yaba");
    expect(addressLine({ line: "12 Allen Avenue", area: "" })).toBe("12 Allen Avenue");
    expect(addressSub({ default: true, landmark: "the blue gate", phone: "+2348031234567" })).toBe("Default · Near the blue gate · Rider calls +2348031234567");
    expect(addressSub({ default: false, landmark: "", phone: null })).toBe("");
  });

  it("checks the form and builds the body", () => {
    const f = { ...emptyAddress(true), label: "Home", line: "12 Allen Avenue" };
    expect(addressFormProblem(emptyAddress(false))).toMatch(/name/);
    expect(addressFormProblem({ ...f, line: " " })).toMatch(/street/);
    expect(addressFormProblem(f)).toBeNull();
    expect(addressBody(f, false)).toEqual({ label: "Home", line: "12 Allen Avenue", area: "", landmark: "", default: true });
    // Changing one sends an empty phone, which takes it away.
    expect(addressBody({ ...f, default: false }, true)).toEqual({ label: "Home", line: "12 Allen Avenue", area: "", landmark: "", phone: "" });
    expect(addressBody({ ...f, phone: " 0803 123 4567 ", default: false }, false)).toMatchObject({ phone: "0803 123 4567" });
  });

  it("says the limit and a bad phone plainly", () => {
    expect(addressProblem(new ApiError(409, "x"))).toBe("You can keep up to 20 addresses. Delete one you no longer use first.");
    expect(addressProblem(new ApiError(400, "x", [], { phone_invalid: true }))).toMatch(/phone number does not look right/);
    expect(addressProblem(new ApiError(400, "The street is too long."))).toBeNull();
  });
});
