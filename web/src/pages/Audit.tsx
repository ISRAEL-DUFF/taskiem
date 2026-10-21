import { useState } from "react";
import { get } from "../api";
import { EmptyState, ErrorBox, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import { useAuth } from "../auth";
import { detailWords, shortIds } from "../lib/audit";

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
  const { can } = useAuth();
  const [action, setAction] = useState("");
  const { data, error } = useLoad(() => get<{ entries: Entry[] }>(`/v1/audit?limit=200${action ? `&action=${encodeURIComponent(action)}` : ""}`), [action]);
  const [verdict, setVerdict] = useState<{ intact: boolean; first_broken_seq: number | null } | null>(null);
  const act = useAction();
  // Who did it, by name, for whoever may list the members.
  const members = useLoad(() => (can("member.manage") ? get<{ members: { user_id: string; name: string; email: string }[] }>("/v1/members") : Promise.resolve({ members: [] })), []);
  const names = new Map((members.data?.members ?? []).map((m) => [m.user_id, m.name || m.email]));
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
        <table className="audit">
          <thead>
            <tr>
              <th className="num">#</th>
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
                <td className="num hint">{e.seq}</td>
                <td className="nowrap">{fmtTime(e.at)}</td>
                <td className="nowrap" title={e.actor_id}>
                  {names.get(e.actor_id) ?? (
                    <>
                      <span className="hint">{e.actor_type}</span> <code>{shortIds(e.actor_id)}</code>
                    </>
                  )}
                </td>
                <td>
                  <code>{e.action}</code>
                </td>
                <td title={e.target}>
                  <code>{shortIds(e.target)}</code>
                </td>
                <td className="audit-detail" title={JSON.stringify(e.detail)}>
                  {detailWords(e.detail)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
