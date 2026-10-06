import { useState } from "react";
import { del, post } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, useAction } from "../ui";

/** The signed-in member's own security settings: the authenticator app used
 * for step-up on approvals that need it. */
export function Account() {
  const { me, refresh } = useAuth();
  const [pending, setPending] = useState<{ secret: string; uri: string }>();
  const [code, setCode] = useState("");
  const act = useAction();
  return (
    <>
      <h1>Account</h1>
      <section className="card">
        <h2 style={{ marginTop: 0 }}>Authenticator app</h2>
        <p className="hint">
          Some approvals need a fresh code from an authenticator app (step-up), set by the approval policy. Codes work once.
        </p>
        <ErrorBox error={act.error} />
        {me?.totp && !pending && (
          <>
            <p>An authenticator is enrolled.</p>
            <div className="row">
              <Field label="Current code, to remove it">
                <input inputMode="numeric" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
              </Field>
              <button className="danger" disabled={act.busy || code.length !== 6} onClick={() => void act.run(async () => (await del("/v1/me/totp", { code }), setCode(""), await refresh()))}>
                Remove
              </button>
            </div>
          </>
        )}
        {!me?.totp && !pending && (
          <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => setPending(await post<{ secret: string; uri: string }>("/v1/me/totp")))}>
            Set up an authenticator
          </button>
        )}
        {pending && (
          <>
            <p>
              Add this account to your authenticator app with the key below (or open the link on the phone that has the app), then enter the code it shows.
            </p>
            <p>
              Key <code data-testid="totp-secret">{pending.secret}</code>
              <br />
              <a href={pending.uri}>Open in authenticator app</a>
            </p>
            <div className="row">
              <Field label="Code from the app">
                <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
              </Field>
              <button
                className="primary"
                disabled={act.busy || code.length !== 6}
                onClick={() => void act.run(async () => (await post("/v1/me/totp/confirm", { code }), setPending(undefined), setCode(""), await refresh()))}
              >
                Confirm
              </button>
            </div>
          </>
        )}
      </section>
    </>
  );
}
