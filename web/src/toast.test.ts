import { describe, expect, it } from "vitest";
import { addToast, MAX_TOASTS, type Toast } from "./toast";

const t = (id: number, text: string, kind: Toast["kind"] = "success"): Toast => ({ id, text, kind });

describe("addToast", () => {
  it("keeps the newest few", () => {
    let list: Toast[] = [];
    for (let i = 1; i <= MAX_TOASTS + 2; i++) list = addToast(list, t(i, `Saved ${i}`));
    expect(list.map((x) => x.id)).toEqual([3, 4, 5]);
  });
  it("never shows the same message twice", () => {
    const list = addToast(addToast([], t(1, "Saved")), t(2, "Saved"));
    expect(list.map((x) => x.id)).toEqual([2]);
  });
  it("keeps an error and a success with the same words apart", () => {
    expect(addToast(addToast([], t(1, "Saved")), t(2, "Saved", "error"))).toHaveLength(2);
  });
});
