// The webhook trigger's settings (wd-v1 webhook triggers): its path, the
// methods it takes, how senders authenticate, which web pages may call it
// (CORS), and "Listen for a test call", which shows what a sender sends
// before the workflow is published.
import { useEffect, useState } from "react";
import { get, post, type JSONSchema } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";
import { METHODS, parseOrigins, schemaOf, withMethod } from "../lib/webhook";

const AUTHS: Record<string, { label: string; secret: string }> = {
  hmac: { label: "HMAC-SHA256 signature", secret: "the signing key; senders send X-Taskiem-Signature: sha256=<hex HMAC of the body>" },
  bearer: { label: "Bearer token", secret: "the token; senders send Authorization: Bearer <token>" },
  basic: { label: "User name and password (basic)", secret: "user:password; senders send Authorization: Basic" },
  jwt: { label: "Signed token (JWT)", secret: "the shared key (HS256/384/512) or a PEM public key (RS*, ES256/384); senders send Authorization: Bearer <jwt> with an exp" },
  none: { label: "None", secret: "" },
};

export function WebhookTriggerSettings({ wdId, config, onChange }: { wdId: string; config: Record<string, unknown>; onChange: (patch: Record<string, unknown>) => void }) {
  const auth = String(config.auth ?? "hmac");
  const methods = Array.isArray(config.methods) && config.methods.length ? (config.methods as string[]) : ["POST"];
  const jwt = (config.jwt ?? {}) as Record<string, unknown>;
  const cors = (config.cors ?? {}) as { origins?: string[] };
  const [origins, setOrigins] = useState((cors.origins ?? []).join(", "));
  const setMethod = (m: string, on: boolean) => onChange({ methods: withMethod(methods, m, on) });
  const setJwt = (patch: Record<string, unknown>) => {
    const next = Object.fromEntries(Object.entries({ ...jwt, ...patch }).filter(([, v]) => v !== undefined && v !== ""));
    onChange({ jwt: Object.keys(next).length ? next : undefined });
  };
  return (
    <>
      <div className="row">
        <Field label="Path">
          <input className="mono" value={String(config.path ?? "")} onChange={(e) => onChange({ path: e.target.value })} />
        </Field>
        <Field label="Authentication" hint={auth === "none" ? "Anyone with the address can start it." : `The environment secret webhook_${wdId} holds ${AUTHS[auth]?.secret ?? "the key"}.`}>
          <select
            value={auth}
            onChange={(e) => {
              const next = e.target.value;
              onChange({ auth: next, jwt: next === "jwt" ? config.jwt : undefined });
            }}
          >
            {Object.entries(AUTHS).map(([k, v]) => (
              <option key={k} value={k}>
                {v.label}
              </option>
            ))}
          </select>
        </Field>
      </div>
      <fieldset className="field" style={{ border: "none", padding: 0, margin: 0 }} aria-describedby="webhook-methods-hint">
        <legend>Methods</legend>
        <div className="row">
          {METHODS.map((m) => (
            <label key={m} className="inline">
              <input type="checkbox" checked={methods.includes(m)} onChange={(e) => setMethod(m, e.target.checked)} /> {m}
            </label>
          ))}
        </div>
        <div className="hint" id="webhook-methods-hint">
          A GET or DELETE has no body: its query parameters are trigger.body, checked against the inputs schema as text.
        </div>
      </fieldset>
      {auth === "jwt" && (
        <div className="row">
          <Field label="Issuer (iss)" hint="Optional: the token must say it comes from here.">
            <input value={String(jwt.issuer ?? "")} onChange={(e) => setJwt({ issuer: e.target.value })} />
          </Field>
          <Field label="Audience (aud)" hint="Optional: the token must be meant for this.">
            <input value={String(jwt.audience ?? "")} onChange={(e) => setJwt({ audience: e.target.value })} />
          </Field>
        </div>
      )}
      <Field
        label="Web pages that may call it (CORS)"
        hint={
          auth === "hmac" || auth === "bearer" || auth === "basic"
            ? "A web page cannot keep this webhook's secret from its visitors: choose JWT or None to let web pages call it."
            : "Origins such as https://shop.example.com, separated by commas, or * for any. Empty: only servers call it."
        }
      >
        <input
          className="mono"
          value={origins}
          placeholder="https://shop.example.com"
          onChange={(e) => {
            setOrigins(e.target.value);
            const list = parseOrigins(e.target.value);
            onChange({ cors: list.length ? { origins: list } : undefined });
          }}
        />
      </Field>
    </>
  );
}

