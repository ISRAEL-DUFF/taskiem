import { describe, expect, it } from "vitest";
import { tableProblem, tablesToSave, tableTroubles, workflowID } from "./aitables";

describe("workflowID", () => {
  it("makes a short camel-case id from a name, or a sentence", () => {
    expect(workflowID("Leave request")).toBe("wf_leaveRequest");
    expect(workflowID("A leave request form for staff: name, start date")).toBe("wf_aLeaveRequestForm");
    expect(workflowID("PAYROLL run 2026")).toBe("wf_payrollRun2026");
    expect(workflowID("!!!")).toBe("wf_workflow");
    expect(workflowID("x".repeat(60)).length).toBe(43);
  });
});

describe("tables", () => {
  const leave = {
    name: "leave",
    title: "Leave",
    columns: [
      { name: "name", title: "Name", type: "text", required: true },
      { name: "start_date", title: "Start", type: "date", required: false },
    ],
  };
  it("checks a reviewed table as the server will", () => {
    expect(tableProblem(leave)).toBe("");
    expect(tableProblem({ ...leave, name: "Leave Days" })).toMatch(/table name/);
    expect(tableProblem({ ...leave, columns: [] })).toMatch(/at least one column/);
    expect(tableProblem({ ...leave, columns: [{ name: "id", title: "", type: "text", required: false }] })).toMatch(/already/);
    const name = { name: "name", title: "Name", type: "text", required: true };
    expect(tableProblem({ ...leave, columns: [name, name] })).toMatch(/twice/);
    expect(tableProblem({ ...leave, columns: [{ name: "days", title: "", type: "float", required: false }] })).toMatch(/unknown type/);
  });
  it("sends only the ticked tables, without the tick", () => {
    const out = tablesToSave([{ ...leave, create: true }, { ...leave, name: "other", create: false }]);
    expect(out).toEqual([leave]);
  });
  it("shows the results that need the person", () => {
    expect(
      tableTroubles([
        { name: "a", status: "created" },
        { name: "b", status: "exists" },
        { name: "c", status: "failed", error: "x" },
        { name: "d", status: "skipped" },
      ]).map((r) => r.name),
    ).toEqual(["c", "d"]);
  });
});
