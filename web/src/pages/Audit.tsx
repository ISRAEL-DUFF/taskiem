import { useState } from "react";
import { get } from "../api";
import { ErrorBox, fmtTime, useAction, useLoad } from "../ui";

interface Entry {
  seq: number;
  actor_type: string;
  actor_id: string;
  action: string;
  target: string;
  detail: unknown;
  at: string;
}

export function Audit() {
  const [action, setAction] = useState("");
  const { data, error } = useLoad(() => get<{ entries: Entry[] }>(`/v1/audit?limit=200${action ? `&action=${encodeURIComponent(action)}` : ""}`), [action]);
  const [verdict, setVerdict] = useState<{ intact: boolean; first_broken_seq: number | null } | null>(null);
  const act = useAction();
  return (
    <>
      <div className="toolbar">
        <h1 className="grow" style={{ margin: 0 }}>
          Audit log
        </h1>
        <input style={{ width: 200 }} placeholder="Filter by action" value={action} onChange={(e) => setAction(e.target.value)} />
        <button onClick={() => void act.run(async () => setVerdict(await get("/v1/audit/verify")))} disabled={act.busy}>
          Verify chain
        </button>
        <a className="button" href="/v1/audit/export" download>
          Export
        </a>
        <a className="button" href="/v1/audit/anchors" download="taskiem-anchors.json">
          Anchors
        </a>
      </div>
      <ErrorBox error={error ?? act.error} />
      {verdict && (
        <div className={verdict.intact ? "notice" : "error"} data-testid="audit-verdict">
          {verdict.intact ? "The hash chain is intact." : `The chain is broken at entry ${verdict.first_broken_seq}.`} Verify an export offline with <code>taskiem audit verify FILE</code>, and against the daily signed anchors with{" "}
          <code>taskiem audit verify --anchors taskiem-anchors.json --key KEY FILE</code> (the key is in the anchors file).
        </div>
      )}
      {data && (
        <table>
          <thead>
            <tr>
              <th>#</th>
              <th>When</th>
              <th>Who</th>
              <th>Action</th>
              <th>Target</th>
              <th>Detail</th>
            </tr>
          </thead>
          <tbody>
            {data.entries.map((e) => (
              <tr key={e.seq}>
                <td>{e.seq}</td>
                <td>{fmtTime(e.at)}</td>
                <td>
                  <span className="hint">{e.actor_type}</span> {e.actor_id}
                </td>
                <td>
                  <code>{e.action}</code>
                </td>
                <td style={{ wordBreak: "break-all" }}>{e.target}</td>
                <td>
                  <code style={{ fontSize: 11 }}>{JSON.stringify(e.detail)}</code>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
