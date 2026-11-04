import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, put } from "../api";
import { useAuth } from "../auth";
import { AREA_NAMES, neededBy, offWords, type AreaKey, type AreaRow } from "../lib/areas";
import { toast } from "../toast";
import { ErrorBox, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";

// What your business uses (docs/spec/business-areas.md): the parts of
// Taskiem a business turns on. Off hides a part and the server refuses it;
// nothing is deleted. Anyone in the business can see the list; owners
// change it.

const ICONS: Record<AreaKey, string> = {
  storefront: "storefront",
  orders: "orders",
  stock: "tables",
  money: "money",
  links: "connections",
  schools: "schools",
};

/** A workflow of the business's that uses a part being turned off. */
interface UsedBy {
  workflow: string;
  step: string;
  environment: string;
}

export function Areas() {
  const { me, refresh } = useAuth();
  const owner = !!me?.roles.includes("owner");
  const list = useLoad(() => get<{ areas: AreaRow[] }>("/v1/settings/areas"), []);
  const act = useAction();
  const [asking, setAsking] = useState<{ row: AreaRow; affected?: UsedBy[] } | null>(null);
  const rows = list.data?.areas;

  async function send(change: Partial<Record<AreaKey, boolean>>, confirm: boolean) {
    const before = new Set((rows ?? []).filter((r) => r.enabled).map((r) => r.area));
    const out = await put<{ areas: AreaRow[] }>("/v1/settings/areas", { areas: change, confirm });
    list.reload();
    await refresh();
    // What else came on with it, because it needs it.
    const asked = Object.keys(change);
    const also = out.areas.filter((r) => r.enabled && !before.has(r.area) && !asked.includes(r.area)).map((r) => r.name);
    if (also.length > 0) toast(`${also.join(" and ")} turned on too, because ${asked.map((a) => AREA_NAMES[a as AreaKey]).join(" and ")} needs it.`);
    else toast("Saved");
  }

  return (
    <>
      <PageHeader
        title="What your business uses"
        description="Turn on only the parts you need. A part that is off is hidden, and nothing in it is deleted: turn it on again and it is all back."
      />
      <ErrorBox error={list.error} />
      {!rows && !list.error && <Skeleton rows={5} label="Loading what your business uses…" />}
      {rows && (
        <Items label="Parts of Taskiem">
          {rows.map((r) => {
            const needs = neededBy(rows, r.area);
            const lock = needs.length > 0 ? `${needs.map((n) => n.name).join(" and ")} needs it.` : "";
            return (
              <Row
                key={r.area}
                icon={ICONS[r.area]}
                title={r.name}
                tone={r.enabled ? undefined : "muted"}
                testid={`area-${r.area}`}
                attrs={{ "data-on": String(r.enabled) }}
                side={<span className={r.enabled ? "badge sf-status ok" : "badge sf-status muted"}>{r.enabled ? "On" : "Off"}</span>}
                sub={
                  <span className="sub">
                    {r.covers}
                    {r.depends_on.length > 0 && ` Needs ${r.depends_on.map((d) => AREA_NAMES[d]).join(" and ")}.`}
                  </span>
                }
              >
                {r.area === "schools" ? (
                  <Link className="button small" to="/settings">
                    {owner ? "Set up Schools" : "See Schools"}
                  </Link>
                ) : owner ? (
                  <>
                    <label className="switch-line">
                      <input
                        type="checkbox"
                        checked={r.enabled}
                        disabled={act.busy || (r.enabled && needs.length > 0)}
                        aria-label={r.name}
                        aria-describedby={lock ? `${r.area}-locked` : undefined}
                        onChange={(e) => (e.target.checked ? void act.run(() => send({ [r.area]: true }, false), false) : setAsking({ row: r }))}
                      />
                      <span>{r.enabled ? "On" : "Off"}</span>
                    </label>
                    {lock && (
                      <span className="hint" id={`${r.area}-locked`}>
                        {lock}
                      </span>
                    )}
                  </>
                ) : null}
              </Row>
            );
          })}
        </Items>
      )}
      {!owner && rows && <p className="hint">Only an owner turns parts on or off.</p>}
      <ErrorBox error={act.error} />
      {asking && (
        <TurnOff
          row={asking.row}
          affected={asking.affected}
          onClose={() => setAsking(null)}
          onTurnOff={async (confirm) => {
            try {
              await send({ [asking.row.area]: false }, confirm);
              setAsking(null);
              return undefined;
            } catch (e) {
              // Workflows use it: show them, and ask again.
              if (e instanceof ApiError && e.status === 409 && Array.isArray(e.body.affected)) {
                setAsking({ row: asking.row, affected: e.body.affected as UsedBy[] });
                return undefined;
              }
              return e;
            }
          }}
        />
      )}
    </>
  );
}

function TurnOff({ row, affected, onClose, onTurnOff }: { row: AreaRow; affected?: UsedBy[]; onClose: () => void; onTurnOff: (confirm: boolean) => Promise<unknown> }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  return (
    <Modal title={`Turn off ${row.name}?`} onClose={onClose}>
      <p>{offWords(row.area)}</p>
      {affected && affected.length > 0 && (
        <div className="warning" role="note">
          <p>These workflows of yours use it, and will stop at that step:</p>
          <ul>
            {affected.map((u, i) => (
              <li key={i}>
                {u.workflow} ({u.step})
              </li>
            ))}
          </ul>
        </div>
      )}
      <ErrorBox error={error} />
      <div className="row-actions">
        <button type="button" onClick={onClose}>
          Keep it on
        </button>
        <button
          type="button"
          className="danger"
          disabled={busy}
          onClick={() => {
            setBusy(true);
            setError(undefined);
            void onTurnOff(!!affected).then((e) => (setError(e), setBusy(false)));
          }}
        >
          {affected ? "Turn off anyway" : "Turn off"}
        </button>
      </div>
    </Modal>
  );
}
