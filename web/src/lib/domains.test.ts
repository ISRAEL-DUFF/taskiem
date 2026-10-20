import { describe, expect, it } from "vitest";
import { domainHosts, domainState, recordsInOrder, tidyDomain, type StorefrontDomain } from "./domains";

const base: StorefrontDomain = {
  id: "d1",
  domain: "adakitchen.ng",
  twin: "www.adakitchen.ng",
  token: "taskiem-domain-verify=x",
  primary: false,
  verified_at: null,
  created_by: "user:ada",
  created_at: "2026-10-20T10:00:00Z",
  status: "pending",
  records: [
    { type: "A", name: "adakitchen.ng", value: "203.0.113.7", purpose: "point" },
    { type: "TXT", name: "_taskiem-verify.adakitchen.ng", value: "taskiem-domain-verify=x", purpose: "proof" },
  ],
};

describe("own domains", () => {
  it("says what state a domain is in", () => {
    expect(domainState(base)).toMatch(/Waiting/);
    expect(domainState({ ...base, status: "live", primary: true })).toMatch(/your page's address/);
    expect(domainState({ ...base, status: "live" })).toMatch(/main domain/);
    expect(domainState({ ...base, status: "unverified", last_check_error: "no TXT record" })).toContain("(no TXT record)");
  });
  it("lists the hosts and puts the proof first", () => {
    expect(domainHosts(base)).toEqual(["adakitchen.ng", "www.adakitchen.ng"]);
    expect(domainHosts({ ...base, twin: null })).toEqual(["adakitchen.ng"]);
    expect(recordsInOrder(base)[0]?.type).toBe("TXT");
  });
  it("tidies what was typed", () => {
    expect(tidyDomain(" https://AdaKitchen.ng/menu?x=1 ")).toBe("adakitchen.ng");
    expect(tidyDomain("shop.example.com.")).toBe("shop.example.com");
    expect(tidyDomain("")).toBe("");
  });
});
