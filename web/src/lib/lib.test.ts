import { describe, expect, it } from "vitest";
import type { Step } from "@sdk/wd";
import { autoLayout, connect, disconnect, freshId, removeStep, renameStep, toGraph } from "./graph";
import { coerce, kindOf } from "./schema";
import { redact, timeline } from "./timeline";

const steps: Step[] = [
  { id: "a", type: "transform", config: { output: 1 } },
  { id: "b", type: "transform", needs: ["a"], config: { output: 2 } },
  { id: "c", type: "transform", needs: ["a", "b"], config: { output: 3 } },
];

describe("graph", () => {
  it("lays out steps by depth and keeps saved positions", () => {
    const l = autoLayout(steps, { b: { x: 5, y: 6 } });
    expect(l.a).toEqual({ x: 0, y: 0 });
    expect(l.b).toEqual({ x: 5, y: 6 });
    expect(l.c?.x).toBe(560);
  });
  it("builds edges from needs", () => {
    expect(toGraph({ steps }).edges.map((e) => e.id)).toEqual(["a->b", "a->c", "b->c"]);
  });
  it("refuses cycles and self edges", () => {
    expect(connect(steps, "c", "a")).toBeNull();
    expect(connect(steps, "a", "a")).toBeNull();
    expect(connect([...steps, { id: "d", type: "wait", config: { duration: "1s" } }], "c", "d")?.find((s) => s.id === "d")?.needs).toEqual(["c"]);
  });
  it("removes, disconnects and renames consistently", () => {
    expect(removeStep(steps, "a").map((s) => [s.id, s.needs])).toEqual([["b", undefined], ["c", ["b"]]]);
    expect(disconnect(steps, "a", "b")[1]?.needs).toBeUndefined();
    expect(renameStep(steps, "a", "start")[2]?.needs).toEqual(["start", "b"]);
  });
  it("finds fresh ids, including nested steps", () => {
    const nested: Step[] = [{ id: "loop", type: "foreach", config: { items: "=x", steps: [{ id: "pay", type: "wait", config: { duration: "1s" } }] } }];
    expect(freshId(nested, "pay")).toBe("pay_2");
    expect(freshId(nested, "new")).toBe("new");
  });
});

describe("schema fields", () => {
  it("picks kinds", () => {
    expect(kindOf({ type: "integer" })).toBe("integer");
    expect(kindOf({ type: ["string", "null"] })).toBe("string");
    expect(kindOf({ enum: ["a"] })).toBe("enum");
    expect(kindOf({ type: "object" })).toBe("json");
  });
  it("coerces text, keeping expressions", () => {
    expect(coerce("integer", "42")).toBe(42);
    expect(coerce("integer", "4.2")).toBe("4.2");
    expect(coerce("integer", "=trigger.body.n")).toBe("=trigger.body.n");
    expect(coerce("json", '{"a":1}')).toEqual({ a: 1 });
    expect(coerce("string", "")).toBeUndefined();
  });
});

describe("timeline", () => {
  it("derives step status from events", () => {
    const rows = timeline([
      { seq: 1, type: "RunStarted", recorded_at: "2026-10-05T10:00:00Z", origin: "ingest" },
      { seq: 2, type: "StepScheduled", step_id: "pay", attempt: 1, payload: { kind: "task", input: { amount: 1 } }, recorded_at: "2026-10-05T10:00:00Z", origin: "decide" },
      { seq: 3, type: "StepFailed", step_id: "pay", attempt: 1, payload: { error: { kind: "transient", next: "retry" } }, recorded_at: "2026-10-05T10:00:01Z", origin: "worker" },
      { seq: 4, type: "StepScheduled", step_id: "pay", attempt: 2, payload: { kind: "task" }, recorded_at: "2026-10-05T10:00:03Z", origin: "decide" },
      { seq: 5, type: "StepCompleted", step_id: "pay", attempt: 2, payload: { output: { ok: true } }, recorded_at: "2026-10-05T10:00:04Z", origin: "worker" },
      { seq: 6, type: "ApprovalRequested", step_id: "ok", payload: { role: "cfo" }, recorded_at: "2026-10-05T10:00:04Z", origin: "decide" },
    ]);
    expect(rows.map((r) => [r.id, r.status, r.attempts])).toEqual([["pay", "completed", 2], ["ok", "waiting", 0]]);
    expect(rows[1]?.waitingFor).toBe("approval by cfo");
  });
  it("redacts sealed values", () => {
    expect(redact({ bvn: { $pii: "bvn", ct: "x", subject: "s" }, n: [1] })).toEqual({ bvn: "🔒 bvn", n: [1] });
  });
});

describe("merge", () => {
  it("applies a patch and drops blanked keys", async () => {
    const { merge } = await import("./schema");
    expect(merge({ a: 1, b: 2, c: 3 }, { b: undefined, c: "", d: 4 })).toEqual({ a: 1, d: 4 });
  });
});
