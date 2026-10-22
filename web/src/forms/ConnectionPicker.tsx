import { useState } from "react";
import { get, type CapabilityInfo, type ConnectorInfo } from "../api";
import { useAuth } from "../auth";
import { capableConnections, railsForAll } from "../lib/capabilities";
import { DEFAULT_CONNECTION } from "../lib/readiness";
import { useLoad } from "../ui";
import { NewConnection, startLink } from "../pages/Connections";

/** A connection by its service, name, environment and status (GET /v1/connection-names). */
export interface ConnectionName {
  connector: string;
  name: string;
  environment: string;
  status: string;
  /** live or test, when the key says. */
  mode?: string | null;
}

/** The connector id in a ref: paystack@1 is paystack. */
export const connectorID = (ref: string) => ref.split("@")[0] ?? ref;

/**
 * The connections a picker offers: the service's, one per name, prod's
 * status first. A name only dev has is still offered.
 */
export function pickable(list: ConnectionName[], connector: string): { name: string; status: string; mode?: string | null }[] {
  const id = connectorID(connector);
  const out = new Map<string, { status: string; mode?: string | null }>();
  for (const c of list.filter((x) => x.connector === id).sort((a, b) => Number(b.environment === "prod") - Number(a.environment === "prod"))) {
    if (!out.has(c.name)) out.set(c.name, { status: c.status, mode: c.mode });
  }
  return [...out].map(([name, v]) => ({ name, ...v }));
}

/** How a connection reads in a picker: its name, and what is wrong or test about it. */
export function connectionLabel(o: { name: string; status: string; mode?: string | null }): string {
  const notes = [o.status !== "active" ? o.status : "", o.mode === "test" ? "test key: no real money" : ""].filter(Boolean);
  return notes.length ? `${o.name} (${notes.join(", ")})` : o.name;
}

/**
 * Picks which of the business's connections to a service a step, template
 * or form uses, with Connect in place when it has none (no-technical-steps
 * N2): a connection is never a name typed to match. "" uses the only one.
 */
export function ConnectionPicker({ connector, value, onChange, label = "Connection" }: { connector: string; value: string; onChange: (name: string) => void; label?: string }) {
  const { can } = useAuth();
  const names = useLoad(() => (connector ? get<{ connections: ConnectionName[] }>(`/v1/connection-names?connector=${encodeURIComponent(connectorID(connector))}`) : Promise.resolve({ connections: [] })), [connector]);
  const [connecting, setConnecting] = useState(false);
  const connectors = useLoad(() => (connecting ? get<{ connectors: ConnectorInfo[] }>("/v1/connectors") : Promise.resolve(undefined)), [connecting]);
  const options = pickable(names.data?.connections ?? [], connector);
  const service = connectors.data?.connectors.find((c) => c.id === connectorID(connector))?.name ?? connectorID(connector);
  return (
    <span className="toolbar">
      <select aria-label={label} value={value} onChange={(e) => (e.target.value === "__connect" ? setConnecting(true) : onChange(e.target.value))}>
        {options.length !== 1 && <option value="">{options.length === 0 ? "Not connected yet" : "Choose a connection…"}</option>}
        {options.length === 1 && options[0] && <option value="">{connectionLabel(options[0])}</option>}
        {value && !options.some((o) => o.name === value) && <option value={value}>{value} (not connected)</option>}
        {options.map((o) => (
          <option key={o.name} value={o.name}>
            {connectionLabel(o)}
          </option>
        ))}
        {can("connection.manage") && <option value="__connect">Connect another…</option>}
      </select>
      {options.length === 0 && can("connection.manage") && (
        <button type="button" className="small primary" onClick={() => setConnecting(true)}>
          Connect
        </button>
      )}
      {connecting && connectors.data && (
        <NewConnection
          connectors={connectors.data.connectors}
          preset={{ connector: connectorID(connector), environment: "prod", name: options.length === 0 ? value || DEFAULT_CONNECTION : "" }}
          onClose={() => setConnecting(false)}
          onDone={() => (setConnecting(false), names.reload())}
          onLink={startLink}
        />
      )}
      {connecting && !connectors.data && <span className="hint">Loading {service}…</span>}
    </span>
  );
}

/**
 * Picks the connection a capability step runs on (decision 0044): any of
 * the business's connections whose payments account can do every one of
 * capabilities, with "" for the only one that can.
 */
export function RailPicker({ capabilities, value, onChange, label = "Pay from" }: { capabilities: string[]; value: string; onChange: (name: string) => void; label?: string }) {
  const caps = useLoad(() => get<{ capabilities: CapabilityInfo[] }>("/v1/capabilities"), []);
  const names = useLoad(() => get<{ connections: ConnectionName[] }>("/v1/connection-names"), []);
  const connectors = useLoad(() => get<{ connectors: ConnectorInfo[] }>("/v1/connectors"), []);
  const rails = railsForAll(caps.data?.capabilities ?? [], capabilities);
  const options = capableConnections(names.data?.connections ?? [], rails, connectors.data?.connectors ?? []);
  return (
    <select aria-label={label} value={value} onChange={(e) => onChange(e.target.value)}>
      <option value="">{options.length === 1 ? `${options[0]?.label} (the only one that can)` : options.length === 0 ? "Not connected yet" : "The only one that can…"}</option>
      {value && !options.some((o) => o.name === value) && <option value={value}>{value} (cannot do this)</option>}
      {options.map((o) => (
        <option key={o.name} value={o.name}>
          {o.label}
        </option>
      ))}
    </select>
  );
}
