import { describe, expect, it } from "vitest";
import { recordSteps, registrarOf, REGISTRARS } from "./registrars";

describe("registrar guides", () => {
  it("says what to type in each box", () => {
    const txt = recordSteps(registrarOf("namecheap"), { type: "TXT", name: "_taskiem-verify.adakitchen.ng", short: "_taskiem-verify", value: "taskiem-verify=abc", purpose: "proof" });
    expect(txt).toContain("In Host, type _taskiem-verify.");
    expect(txt).toContain("In Value, paste taskiem-verify=abc.");
    const a = recordSteps(registrarOf("godaddy"), { type: "A", name: "adakitchen.ng", short: "@", value: "203.0.113.7", purpose: "point" });
    expect(a[1]).toMatch(/^In Name, type @ \(it means the domain itself/);
    expect(a.some((s) => s.startsWith("Delete any other A record"))).toBe(true);
  });
  it("covers the companies Nigerian businesses use, and any other", () => {
    expect(REGISTRARS.map((r) => r.id)).toEqual(["whogohost", "qservers", "domainking", "godaddy", "namecheap", "other"]);
    expect(registrarOf("unknown").id).toBe("other");
  });
});
