// Capability steps (decision 0044): which of a business's connections a
// capability step can run on.

import type { ConnectorInfo } from "../api";
import type { ConnectionName } from "../forms/ConnectionPicker";

/** The connections, one per name, whose service is one of rails (id@major), labelled with the service. */
export function capableConnections(list: ConnectionName[], rails: string[], connectors: Pick<ConnectorInfo, "id" | "ref" | "name">[]): { name: string; label: string }[] {
  const ids = new Set(rails.map((r) => r.split("@")[0]));
  const out = new Map<string, string>();
  for (const c of [...list].sort((a, b) => Number(b.environment === "prod") - Number(a.environment === "prod"))) {
    if (!ids.has(c.connector) || out.has(c.name)) continue;
    const service = connectors.find((k) => k.id === c.connector)?.name ?? c.connector;
    out.set(c.name, c.status === "active" ? `${c.name} (${service})` : `${c.name} (${service}, ${c.status})`);
  }
  return [...out].map(([name, label]) => ({ name, label })).sort((a, b) => a.name.localeCompare(b.name));
}

/** Rails in the order a business most likely knows them (spec 4.6). */
const RAIL_ORDER = ["paystack", "moniepoint", "iswallet", "flutterwave", "anchor"];

/**
 * The rail ids (connector ids) that can do every one of uses, best known
 * first. Payment events (kind "event": a capability_event trigger's) count
 * when the rail sends any one of them.
 */
export function railsForAll(caps: { name: string; rails: string[]; kind?: string }[], uses: string[]): string[] {
  let ids: string[] | undefined;
  let sending: string[] | undefined;
  const railsOf = (c: { rails: string[] } | undefined) => (c?.rails ?? []).map((r) => r.split("@")[0] ?? r);
  for (const u of uses) {
    const c = caps.find((x) => x.name === u);
    if (c?.kind === "event") {
      sending = [...(sending ?? []), ...railsOf(c)];
      continue;
    }
    const got = railsOf(c);
    ids = ids === undefined ? got : ids.filter((x) => got.includes(x));
  }
  if (sending !== undefined) ids = ids === undefined ? sending : ids.filter((x) => sending.includes(x));
  const rank = (id: string) => (RAIL_ORDER.includes(id) ? RAIL_ORDER.indexOf(id) : RAIL_ORDER.length);
  return [...new Set(ids ?? [])].sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));
}

/** A template's capabilities, from its connectors list (capability:pay_bank). */
export function templateCapabilities(connectors: string[]): string[] {
  return connectors.filter((c) => c.startsWith("capability:")).map((c) => c.slice("capability:".length));
}
