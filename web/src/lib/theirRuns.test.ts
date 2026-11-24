import { describe, expect, it } from "vitest";
import { forgetTheirRuns, keepTheirRun, keptTheirRuns, THEIR_RUNS_MAX, withTheirRuns, type KeptTheirRun } from "./theirRuns";

const mem = () => {
  const m = new Map<string, string>();
  return {
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
    key: (i: number) => [...m.keys()][i] ?? null,
    get length() {
      return m.size;
    },
    m,
  };
};
const run = (id: string, at: string): KeptTheirRun => ({ id, command: "order-status", name: "Order status", at });

describe("their runs kept in a chat", () => {
  it("keeps each run once, the latest thirty, by chat", () => {
    const s = mem();
    keepTheirRun("c1", run("r1", "2026-11-24T10:00:00Z"), s);
    keepTheirRun("c1", run("r1", "2026-11-24T10:00:00Z"), s);
    expect(keptTheirRuns("c1", s).map((r) => r.id)).toEqual(["r1"]);
    expect(keptTheirRuns("c2", s)).toEqual([]);
    for (let i = 0; i < THEIR_RUNS_MAX + 5; i++) keepTheirRun("c1", run(`x${i}`, "2026-11-24T11:00:00Z"), s);
    const kept = keptTheirRuns("c1", s);
    expect(kept).toHaveLength(THEIR_RUNS_MAX);
    expect(kept.at(-1)?.id).toBe(`x${THEIR_RUNS_MAX + 4}`);
  });

  it("reads nothing it cannot use", () => {
    const s = mem();
    s.setItem("taskiem_their_tool_runs:c1", "not json");
    expect(keptTheirRuns("c1", s)).toEqual([]);
    s.setItem("taskiem_their_tool_runs:c1", JSON.stringify([{ id: 1 }, run("ok", "2026-11-24T10:00:00Z")]));
    expect(keptTheirRuns("c1", s).map((r) => r.id)).toEqual(["ok"]);
    expect(keptTheirRuns("c1", undefined)).toEqual([]);
  });

  it("places each run in the conversation by its time", () => {
    type T = { id: string; created_at: string };
    const msgs: T[] = [
      { id: "m1", created_at: "2026-11-24T09:00:00Z" },
      { id: "m2", created_at: "2026-11-24T11:00:00Z" },
    ];
    const as = (r: KeptTheirRun): T => ({ id: `run:${r.id}`, created_at: r.at });
    const runs = [run("early", "2026-11-24T08:00:00Z"), run("mid", "2026-11-24T10:00:00Z"), run("late", "2026-11-24T12:00:00Z")];
    expect(withTheirRuns(msgs, runs, false, as).map((x) => x.id)).toEqual(["run:early", "m1", "run:mid", "m2", "run:late"]);
    // Older messages still to load: none placed above them.
    expect(withTheirRuns(msgs, runs, true, as).map((x) => x.id)).toEqual(["m1", "run:mid", "m2", "run:late"]);
    expect(withTheirRuns(msgs, [], false, as)).toBe(msgs);
  });

  it("never keeps what was typed, and forgets every chat's runs on signing out", () => {
    const s = mem();
    keepTheirRun("c1", { ...run("r1", "2026-11-24T10:00:00Z"), values: [{ name: "phone", label: "Phone", value: "0803 123 4567" }] } as KeptTheirRun, s);
    expect(s.m.get("taskiem_their_tool_runs:c1")).not.toContain("0803");
    // An older page's typed values are dropped when read.
    s.setItem("taskiem_their_tool_runs:c2", JSON.stringify([{ ...run("r2", "2026-11-24T10:00:00Z"), values: [{ value: "secret" }] }]));
    expect(JSON.stringify(keptTheirRuns("c2", s))).not.toContain("secret");
    // And written back without them.
    expect(s.m.get("taskiem_their_tool_runs:c2")).not.toContain("secret");
    s.setItem("other", "kept");
    forgetTheirRuns(s);
    expect([...s.m.keys()]).toEqual(["other"]);
  });
});
