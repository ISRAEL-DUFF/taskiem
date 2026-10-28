import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, patch, post } from "../api";
import { EmptyState, ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { zoneLine } from "../lib/orders";
import {
  RIDER_LIMITS,
  emptyRider,
  riderForm,
  riderProblem,
  riderRequest,
  riderSub,
  shareRiderText,
  stateLine,
  stateTone,
  type Rider,
  type RiderForm,
  type RidersView,
} from "../lib/riders";
import { Copy } from "./Forms";
import { StorefrontTabs } from "./Storefront";

/** The business's own riders (dispatch D1): who delivers its orders, the
 * areas they cover, where Taskiem reaches them and whether they are free. */
export function StorefrontRiders() {
  const [retired, setRetired] = useState(false);
  const view = useLoad(() => get<RidersView>(`/v1/riders${retired ? "?retired=true" : ""}`), [retired]);
  const [editing, setEditing] = useState<Rider | "new" | null>(null);
  const act = useAction();
  const v = view.data;
  // Riders are reached by text message; a Taskiem without them has none.
  const off = view.error instanceof ApiError && view.error.status === 409;
  return (
    <>
      <PageHeader
        title="Riders"
        description="The people who deliver your orders. They get your deliveries on WhatsApp, and tell Taskiem when they are working."
        actions={
          v && (
            <>
              <Link className="button" to="/storefront/riders/pay">
                Pay riders
              </Link>
              <button type="button" className="primary" onClick={() => setEditing("new")}>
                Add a rider
              </button>
            </>
          )
        }
      />
      <StorefrontTabs />
      {off ? (
        <EmptyState icon="members">Riders need text messages, which are not set up on this Taskiem yet.</EmptyState>
      ) : (
        <ErrorBox error={view.error ?? act.error} />
      )}
      {!v && !view.error && <Skeleton rows={4} label="Loading your riders…" />}
      {v && (
        <>
          {v.tap_in_url && (
            <section className="card" aria-labelledby="riders-link">
              <h2 id="riders-link" style={{ marginTop: 0 }}>
                The link for riders
              </h2>
              <p className="hint">
                A rider you add gets this link by SMS. One tap opens WhatsApp with a message ready to send; after that, your deliveries reach them there. You can
                also share it yourself.
              </p>
              <div className="email-address">
                <code>{v.tap_in_url}</code>
                <Copy text={v.tap_in_url} label="Copy the link for riders" />
                <a className="button small" href={`https://wa.me/?text=${encodeURIComponent(shareRiderText(v.tap_in_url))}`} target="_blank" rel="noopener noreferrer">
                  Share on WhatsApp
                </a>
              </div>
            </section>
          )}
          <section className="card" aria-labelledby="riders-list">
            <div className="toolbar" style={{ marginBottom: 6 }}>
              <h2 id="riders-list" className="grow" style={{ margin: 0 }}>
                {retired ? "Retired riders" : "Your riders"}
              </h2>
              <button type="button" className="small" onClick={() => setRetired(!retired)}>
                {retired ? "Show current riders" : "Show retired riders"}
              </button>
            </div>
            {!retired && (
              <p className="hint">
                Riders say <strong>off</strong>, <strong>on</strong> or <strong>busy till 3pm</strong> to Taskiem on WhatsApp; it counts for every business they
                ride for, and you never see which.
              </p>
            )}
            {v.riders.length === 0 ? (
              <EmptyState icon="members">{retired ? "No retired riders." : "No riders yet. Add the people who deliver your orders."}</EmptyState>
            ) : (
              <Items label={retired ? "Retired riders" : "Your riders"}>
                {v.riders.map((r) => (
                  <Row
                    key={r.id}
                    letter={r.name}
                    title={r.name}
                    side={!retired && <span className={`badge ${stateTone(r)}`}>{stateLine(r)}</span>}
                    sub={
                      <span className="sub">
                        {r.phone} · {riderSub(r)}
                      </span>
                    }
                    testid="rider"
                  >
                    {!retired && (
                      <>
                        <button type="button" className="small" onClick={() => setEditing(r)}>
                          Change<span className="sr-only"> {r.name}</span>
                        </button>
                        {r.reached === "sms" && (
                          <button
                            type="button"
                            className="small"
                            disabled={act.busy}
                            onClick={() => void act.run(async () => (await post(`/v1/riders/${r.id}/invite`), view.reload()), `Link sent to ${r.name}`)}
                          >
                            Send the link again<span className="sr-only"> to {r.name}</span>
                          </button>
                        )}
                        <button
                          type="button"
                          className="small danger"
                          disabled={act.busy}
                          onClick={() =>
                            confirm(`Retire ${r.name}? They are offered no more deliveries, and keep the ones they made.`) &&
                            void act.run(async () => (await post(`/v1/riders/${r.id}/retire`), view.reload()), `${r.name} retired`)
                          }
                        >
                          Retire<span className="sr-only"> {r.name}</span>
                        </button>
                      </>
                    )}
                  </Row>
                ))}
              </Items>
            )}
          </section>
        </>
      )}
      {editing && v && (
        <RiderModal
          rider={editing === "new" ? null : editing}
          zones={v.zones.map((z) => z.name)}
          zoneLines={v.zones.map(zoneLine)}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            if (retired) setRetired(false);
            else view.reload();
          }}
        />
      )}
    </>
  );
}

