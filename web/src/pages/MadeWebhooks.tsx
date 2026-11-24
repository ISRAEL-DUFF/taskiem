import { useState } from "react";
import type { ConnectionWebhook } from "../lib/connections";

/**
 * The webhook addresses of a connection just saved, each carrying the
 * token Taskiem made for it (generate: webhook_token): shown this once, to
 * paste into the service's dashboard. The service's deliveries are refused
 * without it.
 */
export function MadeWebhooks({ service, hooks }: { service: string; hooks: ConnectionWebhook[] }) {
  const [copied, setCopied] = useState("");
  return (
    <div className="notice" data-testid="made-webhooks">
      <p style={{ marginTop: 0 }}>
        <strong>Copy {hooks.length === 1 ? "this address" : "these addresses"} now</strong> and paste {hooks.length === 1 ? "it" : "each"} into {service}. {hooks.length === 1 ? "It carries" : "They carry"} a
        secret Taskiem made for this connection, and Taskiem refuses anything {service} sends without it. It is shown only once; to get a new one, connect again.
      </p>
      {hooks.map((h) => (
        <div key={h.trigger}>
          <p>
            <strong>{h.title}</strong>: {h.where}
          </p>
          <textarea readOnly rows={3} value={h.url} onFocus={(e) => e.target.select()} aria-label={`${h.title}: the address to paste into ${service}`} />
          <p style={{ marginBottom: 0 }}>
            <button
              type="button"
              onClick={() => {
                void navigator.clipboard?.writeText(h.url).then(() => setCopied(h.trigger));
              }}
            >
              {copied === h.trigger ? "Copied" : "Copy the address"}
            </button>
          </p>
        </div>
      ))}
    </div>
  );
}
