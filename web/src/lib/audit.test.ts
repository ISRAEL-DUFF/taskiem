import { describe, expect, it } from "vitest";
import { detailWords, shortIds } from "./audit";

describe("audit rows", () => {
  it("cuts ids to their first characters", () => {
    expect(shortIds("01a15de9-2f36-7a6f-8dbe-419e66dee946/1")).toBe("01a15de9…/1");
    expect(shortIds("tenant")).toBe("tenant");
  });
  it("says a detail as key value pairs, long values cut", () => {
    expect(detailWords({ ip: "127.0.0.1", name: "Leave request" })).toBe("ip 127.0.0.1 · name Leave request");
    expect(detailWords({ digest: "fb60ea862664ab39439d4fd9bb27378335a5a3f2" })).toBe("digest fb60ea862664ab39439d4fd9…");
    expect(detailWords({ advanced: true, run_id: "01a15de9-2f36-7a6f-8dbe-419e66dee946" })).toBe("advanced true · run id 01a15de9…");
    expect(detailWords({})).toBe("");
    expect(detailWords(null)).toBe("");
  });
});
