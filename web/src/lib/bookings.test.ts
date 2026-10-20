import { describe, expect, it } from "vitest";
import {
  addDays,
  availabilityErrors,
  availabilityFromForm,
  availabilityToForm,
  bookingsPath,
  canChange,
  cleanServiceColumns,
  dayLabel,
  formatDuration,
  groupByDay,
  guessServiceColumns,
  lagosDay,
  lagosInstant,
  lagosTime,
  moveProblem,
  noticeMinutes,
  noticeToForm,
  serviceLine,
  servicePrice,
  servicesProblem,
  servicesTables,
  timeSpan,
  upcomingOff,
  whenLabel,
  type AvailabilityForm,
} from "./bookings";
import type { Order } from "./orders";
import type { Column, Table } from "./tables";

const col = (name: string, type: Column["type"], personal = false): Column => ({ name, title: "", type, required: false, personal });
const table = (columns: Column[]): Table => ({
  id: "t1",
  name: "services",
  title: "Services",
  description: "",
  columns,
  postgres_table: "services",
  connection: "data_tables",
  created_by: "",
  created_at: "",
  updated_at: "",
});

describe("services columns", () => {
  it("guesses a services mapping from column names", () => {
    const t = table([
      col("service", "text"),
      col("price_kobo", "integer"),
      col("duration_minutes", "integer"),
      col("seats", "integer"),
      col("gap", "number"),
      col("active", "boolean"),
      col("image", "text"),
    ]);
    expect(guessServiceColumns(t)).toEqual({
      name: "service",
      duration: "duration_minutes",
      price: "price_kobo",
      price_unit: "kobo",
      photo: "image",
      available: "active",
      capacity: "seats",
      buffer: "gap",
    });
  });

  it("knows the other names for a duration, capacity and buffer", () => {
    for (const d of ["duration", "minutes", "length", "mins"]) expect(guessServiceColumns(table([col("name", "text"), col(d, "number")])).duration).toBe(d);
    for (const c of ["capacity", "spaces", "places"]) expect(guessServiceColumns(table([col("name", "text"), col(c, "integer")])).capacity).toBe(c);
    for (const b of ["buffer", "buffer_minutes"]) expect(guessServiceColumns(table([col("name", "text"), col(b, "integer")])).buffer).toBe(b);
  });

  it("never guesses a duration from a column that does not say so, nor from text or personal columns", () => {
    expect(guessServiceColumns(table([col("name", "text"), col("price", "number")]))).toEqual({ name: "name", price: "price", price_unit: "naira" });
    expect(guessServiceColumns(table([col("name", "text"), col("duration", "text")])).duration).toBeUndefined();
    expect(guessServiceColumns(table([col("name", "text"), col("duration", "integer", true)])).duration).toBeUndefined();
  });

  it("lists the tables services can come from", () => {
    expect(servicesTables([table([col("name", "text"), col("duration", "integer")]), table([col("name", "text")]), table([col("who", "text", true), col("n", "integer")])])).toHaveLength(1);
  });

  it("leaves out empty fields and stock", () => {
    expect(cleanServiceColumns({ name: "name", duration: "mins", price: "", price_unit: "kobo", buffer: "" })).toEqual({ name: "name", duration: "mins" });
    expect(cleanServiceColumns({ name: "name", duration: "mins", price: "cost", price_unit: "" })).toEqual({ name: "name", duration: "mins", price: "cost", price_unit: "naira" });
    expect(cleanServiceColumns({ name: "n", duration: "d", stock: "qty" } as never)).toEqual({ name: "n", duration: "d" });
  });

  it("asks for the name and duration columns", () => {
    expect(servicesProblem(null, {})).toBeNull();
    expect(servicesProblem("t1", { duration: "d" })).toMatch(/name/);
    expect(servicesProblem("t1", { name: "n" })).toMatch(/how long/);
    expect(servicesProblem("t1", { name: "n", duration: "d" })).toBeNull();
  });
});

