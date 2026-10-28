import { describe, expect, it } from "vitest";
import type { Step } from "@sdk/wd";
import {
  TRIGGER,
  childLists,
  connectIn,
  definitionProblem,
  emptyPaths,
  flowGraph,
  forSaving,
  insertStep,
  listAt,
  locate,
  newChoice,
  notesOf,
  removeAnywhere,
  renameAnywhere,
  stepFromJson,
  tidy,
  withNotes,
  type FlowNode,
} from "./flow";

const t = (id: string, needs?: string[]): Step => ({ id, type: "transform", config: { output: {} }, ...(needs ? { needs } : {}) }) as Step;

// An order comes in: check the stock; if it is in stock send a link and
// tell the customer, else say it is sold out.
const order = (): Step[] => {
  const choice = newChoice("in_stock") as Step & { config: { paths: { steps: Step[] }[]; default: { steps: Step[] } } };
  choice.needs = ["check"];
  choice.config.paths[0] = { ...choice.config.paths[0], steps: [t("link"), t("tell", ["link"])] };
  choice.config.default.steps = [t("sold_out")];
  return [t("check"), choice];
};

const node = (nodes: FlowNode[], id: string) => nodes.find((n) => n.id === id) as FlowNode;

describe("lists at every depth", () => {
  it("finds a step and its list anywhere", () => {
    const steps = order();
    expect(locate(steps, "tell")?.list).toBe("in_stock/paths/0");
    expect(locate(steps, "sold_out")?.list).toBe("in_stock/default");
    expect(locate(steps, "check")?.list).toBe("");
    expect(listAt(steps, "in_stock/paths/0").map((s) => s.id)).toEqual(["link", "tell"]);
  });

  it("renames a step and the needs that point at it, in its own list", () => {
    const steps = renameAnywhere(order(), "link", "pay_link");
    expect(listAt(steps, "in_stock/paths/0").map((s) => [s.id, s.needs])).toEqual([
      ["pay_link", undefined],
      ["tell", ["pay_link"]],
    ]);
  });
});

describe("adding a step where the + is", () => {
  it("between two steps", () => {
    const steps = insertStep(order(), { list: "in_stock/paths/0", after: "link", before: "tell" }, t("wait"));
    expect(listAt(steps, "in_stock/paths/0").map((s) => [s.id, s.needs])).toEqual([
      ["link", undefined],
      ["wait", ["link"]],
      ["tell", ["wait"]],
    ]);
  });

  it("after the last step, at the start, and into an empty path", () => {
    let steps = insertStep(order(), { list: "in_stock/default", after: "sold_out" }, t("note"));
    expect(locate(steps, "note")?.step.needs).toEqual(["sold_out"]);
    steps = insertStep(steps, { list: "", before: "check" }, t("first"));
    expect(locate(steps, "first")?.step.needs).toBeUndefined();
    expect(locate(steps, "check")?.step.needs).toEqual(["first"]);
    const empty = [{ ...newChoice("c") }];
    steps = insertStep(empty, { list: "c/default" }, t("only"));
    expect(listAt(steps, "c/default").map((s) => s.id)).toEqual(["only"]);
  });

  it("before a step that already comes after another", () => {
    const steps = insertStep([t("a"), t("b", ["a"])], { list: "", before: "b" }, t("mid"));
    expect(steps.map((s) => [s.id, s.needs])).toEqual([
      ["a", undefined],
      ["mid", ["a"]],
      ["b", ["mid"]],
    ]);
  });
});