/** Add a rider, or change one: their name, areas, fee and whether they are on leave. */
function RiderModal({
  rider,
  zones,
  zoneLines,
  onClose,
  onSaved,
}: {
  rider: Rider | null;
  zones: string[];
  zoneLines: string[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const adding = rider === null;
  const [f, setF] = useState<RiderForm>(() => (rider ? riderForm(rider) : emptyRider()));
  const [leave, setLeave] = useState(rider ? !rider.active : false);
  const [labels, setLabels] = useState(rider && zones.length === 0 ? rider.zones.join(", ") : "");
  const act = useAction();
  const set = (p: Partial<RiderForm>) => setF((x) => ({ ...x, ...p }));
  const form: RiderForm = zones.length > 0 ? f : { ...f, zones: labels.split(",").map((z) => z.trim()).filter(Boolean) };
  const problem = riderProblem(form, adding);
  const save = () =>
    act.run(
      async () => {
        if (adding) await post("/v1/riders", riderRequest(form, true));
        else await patch(`/v1/riders/${rider.id}`, { ...riderRequest(form, false), active: !leave });
        onSaved();
      },
      adding ? `${form.name.trim()} added; the link is on its way by SMS` : "Saved",
    );
  return (
    <Modal title={adding ? "Add a rider" : `Change ${rider.name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Field label="Name">
          <input value={f.name} maxLength={RIDER_LIMITS.name} onChange={(e) => set({ name: e.target.value })} autoFocus autoComplete="off" />
        </Field>
        {adding ? (
          <Field label="Mobile number" hint="They get the link to your deliveries by SMS on this number.">
            <input value={f.phone} type="tel" inputMode="tel" placeholder="0803 123 4567" onChange={(e) => set({ phone: e.target.value })} />
          </Field>
        ) : (
          <p className="hint">
            Mobile number: {rider.phone}. To use another number, retire {rider.name} and add the new one.
          </p>
        )}
        {zones.length > 0 ? (
          <fieldset className="sf-choices">
            <legend>Areas they cover</legend>
            {zones.map((z, i) => (
              <label key={z} className="sf-radio">
                <input
                  type="checkbox"
                  checked={f.zones.includes(z)}
                  onChange={(e) => set({ zones: e.target.checked ? [...f.zones, z] : f.zones.filter((x) => x !== z) })}
                />
                {zoneLines[i]}
              </label>
            ))}
          </fieldset>
        ) : (
          <Field
            label="Areas they cover"
            hint={
              <span className="sf-links">
                Separated by commas. List the areas you deliver to, with their fees, on the order form under <Link to="/storefront">Page</Link>.
              </span>
            }
          >
            <input value={labels} placeholder="Lekki, Ajah" onChange={(e) => setLabels(e.target.value)} />
          </Field>
        )}
        <Field label="Fee per delivery (₦)" hint="What you pay them for each delivery. Leave it empty if you pay them another way.">
          <input value={f.fee} inputMode="decimal" placeholder="1000" onChange={(e) => set({ fee: e.target.value })} />
        </Field>
        {!adding && (
          <label className="sf-radio">
            <input type="checkbox" checked={leave} onChange={(e) => setLeave(e.target.checked)} />
            On leave: keep them, but offer them nothing
          </label>
        )}
        {problem && f.name !== "" && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !!problem}>
            {adding ? "Add the rider" : "Save"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
