import { useState } from "react";
import { get, post } from "../api";
import { agentCodeOK, endReason, visitHours } from "../lib/agent";
import { Badge, ErrorBox, Field, Modal, Skeleton, fmtTime, useAction, useLoad } from "../ui";

interface Visit {
  id: string;
  agent_name: string;
  agent_code: string;
  kind: "onboarding" | "support";
  status: "pending" | "active" | "ended";
  consent_at: string | null;
  expires_at: string;
  ended_at: string | null;
  end_reason: string | null;
  created_at: string;
}

/** Field agents in the organisation (docs/agents.md): who worked on it and
 * when, ending a session, and inviting an agent back for a support visit. */
export function AgentVisits() {
  const visits = useLoad(() => get<{ visits: Visit[] }>("/v1/agent-visits"), []);
  const [inviting, setInviting] = useState(false);
  const act = useAction();
  const list = visits.data?.visits ?? [];
  return (
    <>
      <div className="toolbar" style={{ marginTop: 24 }}>
        <h2 className="grow" style={{ margin: 0 }}>
          Field agents
        </h2>
        <button onClick={() => setInviting(true)}>Invite an agent back</button>
      </div>
      <p className="hint">
        Taskiem's field agents work on your storefront and its tables only, and only in a session you agreed to. Everything they do is in the audit log as
        agent:&lt;code&gt;. On WhatsApp, send STOP AGENT to end every session at once.
      </p>
      <ErrorBox error={visits.error ?? act.error} />
      {!visits.data && !visits.error && <Skeleton rows={2} />}
      {visits.data && list.length === 0 && <p className="hint">No agent has worked on this organisation.</p>}
      {list.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Agent</th>
              <th>Kind</th>
              <th>Status</th>
              <th>Started</th>
              <th>Until</th>
              <th>How it ended</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {list.map((v) => (
              <tr key={v.id}>
                <td>
                  {v.agent_name}
                  <div className="hint">{v.agent_code}</div>
                </td>
                <td>{v.kind === "support" ? "Support visit" : "Sign-up"}</td>
                <td>
                  <Badge value={v.status} />
                </td>
                <td>{fmtTime(v.consent_at ?? v.created_at)}</td>
                <td>{fmtTime(v.ended_at ?? v.expires_at)}</td>
                <td>{v.status === "ended" ? endReason(v.end_reason) : "—"}</td>
                <td>
                  {v.status !== "ended" && (
                    <button
                      className="danger"
                      disabled={act.busy}
                      onClick={() =>
                        confirm(`End ${v.agent_name}'s session now?`) && void act.run(async () => (await post(`/v1/agent-visits/${v.id}/end`), visits.reload()))
                      }
                    >
                      End
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {inviting && <InviteAgent onClose={() => setInviting(false)} onDone={() => (setInviting(false), visits.reload())} />}
    </>
  );
}

function InviteAgent({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [code, setCode] = useState("");
  const [hours, setHours] = useState("2");
  const act = useAction();
  const h = visitHours(hours);
  return (
    <Modal title="Invite an agent back" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (h === null) return;
          void act.run(async () => (await post("/v1/agent-visits", { agent_code: code.trim().toUpperCase(), hours: h }), onDone()));
        }}
      >
        <p className="hint">For a support visit: the agent may work on your storefront and its tables from now, for the hours you choose. You can end it any time.</p>
        <Field label="Agent's code" hint="On the agent's card, such as AG-7KQ2M.">
          <input value={code} onChange={(e) => setCode(e.target.value)} placeholder="AG-7KQ2M" autoComplete="off" spellCheck={false} required autoFocus />
        </Field>
        <Field label="Hours" hint={h === null ? <span className="error-text">1 to 8 hours.</span> : "1 to 8 hours."}>
          <input type="number" min={1} max={8} step={1} value={hours} onChange={(e) => setHours(e.target.value)} />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || h === null || !agentCodeOK(code)}>
            Invite
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
