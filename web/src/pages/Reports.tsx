import { useState } from "react";
import { get } from "../api";
import { ErrorBox, Json, fmtTime, useLoad } from "../ui";

const KINDS: [string, string, string][] = [
  ["approvals", "Approvals", "Every approval decision: who, for whom under a delegation, at which level, with what step-up, and the amount band."],
  ["effects", "Failed and reconciled effects", "Writes parked for an operator, results settled by asking the provider, and operator resolutions."],
  ["pii", "Access to personal data", "Every reveal of personal data in run history, and every erasure."],
  ["changes", "Workflow changes", "Every version: who wrote and published it, its commit or pull request, and what changed from the version before."],
  ["chain", "Audit chain", "The hash chain verified now, and the signed anchors made in the period."],
  ["secret-use", "Secret use", "How often each secret and connection was decrypted, per day; those not used; and the hourly digests checked against the reads."],
];

interface Report {
  kind: string;
  columns: string[];
  rows: Record<string, unknown>[];
  summary?: unknown;
}

function cell(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (Array.isArray(v)) return v.join("; ");
  if (typeof v === "string" && /^\d{4}-\d\d-\d\dT/.test(v)) return fmtTime(v);
  return String(v);
}

/** Compliance reports (spec 9.6) for examiners and internal audit. */
export function Reports() {
  const today = new Date().toISOString().slice(0, 10);
  const quarterAgo = new Date(Date.now() - 90 * 864e5).toISOString().slice(0, 10);
  const [kind, setKind] = useState("approvals");
  const [from, setFrom] = useState(quarterAgo);
  const [to, setTo] = useState(today);
  const q = `from=${from}&to=${to}`;
  const { data, error } = useLoad(() => get<Report>(`/v1/reports/${kind}?${q}`), [kind, q]);
  const about = KINDS.find(([k]) => k === kind);
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Reports
        </h1>
        <select aria-label="Report" value={kind} onChange={(e) => setKind(e.target.value)} style={{ width: "auto" }}>
          {KINDS.map(([k, label]) => (
            <option key={k} value={k}>
              {label}
            </option>
          ))}
        </select>
        <input type="date" aria-label="From" value={from} onChange={(e) => setFrom(e.target.value)} style={{ width: "auto" }} />
        <input type="date" aria-label="To" value={to} onChange={(e) => setTo(e.target.value)} style={{ width: "auto" }} />
        <a className="button" href={`/v1/reports/${kind}?${q}&format=csv`} download>
          Download CSV
        </a>
      </div>
      <p className="hint">{about?.[2]} Viewing and downloading reports is audited.</p>
      <ErrorBox error={error} />
      {data?.summary !== undefined && (
        <section className="card">
          <h2 style={{ marginTop: 0 }}>Summary</h2>
          <Json value={data.summary} />
        </section>
      )}
      {data && data.rows.length === 0 && <div className="empty card">Nothing in this period.</div>}
      {data && data.rows.length > 0 && (
        <div style={{ overflowX: "auto" }}>
          <table data-testid="report-table">
            <thead>
              <tr>
                {data.columns.map((c) => (
                  <th key={c}>{c.replace(/_/g, " ")}</th>
                ))}
              </tr>
            </thead>
            <tbody>
              {data.rows.map((r, i) => (
                <tr key={i}>
                  {data.columns.map((c) => (
                    <td key={c}>{cell(r[c])}</td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </>
  );
}
