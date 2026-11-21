import { describe, expect, it } from "vitest";
import { IMPORT_EXAMPLE, importDoneLine, importExampleHref, importKindWord, importPlanLine, importRefusal, importSaveWords, type ImportRow } from "./savedImport";

const row = (kind: ImportRow["kind"], name: string): ImportRow => ({ line: 2, name, title: name, kind, hint: "" });

describe("saved things from a file", () => {
  it("says what a plan saves", () => {
    expect(importPlanLine({ rows: [row("person", "a"), row("person", "b"), row("person", "c")] })).toBe("This file saves 3 people.");
    expect(importPlanLine({ rows: [row("bank_account", "a")] })).toBe("This file saves 1 bank account.");
    expect(importPlanLine({ rows: [row("place", "a"), row("person", "b"), row("person", "c"), row("bank_account", "d")] })).toBe("This file saves 4 things: 2 people, 1 place, 1 bank account.");
    expect(importSaveWords(1)).toBe("Save 1 thing");
    expect(importSaveWords(4)).toBe("Save 4 things");
    expect(importKindWord("provider_account")).toBe("An account with a provider");
    expect(importKindWord("text")).toBe("A text");
  });

  it("says what saving did, and the lines that were not saved", () => {
    expect(importDoneLine({ added: ["bayo", "bisi"], failed: [] })).toBe("Saved 2 things: @bayo, @bisi.");
    expect(importDoneLine({ added: ["bayo"], failed: [{ line: 3, name: "bisi", message: "That name was taken meanwhile." }] })).toBe("Saved 1 thing: @bayo. 1 line was not saved.");
    expect(
      importDoneLine({
        added: [],
        failed: [
          { line: 2, name: "a", message: "x" },
          { line: 3, name: "b", message: "y" },
        ],
      }),
    ).toBe("Nothing was saved. 2 lines were not saved.");
  });

  it("reads a refusal's lines, and leaves other errors alone", () => {
    const body = {
      error: "Some lines cannot be saved: put them right and send the file again.",
      code: "import_unreadable",
      problems: [{ line: 3, message: "Choose the bank." }, { nope: 1 }],
      total: 4,
    };
    expect(importRefusal(422, body, "")).toEqual({ message: body.error, problems: [{ line: 3, message: "Choose the bank." }], total: 4 });
    // The file as a whole: no lines.
    expect(importRefusal(422, { error: "The file is empty or not a CSV: save it from the spreadsheet as CSV.", code: "import_unreadable", problems: null, total: 0 }, "")?.problems).toEqual([]);
    expect(importRefusal(409, { code: "import_changed" }, "")).toBeNull();
    expect(importRefusal(422, { code: "invalid" }, "")).toBeNull();
  });

  it("offers an example the server reads: a title and a kind on every line", () => {
    const [head, ...lines] = IMPORT_EXAMPLE.trim().split("\n");
    const cols = (head ?? "").split(",");
    expect(cols).toContain("title");
    expect(cols).toContain("account_number");
    expect(lines.length).toBeGreaterThan(2);
    for (const l of lines) expect(l.split(",")).toHaveLength(cols.length);
    expect(decodeURIComponent(importExampleHref().replace(/^data:text\/csv;charset=utf-8,/, ""))).toBe(IMPORT_EXAMPLE);
  });
});