describe("removing and joining", () => {
  it("keeps the chain joined when a middle step goes", () => {
    const steps = removeAnywhere([t("a"), t("b", ["a"]), t("c", ["b"])], "b");
    expect(steps.map((s) => [s.id, s.needs])).toEqual([
      ["a", undefined],
      ["c", ["a"]],
    ]);
  });

  it("removes inside a path, and a choice with everything in it", () => {
    let steps = removeAnywhere(order(), "link");
    expect(listAt(steps, "in_stock/paths/0").map((s) => [s.id, s.needs])).toEqual([["tell", undefined]]);
    steps = removeAnywhere(order(), "in_stock");
    expect(steps.map((s) => s.id)).toEqual(["check"]);
    expect(locate(steps, "tell")).toBeUndefined();
  });

  it("joins only within one list, and never in a loop", () => {
    expect(connectIn(order(), "check", "tell")).toBeNull();
    expect(connectIn([t("a"), t("b", ["a"])], "b", "a")).toBeNull();
    expect(connectIn([t("a"), t("b")], "a", "b")?.[1]?.needs).toEqual(["a"]);
  });
});

describe("the picture", () => {
  it("draws a choice's paths as cards with Yes and No, and a place after it", () => {
    const { nodes, edges } = flowGraph({ steps: order() });
    expect(nodes.map((n) => n.id)).toEqual([TRIGGER, "check", "in_stock", "link", "tell", "sold_out", "then:in_stock"]);
    const byPair = Object.fromEntries(edges.map((e) => [`${e.source}->${e.target}`, e]));
    expect(byPair[`${TRIGGER}->check`]?.at).toEqual({ list: "", before: "check" });
    expect(byPair["check->in_stock"]?.at).toEqual({ list: "", after: "check", before: "in_stock" });
    expect(byPair["in_stock->link"]).toMatchObject({ label: "Yes", tone: "yes", sourceHandle: "p0", at: { list: "in_stock/paths/0", before: "link" } });
    expect(byPair["in_stock->sold_out"]).toMatchObject({ label: "No", tone: "no", sourceHandle: "p1" });
    expect(byPair["tell->then:in_stock"]?.at).toEqual({ list: "", after: "in_stock" });
    expect(byPair["sold_out->then:in_stock"]).toBeDefined();
    const choice = node(nodes, "in_stock");
    expect(choice.kind === "step" && choice.ports.map((p) => p.label)).toEqual(["Yes", "No"]);
    // The last card of a path offers "add after"; a card with one after it does not.
    expect((node(nodes, "tell") as { last: boolean }).last).toBe(true);
    expect((node(nodes, "link") as { last: boolean }).last).toBe(false);
  });

  it("joins the paths' last cards to what comes after the choice", () => {
    const steps = [...order(), t("thank", ["in_stock"])];
    const { nodes, edges } = flowGraph({ steps });
    expect(nodes.some((n) => n.id === "then:in_stock")).toBe(false);
    const into = edges.filter((e) => e.target === "thank");
    expect(into.map((e) => e.source).sort()).toEqual(["sold_out", "tell"]);
    expect(into[0]?.need).toEqual({ source: "in_stock", target: "thank" });
    expect(into[0]?.at).toEqual({ list: "", after: "in_stock", before: "thank" });
  });

  it("shows an empty path as a place to add its first step", () => {
    const { nodes, edges } = flowGraph({ steps: [newChoice("c")] });
    expect(node(nodes, "empty:c/paths/0")).toMatchObject({ kind: "empty", at: { list: "c/paths/0" } });
    expect(node(nodes, "empty:c/default")).toMatchObject({ kind: "empty", label: "Nothing yet" });
    expect(edges.find((e) => e.target === "empty:c/default")?.label).toBe("No");
  });

  it("lays out columns left to right and fans a choice's paths out, Yes above No", () => {
    const { nodes } = flowGraph({ steps: order() });
    const at = (id: string) => node(nodes, id).position;
    expect(at(TRIGGER).x).toBeLessThan(at("check").x);
    expect(at("check").x).toBeLessThan(at("in_stock").x);
    expect(at("link").x).toBe(at("sold_out").x);
    expect(at("link").y).toBeLessThan(at("sold_out").y);
    expect(at("tell").y).toBe(at("link").y);
  });

  it("keeps a place someone dragged a card to", () => {
    const { nodes } = flowGraph({ steps: order() }, { check: { x: 7, y: 9 } });
    expect(node(nodes, "check").position).toEqual({ x: 7, y: 9 });
  });
});

