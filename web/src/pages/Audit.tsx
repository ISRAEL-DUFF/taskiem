import { useState } from "react";
import { get } from "../api";
import { EmptyState, ErrorBox, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";

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
      <PageHeader
        title="Audit log"
        description="Every change and decision in your organisation, in order, each entry chained to the one before so tampering shows."
        actions={
          <>
            <button onClick={() => void act.run(async () => setVerdict(await get("/v1/audit/verify")))} disabled={act.busy}>
              Verify chain
            </button>
            <a className="button" href="/v1/audit/export" download>
              Export
            </a>
            <a className="button" href="/v1/audit/anchors" download="taskiem-anchors.json">
              Anchors
            </a>
          </>
        }
      />
      <div className="filters" role="search" aria-label="Filter the audit log">
        <label className="search">
          <span className="sr-only">Filter by action</span>
          <Icon name="search" />
          <input type="search" placeholder="Filter by action, such as workflow.publish" value={action} onChange={(e) => setAction(e.target.value)} />
        </label>
      </div>
      <ErrorBox error={error ?? act.error} />
      {verdict && (
        <div className={verdict.intact ? "notice" : "error"} data-testid="audit-verdict">
          {verdict.intact ? "The hash chain is intact." : `The chain is broken at entry ${verdict.first_broken_seq}.`} Verify an export offline with <code>taskiem audit verify FILE</code>, and against the daily signed anchors with{" "}
          <code>taskiem audit verify --anchors taskiem-anchors.json --key KEY FILE</code> (the key is in the anchors file).
        </div>
      )}
      {!data && !error && <Skeleton rows={6} />}
      {data && data.entries.length === 0 && (
        <EmptyState icon="audit">
          {action ? `No entry has the action “${action}”.` : "Each sign-in, change, publish and decision is recorded here as it happens."}
        </EmptyState>
      )}
      {data && data.entries.length > 0 && (
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
