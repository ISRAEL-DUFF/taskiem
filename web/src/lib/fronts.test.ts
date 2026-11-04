import { describe, expect, it } from "vitest";
import { frontNameProblem, frontSub, frontURL, type Front } from "./fronts";

const kinds = [
  { name: "shop", title: "Shop", line: "" },
  { name: "food", title: "Food", line: "" },
];
const front = (p: Partial<Front>): Front => ({
  id: "f1",
  slug: "snacks",
  kind: "food",
  name: "Snacks",
  position: 1,
  enabled: false,
  actions: [],
  table_id: null,
  items: 0,
  ...p,
});

describe("fronts", () => {
  it("says where a front is, its kind and what it has", () => {
    expect(frontSub(front({ table_id: "t", items: 3 }), kinds)).toBe(
      "/snacks · Food · 3 items",
    );
    expect(
      frontSub(
        front({ slug: "", kind: "shop", table_id: "t", items: 1, actions: [{} as never] }),
        kinds,
      ),
    ).toBe("Your home page · Shop · 1 item · 1 form");
  });
  it("gives a front's address", () => {
    expect(frontURL("https://ada.taskiem.page/", front({}))).toBe(
      "https://ada.taskiem.page/snacks",
    );
  });
  it("checks a new front's name", () => {
    expect(frontNameProblem("  ")).toBe("Give the front a name.");
    expect(frontNameProblem("x".repeat(81))).toBe("A name is up to 80 characters.");
    expect(frontNameProblem("Snacks")).toBe("");
  });
});
