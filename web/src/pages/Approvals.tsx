import { useState } from "react";
import { Link } from "react-router-dom";
import { get, post, type Approval } from "../api";
import { ErrorBox, Field, Json, Modal, fmtTime, useAction, useLoad } from "../ui";

export function Approvals() {
  const { data, error, reload } = useLoad(() => get<{ approvals: Approval[] }>("/v1/approvals"), [], true);
  const [deciding, setDeciding] = useState<{ a: Approval; decision: "approved" | "rejected" } | null>(null);
  return (
    <>
      <h1>Approvals</h1>
      <p className="hint">Requests for a role you hold that you have not decided. You cannot approve a run you started or a workflow version you wrote or published.</p>
      <ErrorBox error={error} />
      {data && data.approvals.length === 0 && <div className="empty card">Nothing waiting for you.</div>}
      {data?.approvals.map((a) => (
        <div className="card" key={`${a.run_id}/${a.step_id}`} data-testid="approval">
          <div className="toolbar">
            <strong className="grow">
              {a.workflow} · {a.step_id}
            </strong>
            <span className="hint">
              {a.approvals} of {a.required} approvals · {a.environment}
            </span>
          </div>
          <Json value={a.subject} />
          <div className="toolbar" style={{ marginTop: 8 }}>
            <button className="primary" onClick={() => setDeciding({ a, decision: "approved" })}>
              Approve
            </button>
            <button className="danger" onClick={() => setDeciding({ a, decision: "rejected" })}>
              Reject
            </button>
            <span className="grow" />
            <span className="hint">
              requested {fmtTime(a.requested_at)}
              {a.timeout_at && ` · expires ${fmtTime(a.timeout_at)}`}
              {a.role && ` · role ${a.role}`}
            </span>
            <Link to={`/runs/${a.run_id}`}>Open run</Link>
          </div>
        </div>
      ))}
      {deciding && <Decide {...deciding} onClose={() => setDeciding(null)} onDone={() => (setDeciding(null), reload())} />}
    </>
  );
}

function Decide({ a, decision, onClose, onDone }: { a: Approval; decision: "approved" | "rejected"; onClose: () => void; onDone: () => void }) {
  const [comment, setComment] = useState("");
  const act = useAction();
  return (
    <Modal title={decision === "approved" ? "Approve" : "Reject"} onClose={onClose}>
      <p>
        {decision === "approved" ? "Approve" : "Reject"} <strong>{a.step_id}</strong> on {a.workflow}? Your decision and what you were shown are recorded.
      </p>
      <Field label="Comment (optional, audited)">
        <textarea rows={3} value={comment} onChange={(e) => setComment(e.target.value)} />
      </Field>
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button
          className={decision === "approved" ? "primary" : "danger"}
          disabled={act.busy}
          onClick={() => void act.run(async () => (await post(`/v1/approvals/${a.run_id}/${encodeURIComponent(a.step_id)}`, { decision, ...(comment && { comment }) }), onDone()))}
        >
          Confirm
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}
