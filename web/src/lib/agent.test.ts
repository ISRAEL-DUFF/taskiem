import { describe, expect, it } from "vitest";
import {
  AGENT_SESSION_HEADER,
  agentCodeOK,
  agentHeaderApplies,
  attestPath,
  consentChannels,
  endReason,
  fitWithin,
  formatLeft,
  msLeft,
  parseSession,
  sessionEnded,
  visitHours,
  withAgentHeader,
} from "./agent";

describe("field agents", () => {
  it("sends the session header on the business's requests only", () => {
    expect(agentHeaderApplies("/v1/storefront")).toBe(true);
    expect(agentHeaderApplies("/v1/storefront/catalogue/refresh")).toBe(true);
    expect(agentHeaderApplies("/v1/tables/abc/rows?limit=50")).toBe(true);
    // Anything else in a session is refused in the business, not done in the agent's own workspace.
    expect(agentHeaderApplies("/v1/workflows/x/versions/1/publish")).toBe(true);
    expect(agentHeaderApplies("/v1/agent/sessions/abc")).toBe(false);
    expect(agentHeaderApplies("/v1/agent/sessions/abc/attest?lat=1&lng=2")).toBe(false);
    expect(agentHeaderApplies("/v1/auth/logout")).toBe(false);
    expect(agentHeaderApplies("/v1/me")).toBe(false);
    expect(agentHeaderApplies("/v1/me/invitations")).toBe(false);
    // The owner's side is not the agent's own route.
    expect(agentHeaderApplies("/v1/agent-visits")).toBe(true);
  });
  it("adds the header without touching the others", () => {
    const base = { "X-Taskiem-Request": "1" };
    expect(withAgentHeader("/v1/storefront", base, "s1")).toEqual({ "X-Taskiem-Request": "1", [AGENT_SESSION_HEADER]: "s1" });
    expect(withAgentHeader("/v1/storefront", base, null)).toBe(base);
    expect(withAgentHeader("/v1/agent/me", base, "s1")).toBe(base);
    expect(base).toEqual({ "X-Taskiem-Request": "1" });
  });
  it("knows a refusal that ends the session", () => {
    expect(sessionEnded(403, { error: "x", agent_session_ended: true })).toBe(true);
    expect(sessionEnded(403, { error: "in a business, a field agent works on its page" })).toBe(false);
    expect(sessionEnded(401, { agent_session_ended: true })).toBe(false);
    expect(sessionEnded(403, undefined)).toBe(false);
  });
  it("reads a stored session, and nothing else", () => {
    const s = { id: "a", business: "Mama Put", kind: "onboarding", expires_at: "2026-10-20T10:00:00Z" };
    expect(parseSession(JSON.stringify(s))).toEqual(s);
    expect(parseSession(null)).toBeNull();
    expect(parseSession("not json")).toBeNull();
    expect(parseSession(JSON.stringify({ ...s, kind: "other" }))).toBeNull();
    expect(parseSession(JSON.stringify({ id: "a" }))).toBeNull();
  });
  it("counts down", () => {
    const now = Date.parse("2026-10-20T10:00:00Z");
    expect(msLeft("2026-10-20T10:30:00Z", now)).toBe(30 * 60_000);
    expect(msLeft("2026-10-20T09:00:00Z", now)).toBe(0);
    expect(msLeft("nonsense", now)).toBe(0);
    expect(formatLeft(0)).toBe("0:00");
    expect(formatLeft(65_000)).toBe("1:05");
    expect(formatLeft(29 * 60_000 + 59_999)).toBe("29:59");
    expect(formatLeft(3600_000)).toBe("1 h 00 min");
    expect(formatLeft(23 * 3600_000 + 4 * 60_000 + 30_000)).toBe("23 h 04 min");
    expect(formatLeft(-5)).toBe("0:00");
  });
  it("says how a session ended", () => {
    expect(endReason("published")).toBe("Page published");
    expect(endReason("owner_ended")).toBe("The owner ended it");
    expect(endReason("expired")).toBe("Time ran out");
    expect(endReason(null)).toBe("—");
    expect(endReason("something_new")).toBe("something new");
  });
  it("puts the place and note in the attest query", () => {
    expect(attestPath("s 1", 6.5244, 3.3792, "  ")).toBe("/v1/agent/sessions/s%201/attest?lat=6.524400&lng=3.379200");
    expect(attestPath("s1", -1, 2, " Blue door ")).toBe("/v1/agent/sessions/s1/attest?lat=-1.000000&lng=2.000000&note=Blue+door");
  });
  it("scales a photo down, never up", () => {
    expect(fitWithin(4000, 3000, 1600)).toEqual({ width: 1600, height: 1200 });
    expect(fitWithin(3000, 4000, 1600)).toEqual({ width: 1200, height: 1600 });
    expect(fitWithin(800, 600, 1600)).toEqual({ width: 800, height: 600 });
    expect(fitWithin(0, 0, 1600)).toEqual({ width: 0, height: 0 });
  });
  it("checks a support visit's hours and the agent's code", () => {
    expect(visitHours("")).toBe(2);
    expect(visitHours("8")).toBe(8);
    expect(visitHours("0")).toBeNull();
    expect(visitHours("9")).toBeNull();
    expect(visitHours("1.5")).toBeNull();
    expect(agentCodeOK("AG-7KQ2M")).toBe(true);
    expect(agentCodeOK(" ag-7kq2m ")).toBe(true);
    expect(agentCodeOK("AG-7KQ2")).toBe(false);
  });

  it("offers WhatsApp, Telegram or both for consent", () => {
    expect(consentChannels({ link: "https://wa.me/1", qr: "data:", telegram_link: "https://t.me/bot?start=agree_123456" })).toEqual(["whatsapp", "telegram"]);
    expect(consentChannels({ telegram_link: "https://t.me/bot?start=agree_123456" })).toEqual(["telegram"]);
    expect(consentChannels({ link: "https://wa.me/1" })).toEqual(["whatsapp"]);
    expect(consentChannels({})).toEqual(["whatsapp"]);
  });
});