describe("availability", () => {
  const card = { days: [[{ open: "09:00", close: "17:00" }], [{ open: "18:00", close: "02:00" }], null, null, null, null, null] };

  it("reads unset booking hours as the card's, without spans past midnight", () => {
    const f = availabilityToForm({ days: [[], [], [], [], [], [], []] }, card);
    expect(f.sameAsHours).toBe(true);
    expect(f.days[0]).toEqual({ open: true, spans: [{ open: "09:00", close: "17:00" }] });
    expect(f.days[1]?.open).toBe(false);
    expect(availabilityFromForm(f)).toEqual({ days: [[], [], [], [], [], [], []] });
    expect(availabilityToForm(undefined).sameAsHours).toBe(true);
  });

  it("round-trips its own hours, days off and window", () => {
    const api = {
      days: [[{ open: "09:00", close: "13:00" }, { open: "14:00", close: "18:00" }], null, null, null, null, [{ open: "10:00", close: "16:00" }], null],
      off: ["2026-12-26", "2026-12-25"],
      ahead_days: 60,
      soonest_minutes: 90,
      cutoff_minutes: 1440,
      auto_confirm: true,
    };
    const f = availabilityToForm(api, card);
    expect(f.sameAsHours).toBe(false);
    expect(f.soonest).toEqual({ value: "90", unit: "minutes" });
    expect(f.cutoff).toEqual({ value: "24", unit: "hours" });
    expect(availabilityFromForm(f)).toEqual({
      days: [[{ open: "09:00", close: "13:00" }, { open: "14:00", close: "18:00" }], [], [], [], [], [{ open: "10:00", close: "16:00" }], []],
      off: ["2026-12-25", "2026-12-26"],
      ahead_days: 60,
      soonest_minutes: 90,
      cutoff_minutes: 1440,
      auto_confirm: true,
    });
  });

  it("leaves the defaults out", () => {
    const f = availabilityToForm({ days: [[{ open: "09:00", close: "17:00" }], [], [], [], [], [], []] });
    expect(f.ahead).toBe("");
    expect(f.soonest.value).toBe("");
    expect(availabilityFromForm(f)).toEqual({ days: [[{ open: "09:00", close: "17:00" }], [], [], [], [], [], []] });
  });

  it("empties every day when it is the same as the opening hours", () => {
    const f = availabilityToForm({ days: [[{ open: "09:00", close: "17:00" }], [], [], [], [], [], []] });
    expect(availabilityFromForm({ ...f, sameAsHours: true }).days).toEqual([[], [], [], [], [], [], []]);
  });

  it("reads notice in hours or minutes", () => {
    expect(noticeToForm(120)).toEqual({ value: "2", unit: "hours" });
    expect(noticeToForm(45)).toEqual({ value: "45", unit: "minutes" });
    expect(noticeToForm(0)).toEqual({ value: "", unit: "hours" });
    expect(noticeMinutes({ value: "1.5", unit: "hours" })).toBe(90);
    expect(noticeMinutes({ value: " ", unit: "hours" })).toBe(0);
    expect(Number.isNaN(noticeMinutes({ value: "soon", unit: "minutes" }))).toBe(true);
  });

  const base = (): AvailabilityForm => availabilityToForm({ days: [[{ open: "09:00", close: "17:00" }], [], [], [], [], [], []] });
  const withMonday = (spans: { open: string; close: string }[]): AvailabilityForm => {
    const f = base();
    return { ...f, days: f.days.map((d, i) => (i === 0 ? { open: true, spans } : d)) };
  };

  it("refuses what the server refuses", () => {
    expect(availabilityErrors(base())).toEqual([]);
    expect(availabilityErrors(withMonday([{ open: "9:00", close: "17:00" }]))[0]?.message).toMatch(/Monday's start and end/);
    expect(availabilityErrors(withMonday([{ open: "18:00", close: "02:00" }]))[0]?.message).toMatch(/end after they start, the same day/);
    expect(availabilityErrors(withMonday([{ open: "10:00", close: "10:00" }]))).toHaveLength(1);
    const four = Array.from({ length: 4 }, (_, i) => ({ open: `1${i}:00`, close: `1${i}:30` }));
    expect(availabilityErrors(withMonday(four))[0]?.message).toMatch(/At most 3/);
    expect(availabilityErrors({ ...base(), off: ["25/12/2026"] })[0]?.message).toMatch(/not a date/);
    expect(availabilityErrors({ ...base(), ahead: "181" })[0]?.message).toMatch(/1 to 180/);
    expect(availabilityErrors({ ...base(), ahead: "0" })).toHaveLength(1);
    expect(availabilityErrors({ ...base(), soonest: { value: "200", unit: "hours" } })[0]?.message).toMatch(/at most a week/);
    expect(availabilityErrors({ ...base(), cutoff: { value: "0", unit: "minutes" } })[0]?.message).toMatch(/at least 1 minute/);
  });

  it("asks for a day when it is not the opening hours", () => {
    const f = base();
    const closed = { ...f, days: f.days.map((d) => ({ ...d, open: false })) };
    expect(availabilityErrors(closed)[0]?.message).toMatch(/at least one day/);
    // The same as the opening hours, the days are not checked.
    expect(availabilityErrors({ ...withMonday([{ open: "", close: "" }]), sameAsHours: true })).toEqual([]);
  });

  it("sets past days off aside", () => {
    expect(upcomingOff(["2026-01-01", "2026-10-20", "2026-12-25"], "2026-10-20")).toEqual({ upcoming: ["2026-10-20", "2026-12-25"], past: 1 });
  });
});

describe("services as the preview reads them", () => {
  it("says how long", () => {
    expect(formatDuration(45)).toBe("45 min");
    expect(formatDuration(60)).toBe("1 hour");
    expect(formatDuration(90)).toBe("1 hour 30 min");
    expect(formatDuration(120)).toBe("2 hours");
    expect(formatDuration(0)).toBe("");
  });

  it("prices on the day when there is no price", () => {
    expect(servicePrice({ price_kobo: 500000 })).toBe("₦5,000");
    expect(servicePrice({})).toBe("Price on the day");
    expect(servicePrice({ price_kobo: 0 })).toBe("₦0");
  });

  it("shows the capacity only above one", () => {
    expect(serviceLine({ id: "1", name: "Haircut", available: true, duration_minutes: 30, price_kobo: 300000, capacity: 1 })).toBe("30 min · ₦3,000");
    expect(serviceLine({ id: "2", name: "Yoga", available: true, duration_minutes: 60, capacity: 10 })).toBe("1 hour · Price on the day · 10 at once");
  });
});

describe("Lagos time", () => {
  it("reads a moment in Lagos", () => {
    // 23:30 UTC is 00:30 the next day in Lagos.
    expect(lagosDay("2026-10-23T23:30:00Z")).toBe("2026-10-24");
    expect(lagosTime("2026-10-24T09:30:00Z")).toBe("10:30");
    expect(dayLabel("2026-10-24")).toBe("Sat 24 Oct");
    expect(whenLabel("2026-10-24T09:30:00Z")).toBe("Sat 24 Oct, 10:30");
    expect(lagosInstant("2026-10-24", "10:30")).toBe("2026-10-24T10:30:00+01:00");
    expect(Date.parse(lagosInstant("2026-10-24", "10:30"))).toBe(Date.parse("2026-10-24T09:30:00Z"));
    expect(addDays("2026-12-30", 3)).toBe("2027-01-02");
  });

  it("asks for a range from today in Lagos", () => {
    const now = Date.parse("2026-10-19T23:30:00Z"); // 00:30 on the 20th in Lagos
    expect(bookingsPath("week", now)).toBe("/v1/storefront/bookings?from=2026-10-20&to=2026-10-27");
    expect(bookingsPath("month", now)).toBe("/v1/storefront/bookings?from=2026-10-20&to=2026-11-20");
    expect(bookingsPath("", now)).toBe(bookingsPath("month", now));
  });
});

describe("the bookings list", () => {
  const b = (ref: string, starts: string, status = "accepted"): Order => ({
    id: ref,
    reference: ref,
    kind: "booking",
    status: status as Order["status"],
    customer: {},
    basket: null,
    total_kobo: 0,
    payment: "none",
    created_at: "2026-10-01T00:00:00Z",
    starts_at: starts,
    ends_at: new Date(Date.parse(starts) + 45 * 60_000).toISOString(),
  });

  it("groups by Lagos day, soonest first", () => {
    const g = groupByDay([b("C", "2026-10-25T08:00:00Z"), b("A", "2026-10-24T09:30:00Z"), b("B", "2026-10-23T23:30:00Z")]);
    expect(g.map((x) => [x.label, x.bookings.map((y) => y.reference)])).toEqual([
      ["Sat 24 Oct", ["B", "A"]],
      ["Sun 25 Oct", ["C"]],
    ]);
  });

  it("shows the time span", () => {
    expect(timeSpan(b("A", "2026-10-24T09:30:00Z"))).toBe("10:30–11:15");
    expect(timeSpan({ starts_at: "2026-10-24T09:30:00Z" })).toBe("10:30");
  });

  it("changes only a booking that holds its time", () => {
    expect(canChange(b("A", "2026-10-24T09:30:00Z"))).toBe(true);
    expect(canChange(b("A", "2026-10-24T09:30:00Z", "awaiting_payment"))).toBe(true);
    expect(canChange(b("A", "2026-10-24T09:30:00Z", "cancelled"))).toBe(false);
    expect(canChange(b("A", "2026-10-24T09:30:00Z", "no_show"))).toBe(false);
    expect(canChange({ kind: "order", status: "accepted" })).toBe(false);
  });

  it("asks for a date and time to move to", () => {
    expect(moveProblem("", "10:00")).toMatch(/date/);
    expect(moveProblem("2026-10-24", "")).toMatch(/time/);
    expect(moveProblem("2026-10-24", "10:00")).toBeNull();
  });
});
