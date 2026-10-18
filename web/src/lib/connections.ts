// Connecting by OAuth (decision 0034): what the Connections page reads
// from the URL when the provider sends the browser back, and which fields
// a linked connection is still missing.
import type { ConnectorInfo } from "../api";

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
  const filled = new Set(Object.keys(c.auth.oauth2.fields_from_token ?? {}));
  return c.auth.fields.filter((f) => f.required === false && !filled.has(f.key));
}

/** The connector a connection was made with, by its id (connections name
 * the id, connectors their `id@major` ref). */
export function connectorOf(connectors: ConnectorInfo[] | undefined, id: string): ConnectorInfo | undefined {
  return connectors?.find((c) => c.id === id);
}
