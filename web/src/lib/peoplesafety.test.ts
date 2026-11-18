import { describe, expect, it } from "vitest";
import {
  actionHelp,
  actionIsGrave,
  actionOperation,
  appealWords,
  bytesWords,
  checkReasonLabel,
  heldFileURL,
  isPhoto,
  lagosDay,
  periodWords,
  reportFileURL,
  reportReasonLabel,
  standingTitle,
  whyOK,
  withCurrentHalf,
} from "./peoplesafety";

describe("the moderators' screens", () => {
  it("names reasons in plain words", () => {
    expect(reportReasonLabel("scam")).toBe("A scam or fraud");
    expect(reportReasonLabel("weird")).toBe("weird");
    expect(checkReasonLabel("code")).toBe("Asks for a code, PIN, BVN or card");
    expect(checkReasonLabel("new_thing")).toBe("new thing");
  });

  it("asks for a reason of 3 to 500 characters before opening anything", () => {
    expect(whyOK("  ab ")).toBe(false);
    expect(whyOK("abc")).toBe(true);
    expect(whyOK("x".repeat(500))).toBe(true);
    expect(whyOK("x".repeat(501))).toBe(false);
  });

  it("sends the reason with every file it opens", () => {
    expect(reportFileURL("r1", "m1", "Look at it")).toBe("/v1/ops/people/reports/r1/files/m1?why=Look%20at%20it");
    expect(heldFileURL("h1", "a&b")).toBe("/v1/ops/people/held/h1/file?why=a%26b");
  });

  it("binds each action to its step-up and asks before the grave ones", () => {
    expect(actionOperation("suspend")).toBe("ops.people.suspend");
    expect(actionIsGrave("suspend") && actionIsGrave("close")).toBe(true);
    expect(actionIsGrave("warn") || actionIsGrave("limit")).toBe(false);
    expect(actionHelp("close")).toContain("30 days");
  });

  it("says sizes and which files are photos", () => {
    expect(bytesWords(512)).toBe("512 bytes");
    expect(bytesWords(2048)).toBe("2 KB");
    expect(bytesWords(3 * 1024 * 1024)).toBe("3.0 MB");
    expect(bytesWords(1.5 * 1024 ** 3)).toBe("1.5 GB");
    expect(isPhoto("image/png")).toBe(true);
    expect(isPhoto("image/svg+xml")).toBe(false);
    expect(isPhoto("application/pdf")).toBe(false);
  });
});

describe("the person's standing", () => {
  it("titles each state", () => {
    expect(standingTitle("ok")).toBe("Your account is in good standing");
    expect(standingTitle("suspended")).toBe("Your account can't message people for now");
  });
  it("says what became of an appeal", () => {
    const a = { id: "a", text: "t", at: "", decided_at: null, note: null };
    expect(appealWords({ ...a, state: "open" })).toContain("A moderator will read it");
    expect(appealWords({ ...a, state: "upheld", note: "Spam" })).toBe("A moderator looked at your appeal and kept the decision. They said: Spam");
    expect(appealWords({ ...a, state: "overturned" })).toContain("lifted the decision");
  });
});

describe("lawful requests", () => {
  it("turns a day into its start in Lagos, and the day after for an end", () => {
    expect(lagosDay("2026-11-18")).toBe("2026-11-17T23:00:00.000Z");
    expect(lagosDay("2026-11-18", true)).toBe("2026-11-18T23:00:00.000Z");
    expect(lagosDay("18/11/2026")).toBeUndefined();
  });
  it("puts the current half-year first, at zero", () => {
    const p = { year: 2026, half: 1 as const, label: "January to June 2026", received: 2, answered: 1, refused: 1, pending: 0 };
    const out = withCurrentHalf([p], new Date("2026-11-18T10:00:00Z"));
    expect(out.map((x) => x.label)).toEqual(["July to December 2026", "January to June 2026"]);
    expect(withCurrentHalf([p], new Date("2026-03-01T00:00:00Z"))).toEqual([p]);
    // 31 December 23:30 UTC is already January in Lagos.
    expect(withCurrentHalf([], new Date("2026-12-31T23:30:00Z"))[0].label).toBe("January to June 2027");
  });
  it("says a half-year's figures", () => {
    expect(periodWords({ year: 2026, half: 2, label: "", received: 0, answered: 0, refused: 0, pending: 0 })).toBe("No requests.");
    expect(periodWords({ year: 2026, half: 2, label: "", received: 3, answered: 1, refused: 1, pending: 1 })).toBe(
      "3 received, 1 answered, 1 refused, 1 still being looked at.",
    );
  });
});
