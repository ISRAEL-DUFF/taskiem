import { describe, expect, it } from "vitest";
import { fieldWords, segmentsOf, valueOf, type Segment } from "./fieldtext";

const label = (e: string) => ({ "=trigger.body.name": "Customer name", "=steps.link.output.url": "Payment link" })[e] ?? fieldWords(e);

describe("text with fields", () => {
  it("keeps plain text plain, and one field as the field", () => {
    expect(valueOf([{ text: "Hello" }])).toBe("Hello");
    expect(valueOf([{ expr: "=trigger.body.name", label: "Customer name" }])).toBe("=trigger.body.name");
    expect(valueOf([{ text: "" }])).toBeUndefined();
  });

  it("joins text and fields into one expression, and reads it back", () => {
    const segs: Segment[] = [
      { text: "Hello " },
      { expr: "=trigger.body.name", label: "Customer name" },
      { text: ", pay here: " },
      { expr: "=steps.link.output.url", label: "Payment link" },
      { text: " (it's quick)" },
    ];
    const v = valueOf(segs);
    expect(v).toBe("='Hello ' + string(trigger.body.name) + ', pay here: ' + string(steps.link.output.url) + ' (it\\'s quick)'");
    expect(segmentsOf(v, label)).toEqual(segs);
  });

  it("reads the joins templates write by hand", () => {
    expect(segmentsOf("='Your bank sent an alert: ' + trigger.body.subject", label)).toEqual([
      { text: "Your bank sent an alert: " },
      { expr: "=trigger.body.subject", label: "Subject" },
    ]);
    expect(segmentsOf("Thanks", label)).toEqual([{ text: "Thanks" }]);
    expect(segmentsOf(undefined, label)).toEqual([]);
  });

  it("takes an amount in kobo as naira", () => {
    const segs: Segment[] = [{ text: "We got ₦" }, { expr: "=double(trigger.fields.amount_kobo) / 100.0", label: "Amount" }];
    const v = valueOf(segs);
    expect(v).toBe("='We got ₦' + string(double(trigger.fields.amount_kobo) / 100.0)");
    expect(segmentsOf(v, () => "Amount")).toEqual(segs);
  });

  it("leaves anything else to Advanced", () => {
    expect(segmentsOf("=has(trigger.body.x) ? 'a' : 'b'", label)).toBeNull();
    expect(segmentsOf("=string(int(item.days)) + ' days'", label)).toBeNull();
    expect(segmentsOf({ a: 1 }, label)).toBeNull();
  });
});
