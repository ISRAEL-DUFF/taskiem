import { describe, expect, it } from "vitest";
import { formWorkflow } from "./newform";

describe("a new form", () => {
  it("is a form workflow that tells the owner", () => {
    const d = formWorkflow(" Join the waiting list ", "tell");
    expect(d.trigger).toEqual({ type: "form", config: { title: "Join the waiting list" } });
    expect(d.name).toBe("Join the waiting list");
    expect(d.steps[0]).toMatchObject({ connector: "notify@1", action: "tell" });
    expect(formWorkflow("x", "collect").steps[0]?.type).toBe("transform");
  });
});
