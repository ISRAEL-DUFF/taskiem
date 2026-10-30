import { describe, expect, it } from "vitest";
import { cardAction, cardSub, findableLine, groupLinks, looksLikePhone, matchLinked, roleLine, stateWords, type BusinessLink, type LinkCard } from "./links";

const link = (over: Partial<BusinessLink>): BusinessLink => ({
  id: "l1",
  business: "b1",
  name: "Dangote Stores",
  state: "active",
  role: "supplier",
  asked_by_us: true,
  requested_at: "2026-10-30T09:00:00Z",
  expires_at: "2026-11-29T09:00:00Z",
  ...over,
});

describe("links", () => {
  it("say a state and a role in plain words", () => {
    expect(stateWords("active")).toEqual({ label: "Linked", tone: "ok" });
    expect(stateWords("asked_us")).toEqual({ label: "Asks to link", tone: "warn" });
    expect(stateWords("requested")?.label).toBe("Asked");
    expect(stateWords("sent")?.label).toBe("Request sent");
    expect(stateWords("not_accepted")).toEqual({ label: "Not accepted", tone: "muted" });
    expect(stateWords("none")).toBeNull();
    expect(roleLine("supplier")).toBe("They supply you");
    expect(roleLine("customer")).toBe("They buy from you");
    expect(roleLine("both")).toBe("They supply you and buy from you");
  });

  it("group the list: requests to answer, linked, waiting, sent, the rest", () => {
    const g = groupLinks({
      links: [
        link({ id: "a", name: "Zed", state: "active" }),
        link({ id: "b", name: "Ada", state: "active" }),
        link({ id: "c", name: "Cherry", state: "asked_us", asked_by_us: false }),
        link({ id: "d", name: "Bisi", state: "requested" }),
        link({ id: "e", name: "Old", state: "not_accepted" }),
        link({ id: "f", name: "Gone", state: "ended" }),
      ],
      sent: [{ id: "s", kind: "phone", shown: "0803…4567", role: "both", state: "sent", created_at: "", expires_at: "" }],
    });
    expect(g.asked.map((l) => l.id)).toEqual(["c"]);
    expect(g.linked.map((l) => l.name)).toEqual(["Ada", "Zed"]);
    expect(g.waiting.map((l) => l.id)).toEqual(["d", "e"]);
    expect(g.sent).toHaveLength(1);
    expect(g.past.map((l) => l.id)).toEqual(["f"]);
    expect(groupLinks(undefined).linked).toEqual([]);
  });

  it("find linked businesses by every word of their name", () => {
    const list = [link({ name: "Dangote Stores" }), link({ id: "x", name: "Dangote Cement", state: "requested" })];
    expect(matchLinked(list, "dangote").map((l) => l.name)).toEqual(["Dangote Stores"]);
    expect(matchLinked(list, "stores dan")).toHaveLength(1);
    expect(matchLinked(list, "  ")).toEqual([]);
  });

  it("never search a phone number", () => {
    expect(looksLikePhone("0803 123 4567")).toBe(true);
    expect(looksLikePhone("+234-803-123-4567")).toBe(true);
    expect(looksLikePhone("cement")).toBe(false);
    expect(looksLikePhone("rice 50kg")).toBe(false);
    expect(looksLikePhone("123")).toBe(false);
  });

  it("say a business found in one line, and what can be done", () => {
    const c: LinkCard = { business: "b", name: "Cherry", area: "Ikeja", items: [{ name: "9 inch blocks", price_kobo: 65000 }, { name: "Sand" }], state: "none" };
    expect(cardSub(c)).toBe("Ikeja · 9 inch blocks ₦650 · Sand");
    expect(cardSub({ ...c, area: undefined, items: [], tagline: "Blocks and sand" })).toBe("Blocks and sand");
    expect(cardSub({ ...c, area: undefined, items: [] })).toBe("On Taskiem");
    expect(cardAction(c)).toBe("request");
    expect(cardAction({ ...c, state: "asked_us" })).toBe("answer");
    expect(cardAction({ ...c, state: "requested" })).toBeNull();
    expect(cardAction({ ...c, state: "active" })).toBeNull();
  });

  it("say whether others find us", () => {
    expect(findableLine({ findable: null, findable_now: true })).toMatch(/^On, because your storefront is published/);
    expect(findableLine({ findable: null, findable_now: false })).toMatch(/^Off until/);
    expect(findableLine({ findable: false, findable_now: false })).toMatch(/^Off: only someone/);
    expect(findableLine({ findable: true, findable_now: true })).toMatch(/^On: /);
  });
});