type ListenCall = {
  method: string;
  body: unknown;
  headers: Record<string, string>;
  query: Record<string, string>;
  bytes: number;
  received_at: string;
  checks: Record<string, string>;
};
type ListenState = { status: "waiting" | "captured" | "expired"; expires_at: string; call?: ListenCall };

/**
 * "Listen for a test call": a test address for the webhook as it is being
 * built, saved or not. The first call to it within two minutes shows here
 * with what the webhook would say of it; it starts nothing.
 */
export function WebhookListener({ workflow, config, onUseSchema }: { workflow: string; config: Record<string, unknown>; onUseSchema: (schema: JSONSchema) => void }) {
  const { can } = useAuth();
  const act = useAction();
  const [listen, setListen] = useState<{ id: string; url: string; expires_at: string } | null>(null);
  const [state, setState] = useState<ListenState | null>(null);
  const waiting = !!listen && (!state || state.status === "waiting");
  const polled = useLoad(() => (listen ? get<ListenState>(`/v1/workflows/${workflow}/webhook-listens/${listen.id}`) : Promise.resolve(null)), [listen?.id], waiting);
  useEffect(() => {
    if (polled.data) setState(polled.data);
  }, [polled.data]);
  if (!can("workflow.edit")) return null;
  const url = listen ? (listen.url.startsWith("/") ? window.location.origin + listen.url : listen.url) : "";
  const method = (Array.isArray(config.methods) && config.methods.length ? (config.methods as string[]) : ["POST"])[0];
  const start = () =>
    void act.run(async () => {
      setState(null);
      setListen(await post<{ id: string; url: string; expires_at: string }>(`/v1/workflows/${workflow}/webhook-listens`, { config }));
    }, false);
  return (
    <div className="card" aria-label="Test call">
      <h3>Listen for a test call</h3>
      <p className="hint">Get a test address, send one call to it from the other system, and see exactly what it sends and whether this webhook would take it. Nothing runs.</p>
      <ErrorBox error={act.error ?? polled.error} />
      {!listen || state?.status === "expired" || state?.status === "captured" ? (
        <button disabled={act.busy} onClick={start}>
          {state ? "Listen again" : "Listen for a test call"}
        </button>
      ) : (
        <>
          <p>
            Waiting for a call until {fmtTime(listen.expires_at)}. Send it to:
          </p>
          <pre className="mono">{url}</pre>
          <p className="hint">For example:</p>
          <pre className="mono">{method === "GET" || method === "DELETE" ? `curl -X ${method} '${url}?example=1'` : `curl -X ${method} '${url}' -H 'Content-Type: application/json' -d '{"example": 1}'`}</pre>
          <button onClick={() => void navigator.clipboard?.writeText(url)}>Copy address</button>
        </>
      )}
      {state?.status === "expired" && <p className="hint">No call came in time.</p>}
      {state?.status === "captured" && state.call && <CallView call={state.call} onUseSchema={onUseSchema} />}
    </div>
  );
}

function CallView({ call, onUseSchema }: { call: ListenCall; onUseSchema: (schema: JSONSchema) => void }) {
  const checks = Object.entries(call.checks ?? {});
  return (
    <>
      <p>
        <strong>{call.method}</strong> received {fmtTime(call.received_at)} ({call.bytes} bytes)
      </p>
      <ul aria-label="Checks">
        {checks.map(([k, v]) => (
          <li key={k}>
            {v === "ok" ? "✓" : "✗"} {{ method: "Method", origin: "Web page origin", auth: "Authentication" }[k] ?? k}: {v === "ok" ? "would be taken" : v}
          </li>
        ))}
      </ul>
      <Shown label="trigger.body" value={call.body} />
      {Object.keys(call.query ?? {}).length > 0 && <Shown label="trigger.query" value={call.query} />}
      <Shown label="trigger.headers" value={call.headers} hint="Authorization and cookies are never kept." />
      {call.body !== null && typeof call.body === "object" && !Array.isArray(call.body) && (
        <button onClick={() => onUseSchema(schemaOf(call.body))}>Use as the inputs schema</button>
      )}
    </>
  );
}

/** A part of the call, as the run's expressions would read it. */
function Shown({ label, value, hint }: { label: string; value: unknown; hint?: string }) {
  return (
    <div className="field">
      <span className="mono">{label}</span>
      <pre className="mono">{JSON.stringify(value, null, 2)}</pre>
      {hint && <div className="hint">{hint}</div>}
    </div>
  );
}
