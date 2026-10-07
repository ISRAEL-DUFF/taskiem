import { useState } from "react";
import { get, post, put, type PolicyVersion } from "../api";
import { useAuth } from "../auth";
import { Badge, ErrorBox, Field, Json, JsonInput, fmtTime, useAction, useLoad } from "../ui";

const example = {
  rules: [
    { when: "=subject.amount_kobo < 50000000", levels: [{ role: "credit_officer" }] },
    { when: "=subject.amount_kobo >= 50000000", levels: [{ role: "credit_officer" }, { role: "head_of_credit" }], step_up: "totp" },
  ],
  constraints: { forbid_self_approval: true, distinct_approvers: true },
  timeout: "24h",
  on_timeout: "escalate:head_of_operations",
};

/**
 * Approval policies (spec 9.1): reusable rules an approval step names. The
 * first rule whose condition holds for the approval's subject sets who
 * approves, in which order, and with what step-up.
 */
export function Policies() {
  const { can, me } = useAuth();
  const { data, error, reload } = useLoad(() => get<{ policies: PolicyVersion[] }>("/v1/policies"), []);
  const [name, setName] = useState("");
  const [doc, setDoc] = useState<unknown>(example);
  const act = useAction();
  const manage = can("policy.manage");
  return (
    <>
      <h1>Approval policies</h1>
      <p className="hint">
        An approval step names a policy in its configuration. Runs keep the version that was active when they started. Conditions are CEL over the
        approval's <code>subject</code>. A rule's <code>step_up</code> is the weakest second factor it takes: <code>whatsapp_pin</code> (the
        approver's WhatsApp PIN, or anything stronger), <code>totp</code> (an authenticator code or a passkey) or <code>passkey</code>.
      </p>
      <ErrorBox error={error ?? act.error} />
      {data?.policies.map((p) => (
        <section className="card" key={`${p.name}/${p.version}`} data-testid={`policy-${p.name}-${p.version}`}>
          <div className="toolbar">
            <strong className="grow">
              {p.name} v{p.version}
            </strong>
            <Badge value={p.state} />
            <span className="hint">
              by {p.created_by} {fmtTime(p.created_at)}
            </span>
          </div>
          <Json value={p.document} />
          <div className="toolbar" style={{ marginTop: 8 }}>
            {manage && (
              <button onClick={() => (setName(p.name), setDoc(p.document))} disabled={act.busy}>
                Edit as new version
              </button>
            )}
            {manage && p.state === "pending" && p.created_by !== me?.user?.id && (
              <>
                <button className="primary" disabled={act.busy} onClick={() => void act.run(async () => (await post(`/v1/policies/${p.name}/versions/${p.version}/approve`), reload()))}>
                  Approve
                </button>
                <button className="danger" disabled={act.busy} onClick={() => void act.run(async () => (await post(`/v1/policies/${p.name}/versions/${p.version}/reject`), reload()))}>
                  Reject
                </button>
              </>
            )}
            {p.state === "pending" && <span className="hint">Waiting for a second person to approve it.</span>}
          </div>
        </section>
      ))}
      {manage && (
        <section className="card">
          <h2 style={{ marginTop: 0 }}>Save a policy version</h2>
          <Field label="Name">
            <input className="mono" value={name} onChange={(e) => setName(e.target.value)} placeholder="high_value_disbursement" />
          </Field>
          <Field label="Policy (JSON)">
            <JsonInput value={doc} onChange={setDoc} rows={16} />
          </Field>
          <button className="primary" disabled={act.busy || !name || doc === undefined} onClick={() => void act.run(async () => (await put(`/v1/policies/${name}`, { document: doc }), reload()))}>
            Save
          </button>
        </section>
      )}
    </>
  );
}
