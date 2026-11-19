import { describe, expect, it } from "vitest";
import { ApiError } from "../api";
import {
  addResults,
  canShowMore,
  chatOffWords,
  chatPath,
  hoursLine,
  previewResult,
  searchFromParams,
  searchKey,
  searchToParams,
  type DiscoverResult,
  discoverProblem,
  foundWords,
  hiddenWords,
  kmWords,
  listedWords,
  missingWords,
  nothingFound,
  photoAt,
  placeProblem,
  resultLine,
  roundPlace,
  searchBody,
  standing,
} from "./discover";

describe("Discover's search", () => {
  it("sends only what was given, the place in the body", () => {
    expect(searchBody({ q: "  rice ", area: "", category: "" }, null, 0)).toEqual({ q: "rice" });
    expect(searchBody({ q: "", area: "Yaba", category: "food" }, { lat: 6.5, lng: 3.37 }, 20)).toEqual({ area: "Yaba", category: "food", near: { lat: 6.5, lng: 3.37 }, offset: 20 });
    expect(searchBody({ q: "", area: "", category: "" }, null, 0)).toEqual({});
  });

  it("picks a photo near the width asked for", () => {
    expect(photoAt({ "160": "a", "320": "b", "800": "c" }, 300)).toBe("b");
    expect(photoAt({ "160": "a", "320": "b" }, 640)).toBe("b");
    expect(photoAt({ "160": "a" }, 100)).toBe("a");
    expect(photoAt(undefined)).toBeUndefined();
    expect(photoAt({ big: "x" })).toBe("x");
  });

  it("says how far, and a result's line", () => {
    expect(kmWords(0.34)).toBe("300 m away");
    expect(kmWords(0.02)).toBe("100 m away");
    expect(kmWords(2.44)).toBe("2.4 km away");
    expect(kmWords(15.6)).toBe("16 km away");
    expect(kmWords(undefined)).toBe("");
    expect(resultLine({ category_name: "Food and drinks", area: "Yaba", km: 1.2 })).toBe("Food and drinks · Yaba · 1.2 km away");
    expect(resultLine({ category_name: "Fashion", area: "" })).toBe("Fashion");
  });

  it("counts and says when nothing was found", () => {
    expect(foundWords(1, false)).toBe("1 business");
    expect(foundWords(20, true)).toBe("20 businesses so far");
    expect(nothingFound({ q: "cakes", area: "Ikeja", category: "" })).toMatch(/^No business found for “cakes” in Ikeja\./);
    expect(nothingFound({ q: "", area: "", category: "food" }, "Food and drinks", true)).toMatch(/^No business found for Food and drinks near you\./);
    expect(nothingFound({ q: "", area: "", category: "" })).toMatch(/^No business is listed here yet/);
  });

  it("says a refusal plainly", () => {
    expect(discoverProblem(new ApiError(429, "slow down"), "search")).toBe("You are searching too fast. Wait a moment and try again.");
    // The server's own sentences for a person, as they come.
    expect(discoverProblem(new ApiError(429, "You have sent many reports. Try again later."), "report")).toBe("You have sent many reports. Try again later.");
    expect(discoverProblem(new ApiError(429, "You have reported this business several times today. Taskiem's moderators have your reports."), "report")).toMatch(/several times today/);
    expect(discoverProblem(new ApiError(429, "too many requests"), "report")).toBe("You have sent many reports. Try again later.");
    expect(discoverProblem(new ApiError(404, "This business is not in Discover any more."), "profile")).toBe("This business is not in Discover any more.");
    expect(discoverProblem(new ApiError(404, "x"), "profile")).toBe("This business is not in Discover any more.");
    expect(discoverProblem(new ApiError(404, "x"), "search")).toBeNull();
    // Past the server's last page.
    expect(discoverProblem(new ApiError(400, "offset is from 0 to 200."), "search")).toMatch(/^That is as far as this search goes\./);
    expect(discoverProblem(new ApiError(500, "x"), "search")).toBeNull();
    expect(discoverProblem(new Error("x"), "search")).toBeNull();
    expect(placeProblem(1)).toMatch(/did not share/);
    expect(placeProblem(3)).toMatch(/too long/);
  });

  it("opens the person's own chat, or starts one from Discover", () => {
    expect(chatPath({ business: "b1", chat: "c1" }, "shop")).toEqual({ to: "/you/chats/c1", tool: "shop" });
    expect(chatPath({ business: "b1" })).toEqual({ to: "/you/chats/new?business=b1&from=discover" });
    expect(chatPath({ business: "b1" }, "book")).toEqual({ to: "/you/chats/new?business=b1&from=discover&tool=book" });
  });

  it("keeps a place to three decimals", () => {
    expect(roundPlace(6.509512, 3.371149)).toEqual({ lat: 6.51, lng: 3.371 });
  });

  it("keeps the search in the address, never the place", () => {
    const p = new URLSearchParams(searchToParams({ q: "rice ", area: "", category: "food" }));
    expect(p.toString()).toBe("q=rice+&category=food");
    expect(searchFromParams(p)).toEqual({ q: "rice ", area: "", category: "food" });
    expect(searchFromParams(new URLSearchParams(""))).toEqual({ q: "", area: "", category: "" });
    expect(searchToParams({ q: "  ", area: "", category: "" })).toEqual({});
    expect(searchKey({ q: " rice", area: "Yaba ", category: "" })).toBe(searchKey({ q: "rice", area: "Yaba", category: "" }));
  });

  it("asks the next page from the rows received, and stops at the server's last page", () => {
    expect(canShowMore(true, 20)).toBe(true);
    expect(canShowMore(true, 200)).toBe(true);
    expect(canShowMore(true, 220)).toBe(false);
    expect(canShowMore(false, 20)).toBe(false);
    expect(canShowMore(true, 0)).toBe(false);
    const r = (b: string) => ({ business: b }) as DiscoverResult;
    expect(addResults([r("a"), r("b")], [r("b"), r("c")]).map((x) => x.business)).toEqual(["a", "b", "c"]);
  });
});