describe("notes and tidying up", () => {
  it("keeps notes in the layout, and tidying keeps them", () => {
    const layout = withNotes({ check: { x: 1, y: 2 } }, [{ id: "n1", x: 5, y: 6, text: "Ask Tunde" }]);
    expect(notesOf(layout)).toEqual([{ id: "n1", x: 5, y: 6, text: "Ask Tunde" }]);
    expect(tidy(layout)).toEqual({ __notes: [{ id: "n1", x: 5, y: 6, text: "Ask Tunde" }] });
    expect(notesOf(withNotes(layout, []))).toEqual([]);
  });
});

describe("saving an If", () => {
  it("leaves out a No with nothing in it, and still shows it", () => {
    const choice = newChoice("ok") as Step & { config: { paths: { steps: Step[] }[] } };
    choice.config.paths[0] = { ...choice.config.paths[0], steps: [t("tell")] };
    const saved = forSaving({ steps: [choice] }).steps[0] as Step & { config: Record<string, unknown> };
    expect(saved.config.default).toBeUndefined();
    expect(childLists(saved).map((c) => c.label)).toEqual(["Yes", "No"]);
    expect(listAt(insertStep([saved], { list: "ok/default" }, t("sorry")), "ok/default").map((s) => s.id)).toEqual(["sorry"]);
  });

  it("names the paths that still need a step; an empty No is fine", () => {
    expect(emptyPaths([newChoice("ok")], (s) => s.name ?? s.id)).toEqual(["Yes of “Is it so?”"]);
    expect(emptyPaths(order(), (s) => s.id)).toEqual([]);
  });
});

describe("a step edited as JSON", () => {
  it("keeps its id: renaming goes through the ID field", () => {
    expect(stepFromJson(t("a"), { id: "b", type: "transform" })).toMatch(/stays "a"/);
    expect(stepFromJson(t("a"), [])).toMatch(/must be an object/);
    expect(stepFromJson(t("a"), { id: "a" })).toMatch(/needs a "type"/);
    expect(stepFromJson(t("a"), { id: "a", type: "transform", name: "Renamed" })).toEqual({ id: "a", type: "transform", name: "Renamed" });
  });
});

describe("what the JSON tab accepts", () => {
  const def = (steps: unknown) => ({ schema: "wd/v1", id: "wf_x", version: 1, name: "x", trigger: { type: "manual" }, steps });
  it("takes a definition the builder can draw, at every depth", () => {
    expect(definitionProblem(def(order()))).toBeUndefined();
    expect(definitionProblem(def([]))).toBeUndefined();
  });
  it("says what is wrong with anything else", () => {
    expect(definitionProblem([])).toMatch(/must be an object/);
    expect(definitionProblem({ steps: [] })).toMatch(/trigger/);
    expect(definitionProblem({ trigger: { type: "manual" } })).toMatch(/"steps" must be a list/);
    expect(definitionProblem(def([1]))).toMatch(/step 1 must be an object/);
    expect(definitionProblem(def([{ type: "transform" }]))).toMatch(/needs an "id"/);
    expect(definitionProblem(def([{ id: "a", type: "transform", needs: "b" }]))).toMatch(/"needs" must be a list/);
    expect(definitionProblem(def([{ id: "c", type: "branch", config: { paths: [{ name: "yes", steps: [null] }] } }]))).toMatch(/Step "c", paths 1: step 1 must be an object/);
    expect(definitionProblem(def([{ id: "c", type: "branch", config: { default: { steps: {} } } }]))).toMatch(/otherwise must be a list/);
    expect(definitionProblem(def([{ id: "p", type: "parallel", config: { branches: {} } }]))).toMatch(/"branches" must be a list/);
    expect(definitionProblem(def([{ id: "e", type: "foreach", config: { steps: [{ id: "x" }] } }]))).toMatch(/Step "x" needs a "type"/);
  });
});
