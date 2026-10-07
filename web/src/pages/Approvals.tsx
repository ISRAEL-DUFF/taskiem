import { useState } from "react";
import { Link } from "react-router-dom";
import { assert, passkeysSupported } from "../passkeys";
import { ApiError, del, get, post, type Approval, type Delegation, type PublishRequest } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, Field, Json, Modal, fmtTime, useAction, useLoad } from "../ui";

export function Approvals() {
  const { can } = useAuth();
  const { data, error, reload } = useLoad(() => get<{ approvals: Approval[] }>("/v1/approvals"), [], true);
  const [deciding, setDeciding] = useState<{ a: Approval; decision: "approved" | "rejected" } | null>(null);
  return (
    <>
      <h1>Approvals</h1>
      <p className="hint">
        Requests for a role you hold, or one delegated to you, that you have not decided. You cannot approve a run you started or a workflow version you
        wrote or published, and with distinct approvers you approve one level of a request at most.
      </p>
      <ErrorBox error={error} />
      {data && data.approvals.length === 0 && <div className="empty card">Nothing waiting for you.</div>}
      {data?.approvals.map((a) => (
        <div className="card" key={`${a.run_id}/${a.step_id}`} data-testid="approval">
          <div className="toolbar">
            <strong className="grow">
              {a.workflow} · {a.step_id}
            </strong>
            <span className="hint">
              {a.levels > 1 && `level ${a.level} of ${a.levels} · `}
              {a.approvals} of {a.required} approvals · {a.environment}
            </span>
          </div>
          {a.on_behalf_of && <p className="hint">You are covering for {a.on_behalf_of} under a delegation.</p>}
          {a.step_up && (
            <p className="hint">
              {a.step_up === "passkey"
                ? "This approval needs your passkey."
                : a.step_up === "whatsapp_pin"
                  ? "This approval needs your passkey or a code from your authenticator app here, or your WhatsApp PIN in WhatsApp."
                  : "This approval needs your passkey or a code from your authenticator app."}
            </p>
          )}
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
              {a.policy && ` · policy ${a.policy}`}
            </span>
            <Link to={`/runs/${a.run_id}`}>Open run</Link>
          </div>
        </div>
      ))}
      {deciding && <Decide {...deciding} onClose={() => setDeciding(null)} onDone={() => (setDeciding(null), reload())} />}
      {can("workflow.publish") && <PublishReviews />}
      <Delegations />
    </>
  );
}

function Decide({ a, decision, onClose, onDone }: { a: Approval; decision: "approved" | "rejected"; onClose: () => void; onDone: () => void }) {
  const [comment, setComment] = useState("");
  const [code, setCode] = useState("");
  const [needsCode, setNeedsCode] = useState(Boolean(a.step_up) && decision === "approved");
  const passkeyOnly = a.step_up === "passkey";
  const act = useAction();
  const send = async (extra: object) => {
    try {
      await post(`/v1/approvals/${a.run_id}/${encodeURIComponent(a.step_id)}`, { decision, ...(comment && { comment }), ...extra });
    } catch (e) {
      if (e instanceof ApiError && e.body.step_up) setNeedsCode(true);
      throw e;
    }
    onDone();
  };
  const submit = () => act.run(() => send(code ? { totp: code } : {}));
  const withPasskey = () => act.run(async () => send({ passkey: await assert("/v1/me/step-up/options") }));
  return (
    <Modal title={decision === "approved" ? "Approve" : "Reject"} onClose={onClose}>
      <p>
        {decision === "approved" ? "Approve" : "Reject"} <strong>{a.step_id}</strong> on {a.workflow}? Your decision and what you were shown are recorded.
      </p>
      <Field label="Comment (optional, audited)">
        <textarea rows={3} value={comment} onChange={(e) => setComment(e.target.value)} />
      </Field>
      {needsCode && passkeysSupported() && (
        <p>
          <button className="primary" disabled={act.busy} onClick={() => void withPasskey()}>
            Confirm with your passkey
          </button>{" "}
          <span className="hint">
            No passkey yet? Add one under <Link to="/account">Account</Link>.
          </span>
        </p>
      )}
      {needsCode && !passkeyOnly && (
        <Field label="Or an authenticator code" hint={<>No authenticator yet? Enrol one under <Link to="/account">Account</Link>.</>}>
          <input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))} />
        </Field>
      )}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button className={decision === "approved" ? "primary" : "danger"} disabled={act.busy || (needsCode && (passkeyOnly || code.length !== 6))} onClick={() => void submit()}>
          Confirm
        </button>
        <button onClick={onClose}>Cancel</button>
      </div>
    </Modal>
  );
}

