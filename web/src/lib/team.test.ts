import { describe, expect, it } from "vitest";
import { figureRows, lengthWords, shiftWords, staffTile, emptyStaff, inviteSub, payWords, personSub, profileForm, profileProblem, profileRequest, roleWords, rotaWords, staffProblem } from "./team";

describe("staff", () => {
  it("says roles as people do", () => {
    expect(roleWords("shift_manager")).toBe("Shift manager");
    expect(roleWords("cashier")).toBe("Cashier");
    expect(roleWords("payroll_approver")).toBe("Payroll approver");
  });
  it("checks the form", () => {
    expect(staffProblem(emptyStaff())).toBe("Give their name.");
    expect(staffProblem({ name: "Tunde", phone: "080", role: "cashier" })).toBe("Give their mobile number, like 0803 123 4567.");
    expect(staffProblem({ name: "Tunde", phone: "0803 123 4567", role: "" })).toBe("Choose what they do.");
    expect(staffProblem({ name: "Tunde", phone: "0803 123 4567", role: "cashier" })).toBeNull();
  });
  it("lines up a person and an invitation", () => {
    expect(personSub({ user_id: "u", name: "Tunde", roles: ["shift_manager"], phone: "•••9402", chat: true })).toBe("Shift manager · •••9402");
    expect(personSub({ user_id: "u", name: "Ada", roles: ["owner"], email: "ada@x.test", chat: false })).toBe("Owner · ada@x.test");
    expect(inviteSub({ id: "i", phone: "+2348031239403", roles: ["cashier"], inviter: "Ada", expires_at: "2026-11-16T10:00:00Z", created_at: "2026-11-09T10:00:00Z" })).toBe(
      "Cashier · +2348031239403 · not sent yet",
    );
  });
});


describe("a person's profile", () => {
  it("says the usual hours, days in a row once", () => {
    expect(rotaWords([])).toBe("No usual hours set");
    expect(
      rotaWords([
        { day: "sat", start: "10:00", end: "18:00" },
        { day: "mon", start: "08:00", end: "16:00" },
        { day: "tue", start: "08:00", end: "16:00" },
        { day: "wed", start: "08:00", end: "16:00" },
        { day: "fri", start: "08:00", end: "16:00" },
      ]),
    ).toBe("Mon to Wed, 08:00 to 16:00; Fri, 08:00 to 16:00; Sat, 10:00 to 18:00");
  });
  it("says the pay", () => {
    expect(payWords({ pay_kobo: 8_000_000, pay_per: "month" }, (k) => `₦${k / 100}`)).toBe("₦80000 a month");
    expect(payWords({}, String)).toBe("");
  });
  it("goes to the form and back", () => {
    const f = profileForm({ title: "Head cashier", note: "", pay_kobo: 8_000_000, pay_per: "month", rota: [{ day: "mon", start: "08:00", end: "16:00" }] });
    expect(f.pay).toBe("80000");
    expect(f.days.mon.on).toBe(true);
    expect(f.days.tue.on).toBe(false);
    f.days.sat = { on: true, start: "20:00", end: "04:00" };
    f.pay = "85,000";
    expect(profileProblem(f)).toBeNull();
    expect(profileRequest(f)).toEqual({
      title: "Head cashier",
      started_on: undefined,
      note: "",
      pay_kobo: 8_500_000,
      pay_per: "month",
      rota: [
        { day: "mon", start: "08:00", end: "16:00" },
        { day: "sat", start: "20:00", end: "04:00" },
      ],
    });
    f.pay = "a lot";
    expect(profileProblem(f)).toBe("The pay is a number of naira, like 80000.");
    f.pay = "";
    f.days.sun = { on: true, start: "09:00", end: "09:00" };
    expect(profileProblem(f)).toBe("Sunday starts and ends at the same time.");
  });
});

describe("clocking in", () => {
  it("says a shift", () => {
    expect(lengthWords(8 * 3600_000 + 5 * 60_000)).toBe("8 hours 5 minutes");
    expect(lengthWords(45 * 60_000)).toBe("45 minutes");
    expect(lengthWords(3600_000)).toBe("1 hour");
    expect(shiftWords({ id: "s", user_id: "u", in_at: "2026-11-09T07:25:00Z" })).toBe("Since 08:25");
    expect(shiftWords({ id: "s", user_id: "u", in_at: "2026-11-09T07:25:00Z", out_at: "2026-11-09T15:30:00Z", out_how: "chat" })).toBe("08:25 to 16:30 · 8 hours 5 minutes");
    expect(shiftWords({ id: "s", user_id: "u", in_at: "2026-11-09T16:00:00Z", out_at: "2026-11-10T08:00:00Z", out_how: "auto" })).toBe("17:00 to 09:00 · 16 hours · closed by Taskiem");
  });
  it("says who is in on the briefing", () => {
    expect(staffTile({ in: [1, 2], late: [] })).toBe("2 in");
    expect(staffTile({ in: [], late: [1] })).toBe("0 in · 1 late");
  });

  it("says a person's figures", () => {
    const money = (k: number) => `N${k / 100}`;
    const f = {
      period: "week" as const,
      from: "2026-11-09",
      to: "2026-11-11",
      minutes: 485,
      shifts: 1,
      late_days: ["2026-11-09"],
      missed_days: [],
      sales: { count: 2, total_kobo: 23400000, by_how: { cash: 15600000, pos: 7800000 } },
      handovers: [{ at: "2026-11-09T16:00:00Z", diff_kobo: -600000 }],
      orders: 1,
      tasks: { done: 2, done_late: 1, cant: 0, open: 1, late: 0 },
    };
    expect(figureRows(f, money)).toEqual([
      ["Hours", "8 hours 5 minutes over 1 shift"],
      ["Time keeping", "Late on 1 day"],
      ["Sales", "N234000 in 2 sales (Cash N156000, POS N78000)"],
      ["Handovers", "1, N6000 short"],
      ["Orders worked", "1"],
      ["Tasks", "2 done (1 late), 1 open"],
    ]);
    const none = { ...f, minutes: 0, shifts: 0, late_days: [], missed_days: ["2026-11-10"], sales: { count: 0, total_kobo: 0, by_how: {} }, handovers: [], orders: 0, tasks: { done: 0, done_late: 0, cant: 0, open: 0, late: 0 } };
    expect(figureRows(none, money)).toEqual([
      ["Hours", "None clocked"],
      ["Time keeping", "Did not come on 1 day they were due"],
      ["Sales", "None recorded"],
      ["Orders worked", "None"],
      ["Tasks", "None"],
    ]);
  });
});
