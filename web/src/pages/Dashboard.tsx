import { useState } from "react";
import { Link } from "react-router-dom";
import { get } from "../api";
import { EnvSelect } from "../environments";
import { ErrorBox, fmtTime, useLoad } from "../ui";

interface Day {
  day: string;
  completed: number;
  failed: number;
  other: number;
}
interface Failure {
  workflow?: string;
  step?: string;
  connector?: string;
  kind?: string;
  count: number;
}
interface DashboardData {
  days: number;
  runs: { total: number; completed: number; failed: number; cancelled: number; active: number };
  success_rate: number | null;
  duration_seconds: { p50: number | null; p95: number | null };
  needs_reconciliation: number;
  approvals_pending: { count: number; oldest: string | null };
  daily: Day[];
  failures_by_step: Failure[];
  failures_by_connector: Failure[];
  workflows: { id: string; name: string; runs: number; completed: number; failed: number; success_rate: number | null; p95_seconds: number | null }[];
}

const pct = (v: number | null) => (v === null ? "–" : `${(v * 100).toFixed(1)}%`);
const dur = (s: number | null) => (s === null ? "–" : s < 60 ? `${s.toFixed(1)}s` : s < 3600 ? `${(s / 60).toFixed(1)}m` : `${(s / 3600).toFixed(1)}h`);

export function Dashboard() {
  const [env, setEnv] = useState("prod");
  const [days, setDays] = useState(7);
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
  const { data, error } = useLoad(() => get<DashboardData>(`/v1/dashboard?environment=${encodeURIComponent(env)}&days=${days}&tz=${encodeURIComponent(tz)}`), [env, days], true);
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Dashboard
        </h1>
        <EnvSelect value={env} onChange={setEnv} />
        <select aria-label="Period" style={{ width: "auto" }} value={days} onChange={(e) => setDays(Number(e.target.value))}>
          <option value={1}>24 hours</option>
          <option value={7}>7 days</option>
          <option value={30}>30 days</option>
          <option value={90}>90 days</option>
        </select>
      </div>
      <ErrorBox error={error} />
      {data && (
        <>
          <div className="tiles">
            <Tile label="Success rate" value={pct(data.success_rate)} hint={`${data.runs.completed} completed, ${data.runs.failed} failed`} />
            <Tile label="Runs" value={String(data.runs.total)} hint={`${data.runs.active} in progress`} />
            <Tile label="Duration p95" value={dur(data.duration_seconds.p95)} hint={`p50 ${dur(data.duration_seconds.p50)}`} />
            <Tile label="Needs reconciliation" value={String(data.needs_reconciliation)} hint="now" warn={data.needs_reconciliation > 0} link="/runs?status=needs_reconciliation" />
            <Tile
              label="Approvals pending"
              value={String(data.approvals_pending.count)}
              hint={data.approvals_pending.oldest ? `oldest ${fmtTime(data.approvals_pending.oldest)}` : "now"}
              link="/approvals"
            />
          </div>
          <section className="card">
            <h2 style={{ marginTop: 0 }}>Runs per day</h2>
            <DailyChart days={data.daily} />
          </section>
          <div className="row" style={{ alignItems: "flex-start" }}>
            <section className="card" style={{ flex: 1, minWidth: 280 }}>
              <h2 style={{ marginTop: 0 }}>Failing steps</h2>
              <FailureTable rows={data.failures_by_step} cols={["workflow", "step", "connector"]} />
            </section>
            <section className="card" style={{ flex: 1, minWidth: 280 }}>
              <h2 style={{ marginTop: 0 }}>Failing connectors</h2>
              <FailureTable rows={data.failures_by_connector} cols={["connector", "kind"]} />
            </section>
          </div>
          <section className="card">
            <h2 style={{ marginTop: 0 }}>Workflows</h2>
            <table>
              <thead>
                <tr>
                  <th>Workflow</th>
                  <th>Runs</th>
                  <th>Success rate</th>
                  <th>Failed</th>
                  <th>p95</th>
                </tr>
              </thead>
              <tbody>
                {data.workflows.map((w) => (
                  <tr key={w.id}>
                    <td>
                      <Link to={`/workflows/${w.id}`}>{w.name}</Link>
                    </td>
                    <td>{w.runs}</td>
                    <td>{pct(w.success_rate)}</td>
                    <td>{w.failed}</td>
                    <td>{dur(w.p95_seconds)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {data.workflows.length === 0 && <p className="hint">No runs in this period.</p>}
          </section>
        </>
      )}
    </>
  );
}

function Tile({ label, value, hint, warn, link }: { label: string; value: string; hint: string; warn?: boolean; link?: string }) {
  const body = (
    <>
      <div className="hint">{label}</div>
      <div className={`tile-value${warn ? " warn" : ""}`} aria-label={label}>
        {value}
      </div>
      <div className="hint">{hint}</div>
    </>
  );
  return <div className="card tile">{link ? <Link to={link}>{body}</Link> : body}</div>;
}

function FailureTable({ rows, cols }: { rows: Failure[]; cols: (keyof Failure)[] }) {
  if (rows.length === 0) return <p className="hint">None in this period.</p>;
  return (
    <table>
      <thead>
        <tr>
          {cols.map((c) => (
            <th key={c}>{c}</th>
          ))}
          <th>Failed attempts</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((r, i) => (
          <tr key={i}>
            {cols.map((c) => (
              <td key={c}>{r[c] || <span className="hint">–</span>}</td>
            ))}
            <td>{r.count}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

/** Stacked bars per day: completed, failed, other. */
function DailyChart({ days }: { days: Day[] }) {
  const max = Math.max(1, ...days.map((d) => d.completed + d.failed + d.other));
  const w = 100 / Math.max(days.length, 1);
  const h = 120;
  return (
    <svg className="chart" viewBox={`0 0 100 ${h + 14}`} preserveAspectRatio="none" role="img" aria-label="Runs per day">
      {days.map((d, i) => {
        const x = i * w + w * 0.15;
        const bw = w * 0.7;
        let y = h;
        const seg = (n: number, cls: string) => {
          const sh = (n / max) * h;
          y -= sh;
          return n > 0 ? <rect key={cls} className={cls} x={x} y={y} width={bw} height={sh} /> : null;
        };
        return (
          <g key={d.day}>
            <title>{`${d.day}: ${d.completed} completed, ${d.failed} failed, ${d.other} other`}</title>
            {seg(d.completed, "bar-ok")}
            {seg(d.failed, "bar-failed")}
            {seg(d.other, "bar-other")}
            {(days.length <= 14 || i % Math.ceil(days.length / 10) === 0) && (
              <text x={x + bw / 2} y={h + 11} textAnchor="middle" className="chart-label">
                {d.day.slice(5)}
              </text>
            )}
          </g>
        );
      })}
    </svg>
  );
}
