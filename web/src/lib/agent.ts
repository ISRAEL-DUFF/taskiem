// Field agents (docs/agents.md): the pure parts of the agent app, the
// owner's list of agent visits and the operator console's agents page.

/** The header an agent sends, with their own sign-in, to act in a business. */
export const AGENT_SESSION_HEADER = "X-Taskiem-Agent-Session";

/** What an agent may do in a business, as the console's permission checks
 * see it: the page and the tables behind it (api/agents.go). The server
 * also grants workflow permissions for the storefront's own steps, but
 * refuses the workflow routes to an agent, so the console hides them. */
export const AGENT_PERMISSIONS = ["storefront.manage", "table.read", "table.edit"];

/** A business session the agent is working in, as the app keeps it. */
export interface AgentSession {
  id: string;
  business: string;
  kind: "onboarding" | "support";
  expires_at: string;
}

/** The agent's own routes and the sign-in itself go without the session
 * header: they are the agent acting as themselves. Everything else in a
 * session carries it, so a request the agent may not make in the business
 * is refused there rather than made in the agent's own workspace. */
export function agentHeaderApplies(path: string): boolean {
  const p = path.split("?")[0] ?? path;
  return !(p === "/v1/agent" || p.startsWith("/v1/agent/") || p.startsWith("/v1/auth/") || p === "/v1/me" || p.startsWith("/v1/me/"));
}

/** The request's headers, with the session header when one applies. */
export function withAgentHeader(path: string, headers: Record<string, string>, session: string | null | undefined): Record<string, string> {
  if (!session || !agentHeaderApplies(path)) return headers;
  return { ...headers, [AGENT_SESSION_HEADER]: session };
}

/** Whether a refusal says the agent's session in the business is over. */
export function sessionEnded(status: number, body: Record<string, unknown> | undefined): boolean {
  return status === 403 && body?.agent_session_ended === true;
}

/** A stored session, or null for anything that is not one. */
export function parseSession(text: string | null): AgentSession | null {
  if (!text) return null;
  try {
    const v = JSON.parse(text) as Partial<AgentSession>;
    if (typeof v.id !== "string" || typeof v.business !== "string" || typeof v.expires_at !== "string") return null;
    if (v.kind !== "onboarding" && v.kind !== "support") return null;
    return { id: v.id, business: v.business, kind: v.kind, expires_at: v.expires_at };
  } catch {
    return null;
  }
}

/** Milliseconds until `expiresAt`, never below zero. */
export function msLeft(expiresAt: string, now: number): number {
  const t = Date.parse(expiresAt);
  return Number.isNaN(t) ? 0 : Math.max(0, t - now);
}

/** A countdown: "29:05" under an hour, "23 h 04 min" from an hour up. */
export function formatLeft(ms: number): string {
  const s = Math.max(0, Math.floor(ms / 1000));
  if (s >= 3600) {
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return `${h} h ${String(m).padStart(2, "0")} min`;
  }
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
}

/** How a session ended, in words. */
export function endReason(reason: string | null | undefined): string {
  switch (reason) {
    case "published":
      return "Page published";
    case "agent_ended":
      return "The agent left";
    case "owner_ended":
      return "The owner ended it";
    case "expired":
      return "Time ran out";
    case "replaced":
      return "A newer session replaced it";
    case "agent_suspended":
      return "The agent was suspended";
    case "agent_retired":
      return "The agent was retired";
    case null:
    case undefined:
    case "":
      return "—";
    default:
      return reason.replaceAll("_", " ");
  }
}

/** The attest request's path: the place and the note go in the query. */
export function attestPath(session: string, lat: number, lng: number, note: string): string {
  const q = new URLSearchParams({ lat: lat.toFixed(6), lng: lng.toFixed(6) });
  if (note.trim()) q.set("note", note.trim());
  return `/v1/agent/sessions/${encodeURIComponent(session)}/attest?${q.toString()}`;
}

/** A photo's size scaled down to fit `max` on its longer side (never up). */
export function fitWithin(width: number, height: number, max: number): { width: number; height: number } {
  const long = Math.max(width, height);
  if (long <= max || long === 0) return { width, height };
  const k = max / long;
  return { width: Math.round(width * k), height: Math.round(height * k) };
}

/** A visit's length in hours, for inviting an agent back: 1 to 8, default 2. */
export function visitHours(text: string): number | null {
  const n = Number(text.trim() || "2");
  return Number.isInteger(n) && n >= 1 && n <= 8 ? n : null;
}

/** An agent's code as printed on their card: AG- and five letters or digits. */
export function agentCodeOK(code: string): boolean {
  return /^AG-[0-9A-Z]{5}$/i.test(code.trim());
}

export type ConsentChannel = "whatsapp" | "telegram";

/** Where the owner can agree from, by what the consent offers: WhatsApp
 * (a wa.me link or its QR) and Telegram (a t.me link). With neither link,
 * WhatsApp: the owner sends the message to the number shown. */
export function consentChannels(c: { link?: string; qr?: string; telegram_link?: string }): ConsentChannel[] {
  const out: ConsentChannel[] = [];
  if (c.link || c.qr || !c.telegram_link) out.push("whatsapp");
  if (c.telegram_link) out.push("telegram");
  return out;
}
