import { describe, expect, it } from "vitest";
import { notify } from "./connectors.gen.js";
import { celToFunction, compileFunction } from "./expr.js";
import { manual, transform, workflow } from "./builder.js";
import { SAVED_FIELDS, savedRef } from "./saved.js";

// The SDK's saved root (build plan H6): the server's saved.* expressions,
// in code and back.
describe("saved", () => {
  it("compiles the saved root as the server reads it", () => {
    expect(compileFunction("({ saved }) => saved.price_list.body")).toBe("saved.price_list.body");
    expect(compileFunction(`({ saved }) => saved["price-list"].file_name`)).toBe("saved['price-list'].file_name");
    expect(compileFunction("(c) => c.saved.mum.phone")).toBe("saved.mum.phone");
  });

  it("prints it back as code", () => {
    expect(celToFunction("saved.price_list.file")).toBe("({ saved }) => saved.price_list.file");
  });

  it("names a saved thing or a field of it", () => {
    expect(savedRef("price-list", "body")).toBe("=saved.price_list.body");
    expect(savedRef("price-list")).toBe("=saved.price_list");
    expect(savedRef("2go", "phone")).toBe('=saved["2go"].phone');
    expect(() => savedRef("Mum")).toThrow(/not a saved thing's name/);
    expect(() => savedRef("mum", "a b")).toThrow(/not a field's key/);
    expect(SAVED_FIELDS.file).toContain("file");
  });

  it("sends a saved file with a message", () => {
    const def = workflow({ id: "wf_weekly", name: "Weekly prices", trigger: manual() })
      .step("note", transform({ output: { text: ({ saved }) => saved.intro!.body } }))
      .next(
        "send",
        notify.message({ email: "buyer@ada.test", message: ({ steps }) => steps.note.output.text, file: ({ saved }) => saved.price_list }),
      )
      .build();
    const send = def.steps.find((s) => s.id === "send") as { input: Record<string, unknown> };
    expect(send.input.file).toBe("=saved.price_list");
  });
});
