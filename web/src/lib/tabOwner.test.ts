import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { currentAgentSession, setAgentSession } from "../api";
import type { AgentSession } from "./agent";
import { setDraft } from "./drafts";
import { saveRecent, type RecentCheck } from "./schoolClearance";
import { keepInflight, type Inflight } from "./taskiemchat";
import { claimTab, forgetTab } from "./tabOwner";

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

let s: Mem;
beforeEach(() => {
  s = new Mem();
  vi.stubGlobal("sessionStorage", s);
  vi.stubGlobal("window", new EventTarget());
});
afterEach(() => {
  setAgentSession(null);
  vi.unstubAllGlobals();
});

// What person A leaves in the tab: a chat draft, the message to Taskiem
// still in flight, the gate's recent checks, a business session, and an
// unsaved builder draft (kept across sign-outs by design, lib/keep.ts).
function seedA() {
  setDraft("link:abc", "A's half-written message");
  keepInflight({ text: "A's message to Taskiem", at: "2026-11-05T10:00:00Z" } as Inflight);
  saveRecent([{ name: "Chidi Okafor", status: "owing" } as unknown as RecentCheck]);
  setAgentSession({ id: "11111111-1111-1111-1111-111111111111", business: "A's client shop", kind: "support", expires_at: "2099-01-01T00:00:00Z" } as unknown as AgentSession);
  s.setItem("taskiem:draft:wf1", "{}");
}
const personal = ["taskiem-draft:link:abc", "taskiem-chat-inflight", "taskiem-gate-recent", "taskiem.agent-session"];

describe("what the tab keeps belongs to the person signed in", () => {
  it("keeps it for the same person signing in again (a 401 sends them to sign in)", () => {
    claimTab("user-a");
    seedA();
    claimTab("user-a");
    expect([...s.m.keys()]).toEqual(expect.arrayContaining(personal));
    expect(currentAgentSession()).not.toBeNull();
  });

  it("forgets it when someone else signs in after a session ran out", () => {
    claimTab("user-a");
    seedA();
    claimTab("user-b");
    for (const k of personal) expect(s.getItem(k)).toBeNull();
    expect(currentAgentSession()).toBeNull();
    expect(s.getItem("taskiem:draft:wf1")).toBe("{}");
  });

  it("forgets it on signing out", () => {
    claimTab("user-a");
    seedA();
    forgetTab();
    for (const k of personal) expect(s.getItem(k)).toBeNull();
    expect(currentAgentSession()).toBeNull();
    expect(s.getItem("taskiem:draft:wf1")).toBe("{}");
    // The next person to sign in finds nothing, and claims the tab.
    claimTab("user-b");
    expect(s.getItem("taskiem-owner")).toBe("user-b");
  });
});
