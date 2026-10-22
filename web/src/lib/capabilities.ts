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
