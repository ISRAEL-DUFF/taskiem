import { describe, expect, it } from "vitest";
import {
  groupSuggestions,
  insertMention,
  mentionParts,
  mentionQuery,
  stillNamed,
  suggest,
  type Suggestion,
} from "./mentions";

const list: Suggestion[] = [
  { kind: "business", id: "l1", name: "Dangote Stores" },
  { kind: "business", id: "l2", name: "Cherry Blocks" },
  { kind: "business", id: "l3", name: "Ada's Kitchen" },
];

describe("mentions in the composer", () => {
  it("finds the mention being typed", () => {
    expect(mentionQuery("Buy 4 from @Dan", 15)).toEqual({
      mark: "@",
      query: "Dan",
      start: 11,
    });
    expect(mentionQuery("#ri", 3)).toEqual({
      mark: "#",
      query: "ri",
      start: 0,
    });
    expect(mentionQuery("ada@example.com", 15)).toBeNull();
    expect(mentionQuery("@Dangote\nok", 11)).toBeNull();
    expect(mentionQuery("no mark here", 12)).toBeNull();
  });
  it("suggests by a word's start, then by what a name holds", () => {
    expect(suggest(list, "bl").map((s) => s.id)).toEqual(["l2"]);
    expect(suggest(list, "").map((s) => s.id)).toEqual(["l1", "l2", "l3"]);
    expect(suggest(list, "te").map((s) => s.id)).toEqual(["l1"]);
  });
  it("puts the pick in the text", () => {
    const q = { mark: "@" as const, query: "Dan", start: 11 };
    expect(
      insertMention("Buy 4 from @Dan please", q, 15, "Dangote Stores"),
    ).toEqual({ text: "Buy 4 from @Dangote Stores  please", caret: 27 });
  });
  it("keeps only the mentions still named", () => {
    const picked = [
      { kind: "business", name: "Dangote Stores" },
      { kind: "item", name: "Cement" },
    ];
    expect(stillNamed("Ask @dangote stores for #Cement", picked)).toHaveLength(
      2,
    );
    expect(stillNamed("Ask Dangote for cement", picked)).toHaveLength(0);
  });
  it("marks a message's mentions", () => {
    const parts = mentionParts("400 #Cement, then @Cherry Blocks.", [
      {
        kind: "item",
        name: "Cement",
        item_id: "c",
        price_kobo: 950000,
        unit: "bag",
      },
      { kind: "business", name: "Cherry Blocks", link: "l2" },
    ]);
    expect(parts.map((p) => [p.text, p.mention?.kind ?? ""])).toEqual([
      ["400 ", ""],
      ["#Cement", "item"],
      [", then ", ""],
      ["@Cherry Blocks", "business"],
      [".", ""],
    ]);
    expect(mentionParts("plain")).toEqual([{ text: "plain" }]);
  });
});

describe("the grouped picker (chat tools)", () => {
  const items = [
    { kind: "item" as const, id: "i1", name: "Rice, 50kg bag", sub: "₦78,000 a bag" },
    { kind: "item" as const, id: "i2", name: "Rice, 25kg bag" },
    { kind: "item" as const, id: "i3", name: "Beans" },
  ];
  const businesses = [{ kind: "business" as const, id: "l1", name: "Rivers Foods Ltd" }];
  it("groups things, then businesses, then Taskiem", () => {
    const g = groupSuggestions("ri", { items, businesses }, true);
    expect(g.map((x) => x.label)).toEqual(["Products", "Businesses"]);
    expect(g[0]?.items.map((x) => x.id)).toEqual(["i1", "i2"]);
    const all = groupSuggestions("", { items, businesses }, true);
    expect(all.map((x) => x.label)).toEqual(["Products", "Businesses", ""]);
    expect(all[2]?.items[0]?.kind).toBe("taskiem");
    expect(groupSuggestions("task", { items, businesses }, true).map((x) => x.label)).toEqual([""]);
    expect(groupSuggestions("", { items, businesses }, false).some((x) => x.label === "")).toBe(false);
  });
  it("knows an item written with @ or #", () => {
    const picked = [{ kind: "item", name: "Rice, 25kg bag" }];
    expect(stillNamed("@Rice, 25kg bag is out", picked)).toHaveLength(1);
    expect(stillNamed("#Rice, 25kg bag is out", picked)).toHaveLength(1);
    const parts = mentionParts("@Rice, 25kg bag is out", [{ kind: "item", name: "Rice, 25kg bag" }]);
    expect(parts[0]).toEqual({ text: "@Rice, 25kg bag", mention: { kind: "item", name: "Rice, 25kg bag" } });
  });
});
