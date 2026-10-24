// Connecting by OAuth (decision 0034): what the Connections page reads
// from the URL when the provider sends the browser back, and which fields
// a linked connection is still missing.
import type { AuthField, ConnectorInfo } from "../api";

/** What the OAuth callback sent the browser back with: the new
 * connection's id, or what went wrong, in words for the person. */
export function linkResult(search: string): { linked?: string; error?: string } {
  const q = new URLSearchParams(search);
  const linked = q.get("linked");
  const error = q.get("error");
  if (linked) return { linked };
  if (error) return { error };
  return {};
}

/** The connector's auth fields a linked connection may still want: the
 * optional ones the token does not fill (Xenibase: the secret key and
 * data API URL). Empty for a connector without OAuth. */
export function fieldsToComplete(c: ConnectorInfo | undefined): ConnectorInfo["auth"]["fields"] {
  if (!c?.auth.oauth2 || !c.auth.fields) return [];
  // A grant with a refresh token (Google) is complete as it is.
  if (Object.values(c.auth.oauth2.fields_from_token ?? {}).includes("refresh_token")) return [];
  const filled = new Set([...Object.keys(c.auth.oauth2.fields_from_token ?? {}), ...Object.keys(c.auth.oauth2.platform_client_fields ?? {})]);
  return c.auth.fields.filter((f) => f.required === false && !filled.has(f.key));
}

/** Whether a step using the connector needs a connection: not for one
 * without auth, like Taskiem's own notify ("Tell me"). As readiness
 * decides it on the server. */
export function needsConnection(c: ConnectorInfo): boolean {
  return c.auth.type !== "" && c.auth.type !== "none";
}

/** The connector a connection was made with, by its id (connections name
 * the id, connectors their `id@major` ref). */
export function connectorOf(connectors: ConnectorInfo[] | undefined, id: string): ConnectorInfo | undefined {
  return connectors?.find((c) => c.id === id);
}

/** The choices a connector's keys offer (live, test), in order. */
export function modesOf(c: ConnectorInfo | undefined): { mode: string; label: string }[] {
  const out: { mode: string; label: string }[] = [];
  for (const f of c?.auth.fields ?? []) {
    for (const p of f.prefixes ?? []) {
      if (p.mode && !out.some((m) => m.mode === p.mode)) out.push({ mode: p.mode, label: p.label ?? p.mode });
    }
  }
  return out;
}

/**
 * What is wrong with a pasted value, in plain words, as the server checks
 * it (connector.AuthField.CheckValue): a refused start, a key for the other
 * mode, or one that does not start as the field's do. "" when it looks right.
 */
export function keyProblem(f: AuthField, value: string, mode = ""): string {
  const v = value.trim();
  if (!v) return "";
  for (const r of f.refuse ?? []) if (v.startsWith(r.prefix)) return r.message;
  const prefixes = f.prefixes ?? [];
  if (prefixes.length === 0) return "";
  let other: (typeof prefixes)[number] | undefined;
  for (const p of prefixes) {
    if (!v.startsWith(p.prefix)) continue;
    if (!mode || !p.mode || p.mode === mode) return "";
    other = p;
  }
  const label = f.label.toLowerCase();
  if (other) {
    const want = (prefixes.find((p) => p.mode === mode)?.label ?? mode).toLowerCase();
    const got = (other.label ?? other.mode ?? "").toLowerCase();
    return `that is a ${got} ${label}, but you chose ${want}; copy the ${want} ${label} instead`;
  }
  const starts = prefixes.filter((p) => !mode || !p.mode || p.mode === mode).map((p) => p.prefix);
  return `that does not look like your ${label}: it starts with ${starts.join(" or ")}`;
}

/** Fields shown first (required) and under More options (optional). */
export function splitFields(c: ConnectorInfo | undefined): { main: AuthField[]; more: AuthField[] } {
  const fields = c?.auth.fields ?? [];
  return { main: fields.filter((f) => f.required !== false), more: fields.filter((f) => f.required === false) };
}

/**
 * Makes an RSA key pair in the browser for a provider that authenticates
 * with RSA (OPay Business, `generate: rsa_key_pair`): the private key as a
 * PKCS #8 PEM block, kept as the connection's value, and the public key as
 * a PEM block to paste into the provider's dashboard. OPay documents
 * 1024-bit keys.
 */
export async function generateRsaKeyPair(bits = 1024): Promise<{ privatePem: string; publicPem: string }> {
  const pair = await crypto.subtle.generateKey(
    { name: "RSASSA-PKCS1-v1_5", modulusLength: bits, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
    true,
    ["sign", "verify"],
  );
  const priv = await crypto.subtle.exportKey("pkcs8", pair.privateKey);
  const pub = await crypto.subtle.exportKey("spki", pair.publicKey);
  return { privatePem: pemBlock("PRIVATE KEY", priv), publicPem: pemBlock("PUBLIC KEY", pub) };
}

function pemBlock(label: string, der: ArrayBuffer): string {
  let bin = "";
  for (const b of new Uint8Array(der)) bin += String.fromCharCode(b);
  const lines = btoa(bin).match(/.{1,64}/g) ?? [];
  return `-----BEGIN ${label}-----\n${lines.join("\n")}\n-----END ${label}-----\n`;
}

/** An address to set in a rail's dashboard so the money log hears a prod
 * connection's events without a workflow (GET /v1/connections,
 * money_log_webhooks; decision 0045). Never carries a secret: when the
 * address needs one, placeholder is the part to replace. */
export interface MoneyLogWebhook {
  trigger: string;
  title: string;
  url: string;
  events: string[];
  verification: string;
  placeholder?: string;
  where: string;
  poll_minutes?: number;
}

const EVENT_WORDS: Record<string, string> = {
  "payment.received": "payments received",
  "payment.returned": "payments returned",
  "payout.settled": "transfers that went through",
  "payout.failed": "transfers that failed",
  "payout.reversed": "transfers reversed",
  "virtual_account.credited": "money into account numbers",
  "pos.sale": "POS sales",
  "refund.pending": "refunds started",
  "refund.processed": "refunds paid out",
  "refund.failed": "refunds that failed",
};

/** The normalised events an address carries, in words: "payments
 * received and payments returned". */
export function eventsInWords(events: string[]): string {
  const words = events.map((e) => EVENT_WORDS[e] ?? e);
  if (words.length <= 1) return words.join("");
  return `${words.slice(0, -1).join(", ")} and ${words[words.length - 1]}`;
}

/** Whether Taskiem also asks the provider itself, so setting the address
 * up is optional. Empty when it does not. */
export function pollNote(minutes: number | undefined): string {
  if (!minutes) return "";
  return `Taskiem also checks every ${minutes === 1 ? "minute" : `${minutes} minutes`}, so this is optional; with it, money shows at once.`;
}

/** The connections whose page shows addresses for the money log: prod
 * connections of rails whose events it records. */
export function withMoneyLogWebhooks<T extends { environment: string; money_log_webhooks?: MoneyLogWebhook[] }>(conns: T[]): T[] {
  return conns.filter((c) => c.environment === "prod" && (c.money_log_webhooks?.length ?? 0) > 0);
}
