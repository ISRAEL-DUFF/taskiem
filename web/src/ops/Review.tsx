// The catalogue review queue (docs/connector-submissions.md#for-reviewers):
// the submission, its automated checks, and the eight-item checklist,
// every item confirmed to approve. The reviewer is the signed-in operator;
// four eyes is enforced by the database.

import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { missingItems, reviewTarget } from "../lib/ops";
import { Badge, ErrorBox, Field, fmtTime, useAction, useLoad } from "../ui";
import { opsGet, withStepUp, type OpsMe, type QueueItem, type SubmissionDetail } from "./client";

export function Queue() {
  const [all, setAll] = useState(false);
  const { data, error } = useLoad(() => opsGet<{ submissions: QueueItem[] }>(`/catalogue/queue${all ? "?state=all" : ""}`), [all]);
  return (
    <>
      <h1>Review queue</h1>
      <p className="hint">Connector versions that passed the automated checks and wait for a person. Open one to review it.</p>
      <div className="tabs">
        <button className={all ? "" : "active"} onClick={() => setAll(false)}>
          In review
        </button>
        <button className={all ? "active" : ""} onClick={() => setAll(true)}>
          Every state
        </button>
      </div>
      <ErrorBox error={error} />
      {data && data.submissions.length === 0 && <div className="empty">Nothing waits for review.</div>}
      {data && data.submissions.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Connector</th>
              <th>Publisher</th>
              <th>State</th>
              <th>Checks</th>
              <th>Licence</th>
              <th>Submitted</th>
              <th>Reviewer</th>
            </tr>
          </thead>
          <tbody>
            {data.submissions.map((s) => (
              <tr key={s.id}>
                <td>
                  <Link to={`/ops/catalogue/${s.id}`}>
                    {s.connector} {s.version}
                  </Link>
                </td>
                <td>{s.publisher}</td>
                <td>
                  <Badge value={s.state} />
                </td>
                <td>{s.checks_passed ? "passed" : "failed"}</td>
                <td>{s.licence}</td>
                <td>{fmtTime(s.submitted_at)}</td>
                <td>{s.reviewed_by ?? "—"}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

const classLabel: Record<string, string> = {
  read: "read",
  idempotent_write: "idempotent write",
  reconcilable_write: "reconcilable write",
  unsafe_write: "unsafe write",
};

export function Submission({ me }: { me: OpsMe }) {
  const { id = "" } = useParams();
  const { data, error, reload } = useLoad(() => opsGet<SubmissionDetail>(`/catalogue/submissions/${id}`), [id]);
  const [confirmed, setConfirmed] = useState<Record<string, boolean>>({});
  const [note, setNote] = useState("");
  const act = useAction();
  const [done, setDone] = useState("");
  if (error) return <ErrorBox error={error} />;
  if (!data) return <div className="empty">Loading…</div>;
  const s = data.submission;
  const missing = missingItems(data.checklist, confirmed);
  const decide = (decision: "approve" | "reject") =>
    act.run(async () => {
      const out = await withStepUp<{ state: string }>("ops.catalogue.review", reviewTarget(s.id, decision), `/catalogue/submissions/${s.id}/review`, {
        decision,
        note,
        checklist: confirmed,
      });
      setDone(out.state);
      reload();
    });
  const conformance = s.checks.conformance;
  return (
    <>
      <p className="hint">
        <Link to="/ops/catalogue">Review queue</Link>
      </p>
      <h1>
        {s.connector} {s.version}
      </h1>
      <div className="toolbar">
        <Badge value={s.state} />
        <span className="hint">
          by {s.publisher} (tenant <code>{s.publisher_tenant}</code>), submitted {fmtTime(s.submitted_at)} by <code>{s.submitted_by}</code>
        </span>
      </div>
      {done && (
        <div className="notice" role="status">
          Submission {done}.
        </div>
      )}

      <div className="card">
        <h2>Package</h2>
        <dl className="kv">
          <dt>Package digest</dt>
          <dd>
            <code>{s.package_digest}</code>
          </dd>
          <dt>Module SHA-256</dt>
          <dd>
            <code>{s.module_sha256}</code>
          </dd>
          <dt>Signing key</dt>
          <dd>
            <code>{s.key_id}</code>
          </dd>
          <dt>Licence</dt>
          <dd>{s.licence}</dd>
          <dt>Source</dt>
          <dd>{s.source_url ? <a href={s.source_url}>{s.source_url}</a> : "—"}</dd>
          <dt>Attestation</dt>
          <dd>
            <code>{JSON.stringify(s.attestation)}</code>
          </dd>
        </dl>
      </div>

      {data.summary && (
        <div className="card">
          <h2>{data.summary.name}</h2>
          <p>{data.summary.description}</p>
          <dl className="kv">
            <dt>Hosts</dt>
            <dd>{data.summary.hosts.join(", ") || "—"}</dd>
            <dt>Actions</dt>
            <dd>
              {Object.entries(data.summary.actions)
                .map(([a, c]) => `${a} (${classLabel[c] ?? c})`)
                .join(", ")}
            </dd>
            <dt>Personal data</dt>
            <dd>{data.summary.pii.join(", ") || "none declared"}</dd>
            <dt>Triggers</dt>
            <dd>{data.summary.triggers.join(", ") || "—"}</dd>
          </dl>
        </div>
      )}

      <div className="card">
        <h2>Automated checks</h2>
        {(s.checks.checks ?? []).length === 0 && <p className="hint">No check report{s.checks.passed ? " (marked passed)" : ""}.</p>}
        <ul aria-label="Automated checks">
          {(s.checks.checks ?? []).map((c) => (
            <li key={c.name}>
              <strong style={{ color: c.pass ? "var(--ok)" : "var(--danger)" }}>{c.pass ? "pass" : "FAIL"}</strong> {c.name}
              {c.detail ? `: ${c.detail}` : ""}
            </li>
          ))}
        </ul>
        {conformance?.results && conformance.results.length > 0 && (
          <>
            <h3>Conformance cases</h3>
            <ul>
              {conformance.results.map((r) => (
                <li key={r.case}>
                  {r.pass ? "pass" : "FAIL"} {r.case} ({r.action}){r.problems?.length ? `: ${r.problems.join("; ")}` : ""}
                </li>
              ))}
            </ul>
          </>
        )}
        {conformance?.read_writes && conformance.read_writes.length > 0 && (
          <div className="warning">Read actions that sent other than GET or HEAD: {conformance.read_writes.join(", ")}. Look hard at their class.</div>
        )}
        {data.lint.length > 0 && (
          <>
            <h3>Lint</h3>
            <ul>
              {data.lint.map((f, i) => (
                <li key={i}>
                  {f.level} {f.path}: {f.message}
                </li>
              ))}
            </ul>
          </>
        )}
        <details>
          <summary>Manifest</summary>
          <pre>{s.manifest}</pre>
        </details>
      </div>

      {s.state === "in_review" ? (
        <div className="card">
          <h2>Review</h2>
          <p className="hint">
            You review as <strong>{me.operator.email}</strong>
            {me.reviewer ? "" : " (not on the reviewer list: the database will refuse)"}. Someone from the publisher's organisation cannot review it.
            Confirm every item to approve; a rejection needs only a note saying what to change.
          </p>
          <ul className="checklist" aria-label="Review checklist">
            {data.checklist.map((c) => (
              <li key={c.key} className={confirmed[c.key] ? "done" : ""}>
                <label className="inline">
                  <input type="checkbox" checked={!!confirmed[c.key]} onChange={(e) => setConfirmed({ ...confirmed, [c.key]: e.target.checked })} />
                  <span>
                    <strong>{c.key}</strong>: {c.text}
                  </span>
                </label>
              </li>
            ))}
          </ul>
          <Field label="Note to the publisher">
            <textarea rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button className="primary" disabled={act.busy || missing.length > 0 || !note.trim()} onClick={() => void decide("approve")}>
              Approve
            </button>
            <button className="danger" disabled={act.busy || !note.trim()} onClick={() => void decide("reject")}>
              Reject
            </button>
            {missing.length > 0 && <span className="hint">Not confirmed: {missing.join(", ")}</span>}
          </div>
        </div>
      ) : (
        s.reviewed_by && (
          <div className="card">
            <h2>Review</h2>
            <p>
              Reviewed by <strong>{s.reviewed_by}</strong> {fmtTime(s.reviewed_at)}: {s.review_note}
            </p>
          </div>
        )
      )}

      <h2>History</h2>
      <table>
        <thead>
          <tr>
            <th>When</th>
            <th>Event</th>
            <th>By</th>
            <th>Note</th>
          </tr>
        </thead>
        <tbody>
          {data.history.map((h, i) => (
            <tr key={i}>
              <td>{fmtTime(h.at)}</td>
              <td>{h.event}</td>
              <td>{h.actor}</td>
              <td>{h.note ?? ""}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </>
  );
}
