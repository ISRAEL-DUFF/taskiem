import { describe, expect, it } from "vitest";
import {
  accountIDProblem,
  actionName,
  anyActionLabel,
  GROUP_ACTION_LABELS,
  groupActionHelp,
  groupActionOperation,
  groupActionRequest,
  actionHelp,
  actionIsGrave,
  actionOperation,
  appealWords,
  bytesWords,
  currentHalf,
  currentHalfWords,
  endedHalves,
  checkReasonLabel,
  heldFileURL,
  isAccountID,
  isPhoto,
  lagosDay,
  periodWords,
  reportFileURL,
  reportReasonLabel,
  standingNoticeShows,
  standingTitle,
  whyOK,
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
  it("checks account ids before the passkey is asked for", () => {
    expect(isAccountID("0b6f3c1e-2a4d-4c8e-9f10-123456789abc")).toBe(true);
    expect(isAccountID(" 0B6F3C1E-2A4D-4C8E-9F10-123456789ABC ")).toBe(true);
    expect(isAccountID("dayo-ola")).toBe(false);
    expect(isAccountID("")).toBe(false);
    expect(accountIDProblem("")).toBeUndefined();
    expect(accountIDProblem("0b6f3c1e-2a4d-4c8e-9f10-123456789abc")).toBeUndefined();
    expect(accountIDProblem("12345")).toMatch(/not an account id/);
  });
  it("turns a day into its start in Lagos, and the day after for an end", () => {
    expect(lagosDay("2026-11-18")).toBe("2026-11-17T23:00:00.000Z");
    expect(lagosDay("2026-11-18", true)).toBe("2026-11-18T23:00:00.000Z");
    expect(lagosDay("18/11/2026")).toBeUndefined();
  });
  it("shows only half-years that have ended, latest first, and says when the current one shows", () => {
    const h1 = { year: 2026, half: 1 as const, label: "January to June 2026", received: 2, answered: 1, refused: 1 };
    const h2 = { year: 2026, half: 2 as const, label: "July to December 2026", received: 1, answered: 0, refused: 0 };
    const old = { year: 2025, half: 2 as const, label: "July to December 2025", received: 0, answered: 0, refused: 0 };
    // In November 2026 the second half is not over: left out even when sent.
    expect(endedHalves([old, h2, h1], new Date("2026-11-18T10:00:00Z")).map((x) => x.label)).toEqual(["January to June 2026", "July to December 2025"]);
    expect(currentHalfWords(new Date("2026-11-18T10:00:00Z"))).toBe("July to December 2026 shows here once it ends.");
    // 31 December 23:30 UTC is already January in Lagos: the second half has ended.
    expect(endedHalves([h2], new Date("2026-12-31T23:30:00Z"))).toEqual([h2]);
    expect(currentHalf(new Date("2026-12-31T23:30:00Z")).label).toBe("January to June 2027");
    expect(endedHalves([], new Date("2026-03-01T00:00:00Z"))).toEqual([]);
  });
  it("says a half-year's figures, with no count still being looked at", () => {
    expect(periodWords({ received: 0, answered: 0, refused: 0 })).toBe("No requests.");
    expect(periodWords({ received: 3, answered: 1, refused: 1 })).toBe("3 received, 1 answered, 1 refused.");
    // A count the server still sends with `pending` is read the same.
    expect(periodWords({ received: 3, answered: 1, refused: 1, pending: 1 } as { received: number; answered: number; refused: number })).toBe("3 received, 1 answered, 1 refused.");
  });
});

describe("the notice of Your standing", () => {
  it("shows while something is unseen, not on Your standing itself", () => {
    expect(standingNoticeShows({ unseen: 1 }, "/you")).toBe(true);
    expect(standingNoticeShows({ unseen: 0 }, "/you")).toBe(false);
    expect(standingNoticeShows(undefined, "/you")).toBe(false);
    expect(standingNoticeShows({ unseen: 2 }, "/you/settings/standing")).toBe(false);
  });
});

describe("groups", () => {
  it("names a group's actions and binds them to their step-up", () => {
    expect(GROUP_ACTION_LABELS.group_close).toBe("Close the group");
    expect(groupActionOperation("group_link_off")).toBe("ops.people.group.link_off");
    expect(groupActionRequest("group_close")).toBe("close");
    expect(anyActionLabel("group_link_off")).toBe("Turn off its invite link");
    expect(anyActionLabel("suspend")).toBe("Suspend");
    expect(actionName("group_close")).toBe("Group closure");
    expect(groupActionHelp("group_close")).toContain("appeal");
  });
});
