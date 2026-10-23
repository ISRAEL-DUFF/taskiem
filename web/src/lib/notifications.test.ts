import { describe, expect, it } from "vitest";
import { groupRows, groupTitle } from "./notifications";

describe("notification settings", () => {
  it("labels the known groups", () => {
    const rows = groupRows([
      { group: "failures", chat: true, email: true },
      { group: "finished", chat: false, email: false },
    ]);
    expect(rows.map((r) => r.title)).toEqual(["Failed runs", "Your runs that finish"]);
    expect(rows[1]?.email.label).toBe("Your runs that finish by email");
    expect(rows[0]?.chat.label).toBe("Failed runs on WhatsApp or Telegram");
  });

  it("names the money group's two summaries and says owners get them", () => {
    const [money] = groupRows([{ group: "money", chat: true, email: false }]);
    expect(money?.title).toBe("Money summaries");
    expect(money?.chat).toEqual({ label: "Daily money summary (chat)", caption: "Daily money summary" });
    expect(money?.email.label).toBe("Monthly money email");
    expect(money?.hint).toMatch(/Owners get them unless they turn them off/);
    expect(money?.setting).toEqual({ group: "money", chat: true, email: false });
  });

  it("shows a group it does not know with plain labels", () => {
    const rows = groupRows([
      { group: "late_payments", chat: false, email: true },
      { group: "toString", chat: false, email: false },
    ]);
    expect(rows[0]).toMatchObject({ title: "Late payments", hint: "", chat: { label: "Late payments on WhatsApp or Telegram" }, email: { label: "Late payments by email" } });
    expect(rows[1]?.title).toBe("ToString");
  });

  it("copes with nothing, and with odd entries", () => {
    expect(groupRows(undefined)).toEqual([]);
    expect(groupRows([null as never, { group: 3 } as never])).toEqual([]);
    expect(groupTitle("__")).toBe("Other");
  });
});
