import { describe, expect, it } from "vitest";
import type { RunSummary, WorkflowSummary } from "../api";
import { dayAfter, filterRuns, filterWorkflows } from "./lists";

const wf = (name: string, active: number | null): WorkflowSummary => ({ id: name, name, key: name.toLowerCase().replace(/\s/g, "_"), active_version: active, latest_version: 1, created_at: "", git_path: null });
const run = (workflow: string, started_at: string, started_by: string | null = null): RunSummary => ({
  id: `r_${workflow}`,
  workflow_id: "w",
  workflow,
  version: 1,
  environment: "prod",
  status: "completed",
  started_by,
  started_at,
  ended_at: null,
});

describe("filterWorkflows", () => {
  const all = [wf("Payroll run", 2), wf("Loan intake", null)];
  it("matches every word, in any case", () => {
    expect(filterWorkflows(all, "payroll RUN", "").map((w) => w.name)).toEqual(["Payroll run"]);
    expect(filterWorkflows(all, "loan payroll", "")).toEqual([]);
  });
  it("filters by published state", () => {
    expect(filterWorkflows(all, "", "published").map((w) => w.name)).toEqual(["Payroll run"]);
    expect(filterWorkflows(all, "", "draft").map((w) => w.name)).toEqual(["Loan intake"]);
    expect(filterWorkflows(all, "", "")).toHaveLength(2);
  });
});

describe("filterRuns", () => {
  const day = (d: number) => new Date(2026, 9, d, 12).toISOString();
  const runs = [run("Payroll", day(8), "ann@bank.test"), run("Intake", day(5))];
  it("searches workflow, run ID and starter", () => {
    expect(filterRuns(runs, "ann", "").map((r) => r.workflow)).toEqual(["Payroll"]);
    expect(filterRuns(runs, "r_intake", "").map((r) => r.workflow)).toEqual(["Intake"]);
  });
  it("keeps runs started on or after the first day", () => {
    expect(filterRuns(runs, "", "2026-10-06").map((r) => r.workflow)).toEqual(["Payroll"]);
    expect(filterRuns(runs, "", "2026-10-05")).toHaveLength(2);
  });
});

describe("dayAfter", () => {
  it("is local midnight after the day", () => {
    expect(new Date(dayAfter("2026-10-31")).getTime()).toBe(new Date(2026, 10, 1).getTime());
  });
});
