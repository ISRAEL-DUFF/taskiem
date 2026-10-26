import { describe, expect, it } from "vitest";
import type { Step, WorkflowDefinition } from "@sdk/wd";
import { earlierAnywhere, inForeach, sampleWords, sourcesAnywhere, valueAt, withStarted } from "./sources";
import { newChoice } from "../lib/flow";

const form = (id: string, needs?: string[]): Step =>
  ({ id, type: "form", config: { title: id, to: { role: "owner" }, schema: { type: "object", properties: { qty: { type: "integer", title: "Quantity" } } } }, ...(needs ? { needs } : {}) }) as Step;

const def = (): Pick<WorkflowDefinition, "trigger" | "inputs" | "steps"> => {
  const choice = newChoice("ok") as Step & { needs?: string[]; config: { paths: { steps: Step[] }[] } };
  choice.needs = ["ask"];
  choice.config.paths[0] = { ...choice.config.paths[0], steps: [form("more"), form("last", ["more"])] };
  const each = { id: "each", type: "foreach", needs: ["ok"], config: { items: "=trigger.body.items", steps: [form("per")] } } as unknown as Step;
  return { trigger: { type: "manual" }, inputs: { schema: { type: "object", properties: { name: { type: "string", title: "Customer name" } } } }, steps: [form("ask"), choice, each] };
};

describe("what a step can use", () => {
  it("reaches the steps before its path, and before it in the path", () => {
    expect(earlierAnywhere(def().steps, "last").map((s) => s.id)).toEqual(["ask", "more"]);
    expect(earlierAnywhere(def().steps, "per").map((s) => s.id)).toEqual(["ask", "ok"]);
  });

  it("names them in words, with the item inside a for each", () => {
    const labels = sourcesAnywhere(def(), "last", []).map((s) => s.label);
    expect(labels).toEqual(["What was filled in: Customer name", "From “ask”: Quantity", "From “more”: Quantity"]);
    expect(inForeach(def().steps, "per")).toBe(true);
    expect(sourcesAnywhere(def(), "per", [])[0]).toEqual({ label: "This item", expr: "=item" });
  });
});

describe("a value's sample in a test", () => {
  const result = { trigger: { body: { name: "Adaeze" } }, outputs: { ask: { qty: 3 } } };
  it("reads what started it and what each step gave", () => {
    expect(valueAt(result, "=trigger.body.name")).toBe("Adaeze");
    expect(valueAt(result, "=steps.ask.output.qty")).toBe(3);
    expect(valueAt(result, "=steps.nope.output.qty")).toBeUndefined();
    expect(valueAt(undefined, "=trigger.body.name")).toBeUndefined();
  });
  it("says a sample in a few words", () => {
    expect(sampleWords([1, 2])).toBe("2 items");
    expect(sampleWords({ a: 1 })).toBe("1 field");
    expect(sampleWords(null)).toBe("nothing");
  });
});

describe("what started it", () => {
  it("offers a payment's fields, then what a test's sample held", () => {
    const pay = withStarted([], { type: "capability_event" });
    expect(pay.map((s) => s.expr)).toEqual(["=trigger.fields.payment_id", "=double(trigger.fields.amount_kobo) / 100.0", "=trigger.fields.currency", "=trigger.fields.paid_at"]);
    expect(pay[1]?.label).toBe("What started it: Amount (₦)");
    expect(valueAt({ trigger: { fields: { amount_kobo: 150050 } }, outputs: {} }, pay[1]?.expr ?? "")).toBe(1500.5);
    const hook = withStarted([{ label: "From “Check”: ok", expr: "=steps.check.output.ok" }], { type: "webhook" }, { trigger: { body: { name: "Ada" } } });
    expect(hook.map((s) => s.expr)).toEqual(["=trigger.body.name", "=steps.check.output.ok"]);
    const form = [{ label: "What was filled in: Name", expr: "=trigger.body.name" }];
    expect(withStarted(form, { type: "form" }, { trigger: { body: { name: "Ada", extra: 1 } } })).toBe(form);
  });
});
