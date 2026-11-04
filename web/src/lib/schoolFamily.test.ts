import { describe, expect, it } from "vitest";
import {
  claimLine,
  claimStatus,
  emptyRows,
  instalmentProblem,
  instalmentsLine,
  instalmentsRequest,
  linkDaysProblem,
  planStatus,
  rowsOf,
  sentLine,
} from "./schoolFamily";

describe("claims", () => {
  it("say what the parent said", () => {
    expect(claimLine({ amount_kobo: 4_000_000, how: "transfer", paid_on: "2026-10-20", reference: "GTB/1" })).toBe("₦40,000 by bank transfer on 20 Oct 2026, ref GTB/1");
    expect(claimLine({ amount_kobo: 250_050, how: "cash", paid_on: "2026-10-20" })).toBe("₦2,500.50 by cash on 20 Oct 2026");
  });
  it("have a state", () => {
    expect(claimStatus("open").label).toBe("To find");
    expect(claimStatus(undefined).label).toBe("To find");
    expect(claimStatus("matched").label).toBe("Found and counted");
    expect(claimStatus("dismissed").tone).toBe("muted");
  });
});

describe("plans", () => {
  it("list instalments with their total", () => {
    expect(instalmentsLine([{ amount_kobo: 5_000_000, due_on: "2026-11-24" }])).toBe("₦50,000 by 24 Nov 2026");
    expect(
      instalmentsLine([
        { amount_kobo: 5_000_000, due_on: "2026-11-24" },
        { amount_kobo: 6_800_000, due_on: "2026-12-24" },
      ]),
    ).toBe("₦50,000 by 24 Nov 2026; ₦68,000 by 24 Dec 2026 (₦118,000 in all)");
    expect(instalmentsLine(undefined)).toBe("");
  });
  it("check the rows and skip empty ones", () => {
    const rows = emptyRows(3);
    expect(instalmentProblem(rows)).toBe("Give at least one instalment.");
    rows[0] = { amount: "40,000", due_on: "2026-11-01" };
    rows[2] = { amount: "53000.5", due_on: "2026-12-01" };
    expect(instalmentProblem(rows)).toBeNull();
    expect(instalmentsRequest(rows)).toEqual([
      { amount_kobo: 4_000_000, due_on: "2026-11-01" },
      { amount_kobo: 5_300_050, due_on: "2026-12-01" },
    ]);
    rows[1] = { amount: "x", due_on: "2026-11-15" };
    expect(instalmentProblem(rows)).toBe("Each instalment needs an amount in naira.");
    rows[1] = { amount: "10", due_on: "" };
    expect(instalmentProblem(rows)).toBe("Each instalment needs a date.");
    rows[1] = { amount: "10", due_on: "2027-01-01" };
    expect(instalmentProblem(rows)).toBe("Instalments are in date order.");
    expect(instalmentProblem(emptyRows(7).map(() => ({ amount: "1", due_on: "2026-11-01" })))).toBe("A plan has at most six instalments.");
  });
  it("turn a request's instalments into rows", () => {
    expect(rowsOf([{ amount_kobo: 5_000_000, due_on: "2026-11-24" }])).toEqual([{ amount: "50000", due_on: "2026-11-24" }]);
  });
  it("have a state", () => {
    expect(planStatus("requested").label).toBe("Waiting for a decision");
    expect(planStatus("approved").tone).toBe("ok");
    expect(planStatus("declined").label).toBe("Declined");
  });
});

describe("links", () => {
  it("say where a link went", () => {
    expect(sentLine({ channels: { whatsapp: 1, sms: 1 }, expires_at: "2026-10-27T10:00:00Z" })).toBe("Sent on WhatsApp to 1 guardian and by SMS to 1 guardian. The link works until 27 Oct 2026.");
    expect(sentLine({ channels: {}, expires_at: "2026-10-27T10:00:00Z", nobody: true })).toContain("Nobody could be reached");
  });
  it("check the link life", () => {
    expect(linkDaysProblem("3")).toBeNull();
    expect(linkDaysProblem("30")).toBeNull();
    expect(linkDaysProblem("0")).toBe("Links work for 1 to 30 days.");
    expect(linkDaysProblem("31")).toBe("Links work for 1 to 30 days.");
    expect(linkDaysProblem("2.5")).toBe("Links work for 1 to 30 days.");
    expect(linkDaysProblem("")).toBe("Links work for 1 to 30 days.");
  });
});
