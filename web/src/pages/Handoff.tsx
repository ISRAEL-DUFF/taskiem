import { useState } from "react";
import { Link } from "react-router-dom";
import { assert, passkeysSupported } from "../passkeys";
import { ApiError, get, post } from "../api";
import { ErrorBox, Field, Json, fmtTime, useAction, useLoad } from "../ui";

interface HandoffInfo {
  run_id: string;
  step_id: string;
  workflow: string;
  environment: string;
  decision: "approved" | "rejected";
  status: string;
  step_up: string | null;
  level: number;
  levels: number;
  expires_at: string;
  subject: unknown;
}

/** A decision started in WhatsApp whose approval policy needs step-up: the
 * approver confirms that exact decision here with a passkey or an
 * authenticator code. The link's token is in the URL fragment, which the
 * browser never sends to a server. */
export function Handoff() {
  const token = window.location.hash.replace(/^#/, "");
  const info = useLoad(() => get<HandoffInfo>(`/v1/whatsapp/handoff/${encodeURIComponent(token)}`), [token]);
  const [code, setCode] = useState("");
  const [done, setDone] = useState<string>();
  const act = useAction();
  const d = info.data;
  const send = async (body: object) => {
    try {
      const r = await post<{ status: string }>(`/v1/whatsapp/handoff/${encodeURIComponent(token)}`, body);
      setDone(r.status);
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) info.reload();
      throw e;
    }
  };
  if (!token) return <div className="empty card">This page opens from a link Taskiem sends you on WhatsApp.</div>;
  return (
    <div className="card" style={{ maxWidth: 720 }}>
      <h1>Confirm your decision</h1>
      <ErrorBox error={info.error} />
      {d && !done && (
        <>
          <p>
            You chose to <strong data-testid="handoff-decision">{d.decision === "approved" ? "approve" : "reject"}</strong> <strong>{d.step_id}</strong> on {d.workflow} ({d.environment}
            {d.levels > 1 && `, level ${d.level} of ${d.levels}`}) in WhatsApp. This approval needs your {d.step_up === "passkey" ? "passkey" : "passkey or authenticator code"}. The link works
            until {fmtTime(d.expires_at)}.
          </p>
          <Json value={d.subject} />
          {passkeysSupported() && (
            <p>
              <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => send({ passkey: await assert("/v1/me/step-up/options") }))}>
                Confirm with your passkey
              </button>
            </p>
          )}
          {d.step_up !== "passkey" && (
            <form
              className="row"
              style={{ alignItems: "flex-end" }}
              onSubmit={(e) => {
                e.preventDefault();
                void act.run(() => send({ totp: code }));
              }}
            >
              <Field label="Or a code from your authenticator app">
                <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
              </Field>
              <button className="primary" type="submit" disabled={act.busy || code.length !== 6}>
                Confirm
              </button>
            </form>
          )}
          <ErrorBox error={act.error} />
        </>
      )}
      {done && (
        <p data-testid="handoff-done">
          Recorded. The request is now <strong>{done}</strong>. <Link to={`/runs/${d?.run_id ?? ""}`}>Open the run</Link>
        </p>
      )}
    </div>
  );
}
