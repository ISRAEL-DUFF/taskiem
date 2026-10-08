import { describe, expect, it } from "vitest";
import { byCategory, initialValues, inputKind, lineLabel, toParams, type Template, type TemplateParam } from "./templates";

const params: TemplateParam[] = [
  { name: "business_name", type: "string", title: "Business name", description: "d", required: true, example: "Ada" },
  { name: "threshold", type: "number", title: "Alert below", description: "d", required: true, example: 5000, default: 1000 },
  { name: "day", type: "weekday", title: "Day", description: "d", required: true, example: "friday", default: "friday" },
  { name: "send_time", type: "time", title: "Time", description: "d", required: false, example: "10:00" },
  { name: "loud", type: "boolean", title: "Loud", description: "d", required: false, example: true },
  { name: "lang", type: "string", title: "Language", description: "d", required: false, example: "en", enum: ["en", "yo"] },
  { name: "note", type: "text", title: "Note", description: "d", required: false, example: "x" },
];

describe("template parameters", () => {
  it("starts from the defaults", () => {
    expect(initialValues(params)).toEqual({ business_name: "", threshold: "1000", day: "friday", send_time: "", loud: "", lang: "", note: "" });
  });
  it("picks an input per type", () => {
    expect(params.map(inputKind)).toEqual(["text", "number", "select", "time", "checkbox", "select", "textarea"]);
  });
  it("sends typed values and leaves blanks to the server", () => {
    const v = { ...initialValues(params), business_name: " Ada Stores ", threshold: "2500.5", loud: "true", send_time: "" };
    expect(toParams(params, v)).toEqual({ business_name: "Ada Stores", threshold: 2500.5, day: "friday", loud: true });
    // A value that is not a number goes as typed: the server names it.
    expect(toParams(params, { threshold: "lots" })).toEqual({ threshold: "lots" });
  });
});

describe("gallery", () => {
  it("groups by category", () => {
    const t = (id: string, category: string) => ({ id, category }) as Template;
    expect(byCategory([t("a", "sales"), t("b", "alerts"), t("c", "sales")]).map(([c, l]) => [c, l.map((x) => x.id)])).toEqual([
      ["alerts", ["b"]],
      ["sales", ["a", "c"]],
    ]);
  });
  it("labels plain steps", () => {
    expect(lineLabel({ number: "3a", depth: 1, text: "Text the customer" })).toBe("3a. Text the customer");
    expect(lineLabel({ number: "", depth: 1, text: "If that step fails:" })).toBe("If that step fails:");
  });
});
