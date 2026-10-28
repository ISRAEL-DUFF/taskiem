import { describe, expect, it } from "vitest";
import { dropDraft, keepDraft, keptDraft } from "./keep";

class Mem implements Storage {
  m = new Map<string, string>();
  get length() {
    return this.m.size;
  }
  clear() {
    this.m.clear();
  }
  getItem(k: string) {
    return this.m.get(k) ?? null;
  }
  key(i: number) {
    return [...this.m.keys()][i] ?? null;
  }
  removeItem(k: string) {
    this.m.delete(k);
  }
  setItem(k: string, v: string) {
    this.m.set(k, v);
  }
}

describe("unsaved builder edits kept in the tab", () => {
  it("keeps, reads back and drops a workflow's edits", () => {
    const s = new Mem();
    const d = { definition: { steps: [] }, layout: { a: { x: 1 } }, version: 3, digest: "abc", at: "2026-10-28T10:00:00Z" };
    keepDraft("wf1", d, s);
    expect(keptDraft("wf1", s)).toEqual(d);
    expect(keptDraft("wf2", s)).toBeUndefined();
    dropDraft("wf1", s);
    expect(keptDraft("wf1", s)).toBeUndefined();
  });

  it("ignores what it cannot read", () => {
    const s = new Mem();
    s.setItem("taskiem:draft:wf1", "{not json");
    expect(keptDraft("wf1", s)).toBeUndefined();
    s.setItem("taskiem:draft:wf1", JSON.stringify({ definition: null, version: 1 }));
    expect(keptDraft("wf1", s)).toBeUndefined();
  });

  it("works without storage, and when storage refuses", () => {
    expect(keptDraft("wf1", undefined)).toBeUndefined();
    const refusing = new Mem();
    refusing.setItem = () => {
      throw new Error("QuotaExceededError");
    };
    refusing.getItem = () => {
      throw new Error("SecurityError");
    };
    expect(() => keepDraft("wf1", { definition: {}, layout: {}, version: 1, at: "" }, refusing)).not.toThrow();
    expect(keptDraft("wf1", refusing)).toBeUndefined();
  });
});
