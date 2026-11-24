import { describe, expect, it } from "vitest";
import { savedSources } from "./savedSources";
import { stepsWords } from "./savedUses";

describe("stepsWords", () => {
  it("says which steps read a saved thing", () => {
    expect(stepsWords([])).toBe("Read when the run started");
    expect(stepsWords(["send"])).toBe("Read by step send");
    expect(stepsWords(["note", "send"])).toBe("Read by steps note and send");
    expect(stepsWords(["a", "b", "c"])).toBe("Read by steps a, b and c");
  });
});

describe("a saved file as a value", () => {
  it("offers the link a step sends", () => {
    const got = savedSources([{ id: "1", name: "price-list", title: "Price list", kind: "file", details: { file_name: "p.pdf" } } as never], []);
    expect(JSON.stringify(got)).toContain("saved.price_list.file");
  });
});
