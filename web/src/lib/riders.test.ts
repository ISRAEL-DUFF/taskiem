import { describe, expect, it } from "vitest";
import { emptyRider, reachedLine, riderForm, riderProblem, riderRequest, riderSub, stateLine, stateTone, type Rider } from "./riders";
import { feeKobo, zoneList, zoneProblem, zoneLine } from "./orders";

const musa: Rider = {
  id: "r1",
  name: "Musa",
  phone: "+2348031234567",
  zones: ["Lekki", "Ajah"],
  fee_kobo: 100000,
  active: true,
  reached: "sms",
  presence: { state: "free" },
  created_at: "2026-10-28T09:00:00Z",
};

describe("riders", () => {
  it("say a rider's state in plain words", () => {
    expect(stateLine(musa)).toBe("Free");
    expect(stateTone(musa)).toBe("ok");
    expect(stateLine({ ...musa, presence: { state: "busy", until: "2026-10-28T14:00:00Z" } })).toBe("Busy till 15:00");
    expect(stateLine({ ...musa, presence: { state: "off" } })).toBe("Off");
    expect(stateLine({ ...musa, active: false })).toBe("On leave");
    expect(stateTone({ ...musa, presence: { state: "off" } })).toBe("muted");
  });

  it("say where a rider is reached, with their areas and fee", () => {
    expect(reachedLine(musa)).toBe("By SMS until they tap the link");
    expect(riderSub({ ...musa, reached: "whatsapp" })).toBe("Lekki, Ajah · ₦1,000 a delivery · On WhatsApp");
    expect(riderSub({ ...musa, zones: [], fee_kobo: 0, reached: "telegram" })).toBe("All areas · On Telegram");
  });

  it("build the add and change requests", () => {
    const f = { ...emptyRider(), name: " Musa  Bello ", phone: "0803 123 4567", zones: ["Lekki"], fee: "1,500" };
    expect(riderProblem(f, true)).toBeNull();
    expect(riderRequest(f, true)).toEqual({ name: "Musa Bello", phone: "0803 123 4567", zones: ["Lekki"], fee_kobo: 150000 });
    expect(riderRequest(riderForm(musa), false)).toEqual({ name: "Musa", zones: ["Lekki", "Ajah"], fee_kobo: 100000 });
    expect(riderProblem({ ...f, name: "" }, true)).toMatch(/name/);
    expect(riderProblem({ ...f, phone: "0803" }, true)).toMatch(/mobile number/);
    expect(riderProblem({ ...f, phone: "" }, false)).toBeNull();
    expect(riderProblem({ ...f, fee: "abc" }, true)).toMatch(/fee/);
  });
});

describe("delivery areas", () => {
  it("read typed fees", () => {
    expect(feeKobo("")).toBe(0);
    expect(feeKobo("₦1,500.50")).toBe(150050);
    expect(Number.isNaN(feeKobo("1.234"))).toBe(true);
  });

  it("keep named rows and say what is wrong", () => {
    const rows = [
      { name: " Lekki  Phase 1 ", fee: "1500" },
      { name: "", fee: "" },
      { name: "Yaba", fee: "" },
    ];
    expect(zoneList(rows)).toEqual([
      { name: "Lekki Phase 1", fee_kobo: 150000 },
      { name: "Yaba", fee_kobo: 0 },
    ]);
    expect(zoneProblem(rows)).toBeNull();
    expect(zoneProblem([...rows, { name: "yaba", fee: "1" }])).toBe("yaba is listed twice.");
    expect(zoneProblem([{ name: "Ikeja, GRA", fee: "" }])).toMatch(/no commas/);
    expect(zoneProblem([{ name: "Ikeja", fee: "200000" }])).toMatch(/₦100,000/);
    expect(zoneLine({ name: "Yaba", fee_kobo: 200000 })).toBe("Yaba, ₦2,000 delivery");
    expect(zoneLine({ name: "Akoka", fee_kobo: 0 })).toBe("Akoka, free delivery");
  });
});
