import { describe, expect, it } from "vitest";
import { dueWords, emptyTask, lagosTime, linkedWhat, repeatWords, taskBadge, taskProblem, taskRequest, taskSub, taskWho, tasksTile, type StaffTask } from "./tasks";

const now = new Date("2026-11-09T09:00:00+01:00");
const task = (over: Partial<StaffTask> = {}): StaffTask => ({
  id: "t1",
  what: "count the cash float",
  given_by: "u1",
  giver: "Ada",
  to_user: "u2",
  person: "Bisi",
  photo: false,
  state: "open",
  created_at: "2026-11-09T07:00:00Z",
  late: false,
  ...over,
});

describe("tasks", () => {
  it("says who a task is for", () => {
    expect(taskWho(task())).toBe("Bisi");
    expect(taskWho(task({ to_user: undefined, person: undefined, to_role: "shift_manager" }))).toBe("any shift manager");
    expect(taskWho(task({ to_user: undefined, to_role: "cashier", taken_by: "u2" }))).toBe("Bisi (any cashier)");
  });

  it("says when a task is due, in Lagos", () => {
    expect(dueWords("2026-11-09T15:00:00+01:00", now)).toBe("by 15:00");
    expect(dueWords("2026-11-10T07:30:00+01:00", now)).toBe("by tomorrow 07:30");
    expect(dueWords("2026-11-08T18:00:00+01:00", now)).toBe("by yesterday 18:00");
    expect(dueWords("2026-11-12T10:00:00+01:00", now)).toBe("by Thu 12 Nov, 10:00");
  });

  it("says a row's line for the person and for the giver", () => {
    const t = task({ due_at: "2026-11-09T15:00:00+01:00", photo: true });
    expect(taskSub(t, true, now)).toBe("by 15:00 · photo when done · from Ada");
    expect(taskSub(t, false, now)).toBe("Bisi · by 15:00 · photo when done");
    expect(taskSub(task({ state: "cant", cant_why: "no fuel" }), false, now)).toBe(`Bisi · can't: "no fuel"`);
    expect(taskSub(task({ to_user: undefined, person: undefined, to_role: "cashier" }), true, now)).toBe("for any cashier · from Ada");
  });

  it("badges done, can't, called off and late", () => {
    expect(taskBadge(task())).toBeNull();
    expect(taskBadge(task({ late: true }))?.text).toBe("Late");
    expect(taskBadge(task({ state: "done" }))?.tone).toBe("ok");
    expect(taskBadge(task({ state: "cant" }))?.text).toBe("Can't");
    expect(taskBadge(task({ state: "cancelled" }))?.text).toBe("Called off");
  });

  it("says repeats", () => {
    expect(repeatWords({ every: "day", at: "07:00" })).toBe("Every day at 07:00");
    expect(repeatWords({ every: "week", day: "mon", at: "09:00" })).toBe("Every Monday at 09:00");
  });

  it("checks the form and builds the request", () => {
    const f = emptyTask("2026-11-09");
    expect(taskProblem(f)).toBe("Say what is to be done.");
    expect(taskProblem({ ...f, what: "clean" })).toBe("Choose who it is for.");
    expect(taskProblem({ ...f, what: "clean", who: "user:u2", when: "by" })).toBe("Choose the time it is due.");
    const ok = { ...f, what: " clean the freezer ", who: "user:u2", when: "by" as const, time: "17:00", photo: true };
    expect(taskProblem(ok)).toBeNull();
    expect(taskRequest(ok)).toEqual({ what: "clean the freezer", photo: true, to_user: "u2", due_at: "2026-11-09T16:00:00.000Z" });
    expect(taskRequest({ ...ok, who: "role:cashier", when: "week", day: "fri", time: "08:00" }, { kind: "order", id: "P-1" })).toEqual({
      what: "clean the freezer",
      photo: true,
      to_role: "cashier",
      repeat: { every: "week", day: "fri", at: "08:00" },
      link: { kind: "order", id: "P-1" },
    });
    expect(lagosTime("2026-11-09", "00:30")).toBe("2026-11-08T23:30:00.000Z");
  });

  it("starts a linked task's words, and the briefing tile", () => {
    expect(linkedWhat({ kind: "order", id: "x", name: "P-7K8YED" })).toBe("Pack order P-7K8YED");
    expect(linkedWhat({ kind: "stock", id: "s1", name: "Rice, 50kg bag" })).toBe("Restock Rice, 50kg bag");
    expect(tasksTile({ today: 3, late: 1 })).toBe("3 today · 1 late");
    expect(tasksTile({ today: 0, late: 2 })).toBe("2 late");
  });
});
