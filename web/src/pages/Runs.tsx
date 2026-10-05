import { useState } from "react";
import { useNavigate, useSearchParams } from "react-router-dom";
import { get, type RunSummary } from "../api";
import { Badge, ErrorBox, fmtTime, useAction, useLoad } from "../ui";
import { duration } from "../lib/timeline";

const STATUSES = ["", "running", "waiting", "queued", "needs_reconciliation", "completed", "failed", "cancelled"];

export function Runs() {
  const nav = useNavigate();
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "";
  const workflow = params.get("workflow") ?? "";
  const query = new URLSearchParams({ ...(status && { status }), ...(workflow && { workflow }), limit: "50" });
  const { data, error } = useLoad(() => get<{ runs: RunSummary[] }>(`/v1/runs?${query}`), [status, workflow], true);
  const [more, setMore] = useState<RunSummary[]>([]);
  const act = useAction();
  const runs = [...(data?.runs ?? []), ...more.filter((m) => !data?.runs.some((r) => r.id === m.id))];
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Runs
        </h1>
        <select
          aria-label="Status"
          style={{ width: "auto" }}
          value={status}
          onChange={(e) => {
            setMore([]);
            const p = new URLSearchParams(params);
            if (e.target.value) p.set("status", e.target.value);
            else p.delete("status");
            setParams(p);
          }}
        >
          {STATUSES.map((s) => (
            <option key={s} value={s}>
              {s ? s.replaceAll("_", " ") : "All statuses"}
            </option>
          ))}
        </select>
      </div>
      <ErrorBox error={error ?? act.error} />
      {data && runs.length === 0 && <div className="empty card">No runs.</div>}
      {runs.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Workflow</th>
              <th>Status</th>
              <th>Environment</th>
              <th>Started</th>
              <th>Duration</th>
              <th>Started by</th>
            </tr>
          </thead>
          <tbody>
            {runs.map((r) => (
              <tr key={r.id} className="clickable" onClick={() => nav(`/runs/${r.id}`)}>
                <td>
                  {r.workflow} <span className="hint">v{r.version}</span>
                </td>
                <td>
                  <Badge value={r.status} />
                </td>
                <td>{r.environment}</td>
                <td>{fmtTime(r.started_at)}</td>
                <td>{r.ended_at ? duration(r.started_at, r.ended_at) : ""}</td>
                <td className="hint">{r.started_by}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {runs.length >= 50 && (
        <div className="toolbar" style={{ marginTop: 10 }}>
          <button
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                const last = runs[runs.length - 1];
                if (!last) return;
                const q = new URLSearchParams(query);
                q.set("before", last.started_at);
                const r = await get<{ runs: RunSummary[] }>(`/v1/runs?${q}`);
                setMore([...more, ...r.runs]);
              })
            }
          >
            Load older runs
          </button>
        </div>
      )}
    </>
  );
}