/** Versions waiting for a second person to publish them (four-eyes). */
function PublishReviews() {
  const { data, error, reload } = useLoad(() => get<{ requests: PublishRequest[] }>("/v1/publish-requests"), []);
  const act = useAction();
  if (!data || data.requests.length === 0) return error ? <ErrorBox error={error} /> : null;
  const decide = (r: PublishRequest, verdict: "approve" | "reject") =>
    act.run(async () => (await post(`/v1/workflows/${r.workflow_id}/versions/${r.version}/publish/${verdict}`), reload()));
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Publishing to review</h2>
      <p className="hint">Publishing needs a second person. You cannot approve a version you wrote or asked to publish.</p>
      <ErrorBox error={act.error} />
      <table>
        <tbody>
          {data.requests.map((r) => (
            <tr key={`${r.workflow_id}/${r.version}`}>
              <td>
                <Link to={`/workflows/${r.workflow_id}?v=${r.version}`}>
                  {r.workflow} v{r.version}
                </Link>
              </td>
              <td className="hint">asked {fmtTime(r.requested_at)}</td>
              <td style={{ textAlign: "right" }}>
                <button className="primary" disabled={act.busy} onClick={() => void decide(r, "approve")}>
                  Publish
                </button>{" "}
                <button className="danger" disabled={act.busy} onClick={() => void decide(r, "reject")}>
                  Reject
                </button>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

/** Time-boxed handover of approval roles, for leave or cover. */
function Delegations() {
  const { me } = useAuth();
  const { data, error, reload } = useLoad(() => get<{ delegations: Delegation[] }>("/v1/delegations"), []);
  const [form, setForm] = useState({ to: "", roles: "", ends: "", reason: "" });
  const act = useAction();
  const approvalRoles = (me?.roles ?? []).filter((r) => !["owner", "admin", "builder", "operator", "auditor", "viewer", "approver"].includes(r));
  return (
    <section className="card">
      <h2 style={{ marginTop: 0 }}>Delegations</h2>
      <p className="hint">Hand an approval role to a colleague for a period (at most 90 days). Every delegation and every decision under it is audited.</p>
      <ErrorBox error={error ?? act.error} />
      {data && data.delegations.length > 0 && (
        <table style={{ marginBottom: 10 }}>
          <tbody>
            {data.delegations.map((d) => {
              const live = !d.revoked_at && new Date(d.ends_at) > new Date();
              return (
                <tr key={d.id}>
                  <td>
                    {d.from_email} → {d.to_email}
                  </td>
                  <td>{d.roles.join(", ")}</td>
                  <td className="hint">
                    {fmtTime(d.starts_at)} – {fmtTime(d.ends_at)} · {d.reason}
                    {d.revoked_at && " · revoked"}
                  </td>
                  <td style={{ textAlign: "right" }}>
                    {live && d.from_user === me?.user?.id && (
                      <button className="danger" disabled={act.busy} onClick={() => void act.run(async () => (await del(`/v1/delegations/${d.id}`), reload()))}>
                        Revoke
                      </button>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
      {approvalRoles.length > 0 && (
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              await post("/v1/delegations", {
                to: form.to,
                roles: form.roles.split(",").map((r) => r.trim()).filter(Boolean),
                ends_at: new Date(form.ends).toISOString(),
                reason: form.reason,
              });
              setForm({ to: "", roles: "", ends: "", reason: "" });
              reload();
            });
          }}
        >
          <input placeholder="colleague's email" value={form.to} onChange={(e) => setForm({ ...form, to: e.target.value })} required />
          <input placeholder={`roles (${approvalRoles.join(", ")})`} value={form.roles} onChange={(e) => setForm({ ...form, roles: e.target.value })} required />
          <input type="datetime-local" aria-label="Ends" value={form.ends} onChange={(e) => setForm({ ...form, ends: e.target.value })} required />
          <input placeholder="reason" value={form.reason} onChange={(e) => setForm({ ...form, reason: e.target.value })} required />
          <button type="submit" style={{ flex: "0 0 auto" }} disabled={act.busy}>
            Delegate
          </button>
        </form>
      )}
    </section>
  );
}
