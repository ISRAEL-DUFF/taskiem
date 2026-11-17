import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  addPage,
  addressBody,
  addressFormProblem,
  addressLine,
  addressProblem,
  addressSub,
  bookingWhen,
  chosenAddress,
  dueWords,
  emptyAddress,
  fulfilmentWords,
  freshen,
  keepProblem,
  orderAPI,
  orderIcon,
  orderLine,
  orderPath,
  orderProblem,
  orderTitle,
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
    // The server's own sentence: closed, a new price, nothing due.
    expect(payProblem(new ApiError(409, "This order is closed."))).toBe("This order is closed.");
    expect(payProblem(new ApiError(409, "Nothing is due on this order."))).toBe("Nothing is due on this order.");
    expect(payProblem(new ApiError(409, "x", [], { earlier: true }))).toMatch(/^This order was made before this number was yours, so it can't be paid here\./);
    expect(payProblem(new ApiError(429, "Too many tries. Wait a moment and try again."))).toBe("Too many tries. Wait a moment and try again.");
    expect(payProblem(new ApiError(409, "conflict"))).toBeNull();
    expect(payProblem(new Error("x"))).toBeNull();
  });

  it("says a refused order page plainly", () => {
    expect(orderProblem(new ApiError(404, "x"))).toBe("This order is not one of yours, or it is no longer kept.");
    expect(orderProblem(new ApiError(429, "x"))).toBe("Too many tries. Wait a moment and try again.");
    expect(orderProblem(new ApiError(403, "x", [], { blocked: true }))).toMatch(/not taking messages from you/);
    expect(orderProblem(new ApiError(409, "x", [], { area_off: true }))).toMatch(/not taking orders/);
    expect(orderProblem(new ApiError(500, "x"))).toBeNull();
  });

  it("shows an earlier order short: no summary, only what it was", () => {
    const e = { ...row("OLD-1"), summary: "", earlier: true };
    expect(orderLine(e, "12 Oct 2026")).toBe("Made before this number was yours · OLD-1 · 12 Oct 2026");
    expect(orderLine({ ...e, summary: "2 × Jollof rice" }, "")).toBe("Made before this number was yours · OLD-1");
  });

  it("names bookings of every kind", () => {
    expect(orderTitle(row("MK-1"))).toBe("Order MK-1");
    expect(orderTitle({ ...row("B-1"), kind: "booking" })).toBe("Booking B-1");
    expect(orderTitle({ ...row("H-1"), kind: "booking", booking_type: "hire" })).toBe("Hire H-1");
    expect(orderTitle({ ...row("T-1"), kind: "booking", booking_type: "tickets" })).toBe("Tickets T-1");
    expect(orderTitle({ ...row("C-1"), kind: "booking", booking_type: "class" })).toBe("Class booking C-1");
    expect(orderTitle({ ...row("S-1"), kind: "booking", booking_type: "space" })).toBe("Space booking S-1");
    expect(orderLine({ ...row("H-1"), kind: "booking", booking_type: "hire", summary: "2 × Canopy, 3 days" }, "")).toBe("2 × Canopy, 3 days · H-1");
    expect(orderLine({ ...row("H-2"), kind: "booking", booking_type: "hire", summary: "" }, "")).toBe("A hire · H-2");
    expect(orderIcon({ kind: "booking", booking_type: "tickets" })).toBe("tag");
    expect(orderIcon({ kind: "booking", booking_type: "hire" })).toBe("calendar");
    expect(orderIcon({ kind: "order" })).toBe("orders");
  });

  it("says a booking's times, with the end's day when it is another", () => {
    const day = (t: string) => t.slice(0, 10);
    const time = (t: string) => t.slice(11, 16);
    expect(bookingWhen("2026-11-20T09:00", "2026-11-20T11:00", day, time)).toBe("2026-11-20, 09:00 to 11:00");
    expect(bookingWhen("2026-11-20T09:00", "2026-11-23T09:00", day, time)).toBe("2026-11-20, 09:00 to 2026-11-23, 09:00");
    expect(bookingWhen("2026-11-20T09:00", undefined, day, time)).toBe("2026-11-20, 09:00");
  });

  it("freshens kept pages with the newest page read again", () => {
    const kept = [row("A"), row("B"), row("C")];
    const newest = [row("N"), { ...row("A"), state: "paid" }, row("B")];
    const out = freshen(kept, newest);
    expect(out.map((o) => o.reference)).toEqual(["N", "A", "B", "C"]);
    expect(out[1]?.state).toBe("paid");
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

  it("saves a typed address at checkout only when it can be kept", () => {
    expect(keepProblem("12 Allen Avenue", 3)).toBeNull();
    expect(keepProblem("x".repeat(201), 3)).toMatch(/at most 200 letters/);
    expect(keepProblem(` ${"x".repeat(200)} `, 3)).toBeNull();
    expect(keepProblem("12 Allen Avenue", 20)).toMatch(/up to 20 addresses/);
  });

  it("chooses the default until the person types, and then keeps what they typed", () => {
    const list = [
      { id: "a", default: false },
      { id: "b", default: true },
    ];
    expect(chosenAddress(undefined, list, false)).toBe("b");
    expect(chosenAddress(undefined, [{ id: "a", default: false }], false)).toBe("a");
    expect(chosenAddress(undefined, [], false)).toBe("");
    // Typed before the saved ones loaded: the typed one stays.
    expect(chosenAddress(undefined, list, true)).toBe("");
    expect(chosenAddress("a", list, true)).toBe("a");
    expect(chosenAddress("", list, false)).toBe("");
  });

  it("says the limit and a bad phone plainly", () => {
    expect(addressProblem(new ApiError(409, "x"))).toBe("You can keep up to 20 addresses. Delete one you no longer use first.");
    expect(addressProblem(new ApiError(400, "x", [], { phone_invalid: true }))).toMatch(/phone number does not look right/);
    expect(addressProblem(new ApiError(400, "The street is too long."))).toBeNull();
  });
});
