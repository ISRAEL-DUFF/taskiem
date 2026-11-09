import { describe, expect, it } from "vitest";
import {
  fillTemplate,
  heldWords,
  kindWords,
  seconds,
  statusWords,
  templateSlots,
} from "./conversations";

describe("the conversations page's words", () => {
  it("says why a reply was held back, once per reason", () => {
    expect(heldWords("price, link")).toBe(
      "it gave a price your material does not state; it had a link to a site you have not allowed",
    );
    expect(heldWords("secret, secret")).toBe(
      "it looked like it had a password or key in it",
    );
    expect(heldWords("new_kind")).toBe("it did not pass the check");
    expect(heldWords(undefined)).toBe("");
    expect(statusWords({ role: "held", reason: "promise" })).toContain(
      "a discount or a refund",
    );
  });

  it("says what happened to a customer's message and to a reply by hand", () => {
    expect(
      statusWords({ role: "customer", status: "dropped", reason: "stopped" }),
    ).toBe("Not answered: they had written STOP");
    expect(
      statusWords({ role: "customer", status: "quiet", reason: "paused" }),
    ).toContain("paused");
    expect(
      statusWords({ role: "customer", status: "reply", reason: "handover" }),
    ).toContain("asked for a person");
    expect(statusWords({ role: "customer", status: "answer" })).toBe("");
    expect(statusWords({ role: "member", status: "unknown" })).toBe(
      "May not have been sent",
    );
    expect(statusWords({ role: "member", status: "sending" })).toBe("Sending…");
    expect(statusWords({ role: "assistant" })).toBe("");
  });

  it("counts and fills a template's blanks", () => {
    expect(templateSlots("Hi {{1}}, your order {{2}} is ready. {{1}}")).toBe(2);
    expect(templateSlots("No blanks")).toBe(0);
    expect(fillTemplate("Hi {{1}}, order {{2}}", ["Ada", "P-1"])).toBe(
      "Hi Ada, order P-1",
    );
    expect(fillTemplate("Hi {{1}}", [])).toBe("Hi {{1}}");
  });

  it("gives times and kinds short words", () => {
    expect(seconds(null)).toBe("—");
    expect(seconds(12.4)).toBe("12 s");
    expect(seconds(90)).toBe("1.5 min");
    expect(kindWords("voice")).toBe("Voice note");
    expect(kindWords("text")).toBe("");
  });
});
