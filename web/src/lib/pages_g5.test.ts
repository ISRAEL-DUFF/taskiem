import { describe, expect, it } from "vitest";
import { newPart, partWords, PART_KINDS, NO_TABLE_KINDS } from "./pages";
import { buildStage, comingFields, madeWords, partsWords, savedListPart, savedPartWords, type ChoiceKind } from "./pages_g5";

const generator: ChoiceKind = {
  name: "Generator",
  fields: [
    { key: "size", label: "Size", type: "text", private: false },
    { key: "bought", label: "Bought", type: "date", private: true },
    { key: "next_service", label: "Next service", type: "date", private: false },
    { key: "manual", label: "Manual", type: "file", private: false },
  ],
};

describe("pages G5", () => {
  it("says a page's parts in a line", () => {
    expect(partsWords(["figure", "figure", "buttons", "board"])).toBe("figures, buttons and a board");
    expect(partsWords(["list"])).toBe("a list");
  });

  it("says what was made, a name taken and why it is not in use", () => {
    const words = madeWords({
      page: "tickets-2",
      title: "Tickets",
      tables: [{ name: "tickets_2", title: "Tickets", asked: "tickets", changed: true }],
      tools: [{ command: "new-ticket-2", asked: "new-ticket", changed: true, name: "New ticket", workflow: "w", state: "draft" }],
      published: false,
      waiting: "Saved as drafts: publishing the tools needs the right to publish workflows.",
      problems: [],
    });
    expect(words).toEqual([
      "A table called tickets was there already, so this one is tickets_2.",
      "/new-ticket is taken, so it is called with /new-ticket-2.",
      "Saved as drafts: publishing the tools needs the right to publish workflows.",
    ]);
  });

  it("says where a draft is", () => {
    const b = { id: "1", status: "running" as const, stage: "correct 1", error: null, goal: "", page_name: null, proposal: null };
    expect(buildStage(b)).toBe("Putting the draft right…");
    expect(buildStage({ ...b, status: "failed", error: "No budget" })).toBe("No budget");
    expect(buildStage({ ...b, status: "proposed" })).toBe("");
  });

  it("makes a list of saved things in the order a date comes, never a private one", () => {
    expect(comingFields(generator).map((f) => f.key)).toEqual(["next_service"]);
    const p = savedListPart(generator, "saved");
    expect(p.source).toEqual({ saved: "Generator", coming: "next_service" });
    expect(p.item?.state?.text).toBe("=row.coming");
    expect(p.detail?.lines?.map((l) => l.label)).toEqual(["Size", "Bought", "Next service"]);
    expect(savedPartWords({ ...p, source: { ...p.source, within: 7 } }, [generator])).toBe("List of saved Generator, in the order Next service comes, due within 7 days");
    expect(PART_KINDS.some((k) => k.kind === "saved-list") && NO_TABLE_KINDS.includes("saved-list")).toBe(true);
    const made = newPart("saved-list", undefined, [{ id: "saved" }], { tools: [], workflows: [], saved_kinds: [generator] });
    expect(made.id).toBe("saved_2");
    expect(partWords(made, [])).toContain("saved Generator");
  });
});
