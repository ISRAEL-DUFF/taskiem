// Explore's Featured row (docs/spec/mobile-app-explore-and-offline.md §2.2):
// Taskiem's picks, each until a date, ended early when need be. The row is
// filled to six by businesses Trusted on Taskiem. A business that stops
// being findable, or whose page is suspended, leaves the row at once. Every
// change asks for the operator's passkey.

import { useState } from "react";
import { Badge, ErrorBox, fmtTime, useAction, useLoad } from "../ui";
import { opsGet, withStepUp } from "./client";

type Pick = {
  id: string;
  business: string;
  name: string;
  starts_at: string;
  until: string;
  note: string;
  created_by: string;
  created_at: string;
  ended_at: string | null;
  ended_by: string | null;
  shown: boolean;
};

const inAWeek = () => new Date(Date.now() + 7 * 864e5).toISOString().slice(0, 10);

export function Featured() {
  const { data, error, reload } = useLoad(() => opsGet<{ picks: Pick[] }>("/explore/featured"), []);
  const [f, setF] = useState({ business: "", until: inAWeek(), note: "" });
  const act = useAction();
  const add = () =>
    act.run(async () => {
      const business = f.business.trim();
      // The end of the chosen day, in Lagos.
      const until = new Date(`${f.until}T23:59:00+01:00`).toISOString();
      await withStepUp("ops.explore.feature", business, "/explore/featured", { business, until, note: f.note.trim() });
      setF({ business: "", until: inAWeek(), note: "" });
      reload();
    }, "Business featured");
  const end = (p: Pick) =>
    act.run(async () => {
      if (!confirm(`End the pick of ${p.name} now?`)) return;
      await withStepUp("ops.explore.unfeature", p.id, `/explore/featured/${p.id}/end`, {});
      reload();
    }, "Pick ended");
  const live = (p: Pick) => !p.ended_at && new Date(p.until) > new Date();
  return (
    <>
      <h1>Featured in Explore</h1>
      <p className="hint">
        Businesses shown first in Explore, at most six. Until there are six picks, businesses Trusted on Taskiem fill the row, in an order that changes each day. Only a
        business that chose to be found can be picked; one whose page is suspended leaves the row at once.
      </p>
      <section className="card" aria-labelledby="feature-one">
        <h2 id="feature-one">Feature a business</h2>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void add();
          }}
        >
          <label>
            Business (its tenant id, from Tenants)
            <input required value={f.business} onChange={(e) => setF({ ...f, business: e.target.value })} placeholder="0193…" />
          </label>
          <label>
            Until
            <input type="date" required value={f.until} onChange={(e) => setF({ ...f, until: e.target.value })} />
          </label>
          <label>
            Note (for the audit)
            <input value={f.note} maxLength={500} onChange={(e) => setF({ ...f, note: e.target.value })} placeholder="Why this business" />
          </label>
          <button className="primary" disabled={act.busy || !f.business.trim()}>
            Feature
          </button>
        </form>
      </section>
      <ErrorBox error={error ?? act.error} />
      {data && data.picks.length === 0 && <div className="empty">No business has been featured yet.</div>}
      {data && data.picks.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Business</th>
              <th>In the row</th>
              <th>Until</th>
              <th>Note</th>
              <th>
                <span className="sr-only">Actions</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {data.picks.map((p) => (
              <tr key={p.id}>
                <td>
                  {p.name}
                  <div className="hint">
                    by {p.created_by} · {fmtTime(p.created_at)}
                    {p.ended_at && ` · ended ${fmtTime(p.ended_at)} by ${p.ended_by ?? ""}`}
                  </div>
                </td>
                <td>
                  <Badge value={p.shown ? "shown" : live(p) ? "hidden" : "ended"} />
                </td>
                <td>{fmtTime(p.until)}</td>
                <td>{p.note || "—"}</td>
                <td>
                  {live(p) && (
                    <button className="danger" disabled={act.busy} onClick={() => void end(p)}>
                      End now
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}