describe("the hours line, as the server words it", () => {
  // 2026-11-16 is a Monday; Lagos is UTC+1.
  const at = (iso: string) => new Date(iso);
  const weekdays = { days: [[{ open: "09:00", close: "18:00" }], [{ open: "09:00", close: "18:00" }], null, null, null, null, null] };
  it("is open, or says when it opens", () => {
    expect(hoursLine(weekdays, at("2026-11-16T10:00:00Z"))).toEqual({ open: true, line: "Open now, closes at 18:00" });
    expect(hoursLine(weekdays, at("2026-11-16T06:00:00Z"))).toEqual({ open: false, line: "Closed now. Opens at 09:00" });
    expect(hoursLine(weekdays, at("2026-11-16T18:30:00Z"))).toEqual({ open: false, line: "Closed now. Opens tomorrow at 09:00" });
    expect(hoursLine(weekdays, at("2026-11-17T18:30:00Z"))).toEqual({ open: false, line: "Closed now. Opens on Monday at 09:00" });
    expect(hoursLine({ days: [] }, at("2026-11-16T10:00:00Z"))).toEqual({ open: false, line: "" });
    expect(hoursLine(undefined)).toEqual({ open: false, line: "" });
  });
  it("follows an opening past midnight, and looks a week ahead", () => {
    const late = { days: [null, null, null, null, [{ open: "20:00", close: "02:00" }], null, null] };
    // Saturday 01:00 in Lagos: Friday's opening still holds.
    expect(hoursLine(late, at("2026-11-21T00:00:00Z"))).toEqual({ open: true, line: "Open now, closes at 02:00" });
    const saturdays = { days: [null, null, null, null, null, [{ open: "10:00", close: "14:00" }], null] };
    expect(hoursLine(saturdays, at("2026-11-21T17:00:00Z"))).toEqual({ open: false, line: "Closed now. Opens next Saturday at 10:00" });
  });
  it("makes the listing's preview from a result's parts", () => {
    const r = previewResult(
      { business: "b", name: "Ada", photos: [], hours: weekdays, can_chat: true, has_shop: true, has_book: false, area: "Yaba", category: "food", photo: { urls: { "160": "/p.png" } } },
      "Food and drinks",
      at("2026-11-16T10:00:00Z"),
    );
    expect(r).toEqual({ business: "b", name: "Ada", photo: { "160": "/p.png" }, category: "food", category_name: "Food and drinks", area: "Yaba", open_now: true, line: "Open now, closes at 18:00", can_chat: true });
    expect(resultLine(r)).toBe("Food and drinks · Yaba");
  });
});

describe("a business's listing", () => {
  it("says the default or the choice", () => {
    expect(listedWords({ listed: true, chosen: null, default: true })).toBe("On by default, because your shop's page is published.");
    expect(listedWords({ listed: false, chosen: null, default: false })).toBe("Off until you turn it on.");
    expect(listedWords({ listed: false, chosen: false, default: true })).toMatch(/^Off, as you chose\. Without your choice it would be on/);
    expect(listedWords({ listed: true, chosen: true, default: true })).toBe("On, as you chose.");
    expect(listedWords({ listed: false, chosen: null, default: false, workspace: true })).toBe("A workflows workspace is not listed in Discover.");
  });

  it("says what is missing", () => {
    expect(missingWords({ missing: ["category", "area", "photo"] })).toBe("Customers can't find you in Discover until you add a category, an area and a photo.");
    expect(missingWords({ missing: ["hours"] })).toBe("Customers can't find you in Discover until you add opening hours.");
    expect(missingWords({ missing: ["photo"], missing_line: "The server's words." })).toBe("The server's words.");
    expect(missingWords({ missing: [] })).toBe("");
  });

  it("says why it is hidden and why it has no Chat", () => {
    expect(hiddenWords("removed", "Selling something not allowed.")).toBe(
      "Taskiem took your listing out of Discover: Selling something not allowed. Customers can still reach you through your chat link.",
    );
    expect(hiddenWords("reported")).toMatch(/reports about it/);
    expect(hiddenWords("suspended")).toMatch(/suspended/);
    expect(hiddenWords(undefined)).toBe("");
    expect(chatOffWords("orders_off")).toMatch(/Orders area is off/);
    expect(chatOffWords("chats_off")).toMatch(/not taking chats/);
  });

  it("says where it stands", () => {
    expect(standing({ listed: false, shown: false, missing: [] }).tag).toBe("Off");
    expect(standing({ listed: true, shown: false, missing: ["photo"] }).tag).toBe("Not shown yet");
    expect(standing({ listed: true, shown: false, hidden: "reported", missing: [] }).tag).toBe("Hidden");
    expect(standing({ listed: true, shown: true, missing: [] })).toEqual({ tag: "Listed", line: "Customers can find you in Discover." });
  });
});
