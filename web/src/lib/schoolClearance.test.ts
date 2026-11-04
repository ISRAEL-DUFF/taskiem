import { describe, expect, it } from "vitest";
import {
  GATE_WORDS,
  RECENT_MAX,
  canSearch,
  checkTime,
  clearancePath,
  clearanceStatus,
  hasAmountField,
  loadRecent,
  overrideLine,
  overrideProblem,
  reasonWords,
  remember,
  saveRecent,
  totalsLine,
  type ClearanceOverride,
  type GateAnswer,
} from "./schoolClearance";

const override: ClearanceOverride = {
  id: "o1",
  student_id: "s1",
  admission_number: "GCA/003",
  term_id: "t1",
  term: "First term 2026/2027",
  reason: "Bereavement in the family",
  starts_on: "2026-10-20",
  ends_on: "2026-12-11",
  created_by: "user:bola",
  created_at: "2026-10-20T09:00:00Z",
  in_force: true,
};

describe("the words", () => {
  it("names each status with a tone, and an unknown one plainly", () => {
    expect(clearanceStatus("cleared")).toEqual({ label: "Cleared", tone: "ok" });
    expect(clearanceStatus("partly_paid").label).toBe("Partly paid");
    expect(clearanceStatus("not_paid").tone).toBe("danger");
    expect(clearanceStatus("odd")).toEqual({ label: "odd", tone: "muted" });
  });
  it("says why, with the override's end and the plan's next instalment", () => {
    expect(reasonWords({ reason: "paid" })).toBe("paid in full");
    expect(reasonWords({ reason: "balance" })).toBe("a balance is left");
    expect(reasonWords({ reason: "no_invoice" })).toContain("no invoice yet");
    expect(reasonWords({ reason: "override", override })).toBe("cleared by override until 11 Dec 2026");
    expect(reasonWords({ reason: "plan", plan: { next_due_on: "2026-11-15" } as never })).toBe("payment plan in good standing (next instalment 15 Nov 2026)");
  });
  it("sums the totals", () => {
    expect(totalsLine(undefined)).toBe("");
    expect(totalsLine({ students: 0, cleared: 0, partly_paid: 0, not_paid: 0 })).toBe("No students enrolled in this term.");
    expect(totalsLine({ students: 3, cleared: 1, partly_paid: 1, not_paid: 1 })).toBe("3 students: 1 cleared, 1 partly paid, 1 not paid.");
    expect(totalsLine({ students: 1, cleared: 1, partly_paid: 0, not_paid: 0 })).toBe("1 student: 1 cleared, 0 partly paid, 0 not paid.");
  });
  it("builds the list's path", () => {
    expect(clearancePath({})).toBe("/v1/schools/clearance");
    expect(clearancePath({ term: "t1", class: "c1" }, "csv", true)).toBe("/v1/schools/clearance?term=t1&class=c1&format=csv&reveal=true");
  });
  it("lines up an override", () => {
    expect(overrideLine(override)).toBe("First term 2026/2027: Bereavement in the family (20 Oct 2026 to 11 Dec 2026)");
    expect(overrideLine({ ...override, in_force: false })).toContain("; ended)");
    expect(overrideLine({ ...override, in_force: false, withdrawn_at: "2026-10-21T09:00:00Z" })).toContain("; withdrawn)");
  });
});

describe("an override's form", () => {
  const today = new Date(2026, 9, 24);
  it("needs a reason and a sensible end date", () => {
    expect(overrideProblem({ reason: "", ends_on: "" }, today)).toContain("reason");
    expect(overrideProblem({ reason: "x".repeat(301), ends_on: "" }, today)).toContain("300");
    expect(overrideProblem({ reason: "Bereavement", ends_on: "" }, today)).toBe("");
    expect(overrideProblem({ reason: "Bereavement", ends_on: "2026-10-24" }, today)).toBe("");
    expect(overrideProblem({ reason: "Bereavement", ends_on: "2026-10-23" }, today)).toContain("past");
    expect(overrideProblem({ reason: "Bereavement", ends_on: "2027-10-25" }, today)).toContain("within a year");
    expect(overrideProblem({ reason: "Bereavement", ends_on: "soon" }, today)).toContain("YYYY-MM-DD");
  });
});

describe("the gate", () => {
  const ebube: GateAnswer = { student_id: "s1", name: "Ebube Eze", class: "JSS 1 A", status: "cleared" };
  const chidi: GateAnswer = { student_id: "s2", name: "Chidi Eze", class: "JSS 1 A", status: "see_bursar" };
  it("searches from three letters", () => {
    expect(canSearch("")).toBe(false);
    expect(canSearch("eb")).toBe(false);
    expect(canSearch("  eb  ")).toBe(false);
    expect(canSearch("ebu")).toBe(true);
    expect(canSearch("a  b")).toBe(true);
  });
  it("answers in two words and never an amount", () => {
    expect(GATE_WORDS.cleared.label).toBe("CLEARED");
    expect(GATE_WORDS.see_bursar.label).toBe("SEE THE BURSAR");
    expect(hasAmountField(ebube)).toBe(false);
    expect(hasAmountField({ students: [ebube, chidi] })).toBe(false);
    expect(hasAmountField({ ...ebube, balance_kobo: 0 })).toBe(true);
    expect(hasAmountField({ students: [{ ...chidi, invoice: { number: "INV-1" } }] })).toBe(true);
  });
  it("remembers recent checks, newest first, once per child, a few at most", () => {
    let list = remember([], ebube, "2026-10-24T07:00:00Z");
    list = remember(list, chidi, "2026-10-24T07:01:00Z");
    expect(list.map((r) => r.name)).toEqual(["Chidi Eze", "Ebube Eze"]);
    list = remember(list, ebube, "2026-10-24T07:02:00Z");
    expect(list.map((r) => r.name)).toEqual(["Ebube Eze", "Chidi Eze"]);
    for (let i = 0; i < 20; i++) list = remember(list, { ...ebube, student_id: `x${i}` });
    expect(list).toHaveLength(RECENT_MAX);
  });
  it("keeps them in session storage and reads back only what it wrote", () => {
    const store = new Map<string, string>();
    const storage = { getItem: (k: string) => store.get(k) ?? null, setItem: (k: string, v: string) => void store.set(k, v), removeItem: (k: string) => void store.delete(k) };
    expect(loadRecent(storage)).toEqual([]);
    saveRecent(remember([], ebube, "2026-10-24T07:00:00Z"), storage);
    expect(loadRecent(storage)).toHaveLength(1);
    store.set("taskiem-gate-recent", '[{"student_id":"s9","status":"rich"}]');
    expect(loadRecent(storage)).toEqual([]);
    store.set("taskiem-gate-recent", "not json");
    expect(loadRecent(storage)).toEqual([]);
    expect(loadRecent(undefined)).toEqual([]);
  });
  it("shows a check's time", () => {
    expect(checkTime("nope")).toBe("");
    expect(checkTime(new Date(2026, 9, 24, 7, 5).toISOString())).toBe("07:05");
  });
});
