import { useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { ApiError, get, post, put } from "../api";
import { EmptyState, ErrorBox, Field, Item, Items, Modal, PageHeader, Row, Skeleton, fmtTime, useAction, useLiveRefresh, useLoad } from "../ui";
import { feeInput, feeKobo } from "../lib/orders";
import { formatPrice } from "../lib/storefront";
import { bankOf, type Bank } from "../lib/receiveMoney";
import type { RidersView } from "../lib/riders";
import {
  PAYOUT_STATUS,
  WEEKDAYS,
  bankProblem,
  currentDelivery,
  deliveryStatus,
  deliverySteps,
  deliverySub,
  isHeld,
  offerOutcome,
  payLineSub,
  remitProblem,
  settingsForm,
  settingsProblem,
  settingsRequest,
  type Delivery,
  type DispatchSettings,
  type OrderDeliveries,
  type Owed,
  type PayLine,
  type RidersPay,
  type SettingsForm,
} from "../lib/dispatch";
import { StorefrontTabs } from "./Storefront";

// Deliveries by the business's riders (dispatch D2 to D5; docs/dispatch.md):
// an order's delivery on its page, the day's deliveries with how they are
// offered, and Riders > Pay with the cash riders owe.

/** Whether an error says riders are not available on this Taskiem. */
const dispatchOff = (e: unknown) => e instanceof ApiError && e.status === 409;

/** An order's delivery on its page: who has it and how far it got, with
 * Offer and Take back. Nothing for pickup orders, or with riders off. */
export function OrderDelivery({ reference, delivery, onChanged }: { reference: string; delivery: boolean; onChanged: () => void }) {
  const ref = encodeURIComponent(reference);
  const view = useLoad(() => get<OrderDeliveries>(`/v1/storefront/orders/${ref}/deliveries`), [ref]);
  useLiveRefresh(view.reload);
  const [choosing, setChoosing] = useState(false);
  const [said, setSaid] = useState("");
  const act = useAction();
  if (!delivery || dispatchOff(view.error)) return null;
  const v = view.data;
  const cur = v ? currentDelivery(v.deliveries) : undefined;
  const earlier = v ? v.deliveries.filter((d) => d !== cur) : [];
  const done = () => {
    view.reload();
    onChanged();
  };
  const offer = (riderId?: string) =>
    act.run(async () => {
      const out = await post<{ status: string; rider?: string }>(`/v1/storefront/orders/${ref}/offer`, riderId ? { rider_id: riderId } : {});
      setSaid(offerOutcome(out));
      setChoosing(false);
      done();
    }, false);
  return (
    <section className="card" aria-labelledby="order-delivery" data-testid="order-delivery">
      <h2 id="order-delivery">Delivery</h2>
      <ErrorBox error={view.error ?? act.error} />
      {!v && !view.error && <Skeleton rows={2} label="Loading the delivery…" />}
      {said && (
        <div className="notice" role="status">
          {said}
        </div>
      )}
      {v && cur && (
        <>
          <dl className="kv">
            <dt>Rider</dt>
            <dd>
              {cur.rider}
              {cur.rider_phone && (
                <>
                  {" · "}
                  <a href={`tel:${cur.rider_phone}`}>{cur.rider_phone}</a>
                </>
              )}
            </dd>
            <dt>Status</dt>
            <dd>
              <span className={`badge ${deliveryStatus(cur.status).tone}`}>{deliveryStatus(cur.status).label}</span>
              {cur.late && <span className="hint"> {cur.late === "pickup" ? "Not picked up in time" : "Not at the door in time"}</span>}
            </dd>
            {cur.fee_kobo > 0 && (
              <>
                <dt>Rider's fee</dt>
                <dd>{formatPrice(cur.fee_kobo)}</dd>
              </>
            )}
            {cur.rating ? (
              <>
                <dt>Customer's rating</dt>
                <dd>{cur.rating} of 5</dd>
              </>
            ) : null}
          </dl>
          <ol className="sf-lines" aria-label="How far it got">
            {deliverySteps(cur).map((s) => (
              <li key={s.label}>
                {s.label} <span className="hint">{s.at}</span>
              </li>
            ))}
          </ol>
          {(cur.proof_url || cur.location) && (
            <p>
              {cur.proof_url && (
                <a href={cur.proof_url} target="_blank" rel="noreferrer">
                  See the photo from the door
                </a>
              )}
              {cur.proof_url && cur.location && " · "}
              {cur.location && (
                <a href={cur.location.startsWith("https://") ? cur.location : `https://maps.google.com/?q=${encodeURIComponent(cur.location)}`} target="_blank" rel="noreferrer">
                  Where the rider was
                </a>
              )}
            </p>
          )}
        </>
      )}
      {v && !cur && <p className="hint">{v.not_ready ? `Not ready for a rider: ${v.not_ready}.` : "No rider has it yet."}</p>}
      {v && (
        <div className="toolbar">
          {!v.not_ready && (!cur || cur.status === "offered") && (
            <>
              <button type="button" className="primary" disabled={act.busy} onClick={() => void offer()}>
                {cur ? "Offer to the next free rider" : "Offer to a free rider"}
              </button>
              <button type="button" disabled={act.busy} onClick={() => setChoosing(true)}>
                Choose the rider
              </button>
            </>
          )}
          {cur && (isHeld(cur) || cur.status === "offered") && (
            <button
              type="button"
              className="danger"
              disabled={act.busy}
              onClick={() =>
                confirm(`Take ${reference} back from ${cur.rider}? They are told, and you can offer it again.`) &&
                void act.run(async () => (await post(`/v1/storefront/orders/${ref}/release`), setSaid(""), done()), "Taken back")
              }
            >
              Take it back
            </button>
          )}
        </div>
      )}
      {earlier.length > 0 && (
        <details>
          <summary>Earlier offers ({earlier.length})</summary>
          <Items label="Earlier offers">
            {earlier.map((d) => (
              <Row key={d.id} letter={d.rider} title={d.rider} sub={<span className="sub">{fmtTime(d.offered_at)}</span>} side={<span className={`badge ${deliveryStatus(d.status).tone}`}>{deliveryStatus(d.status).label}</span>} />
            ))}
          </Items>
        </details>
      )}
      {choosing && <ChooseRider onClose={() => setChoosing(false)} onChoose={(id) => void offer(id)} busy={act.busy} />}
    </section>
  );
}

/** The owner's choice of rider for an order: the current riders, free first. */
function ChooseRider({ onClose, onChoose, busy }: { onClose: () => void; onChoose: (id: string) => void; busy: boolean }) {
  const view = useLoad(() => get<RidersView>("/v1/riders"), []);
  const riders = useMemo(() => {
    const order = { free: 0, busy: 1, off: 2 };
    return (view.data?.riders ?? []).filter((r) => r.active).sort((a, b) => order[a.presence.state] - order[b.presence.state]);
  }, [view.data]);
  return (
    <Modal title="Choose the rider" onClose={onClose}>
      <ErrorBox error={view.error} />
      {!view.data && !view.error && <Skeleton rows={3} label="Loading your riders…" />}
      {view.data && riders.length === 0 && (
        <EmptyState icon="members">
          No riders yet. <Link to="/storefront/riders">Add your riders</Link>.
        </EmptyState>
      )}
      {riders.length > 0 && (
        <Items label="Your riders">
          {riders.map((r) => (
            <Row key={r.id} letter={r.name} title={r.name} sub={<span className="sub">{r.zones.join(", ") || "All areas"}</span>}>
              <button type="button" className="small" disabled={busy} onClick={() => onChoose(r.id)}>
                Offer<span className="sr-only"> to {r.name}</span>
              </button>
            </Row>
          ))}
        </Items>
      )}
    </Modal>
  );
}

/** Today's deliveries (or another day's, or those under way), and how
 * orders are offered to riders. */
export function StorefrontDeliveries() {
  const [params, setParams] = useSearchParams();
  const open = params.get("open") === "true";
  const day = params.get("day") ?? "";
  const path = open ? "/v1/deliveries?open=true" : `/v1/deliveries${day ? `?day=${day}` : ""}`;
  const view = useLoad(() => get<{ deliveries: Delivery[] }>(path), [path]);
  useLiveRefresh(view.reload);
  const [settings, setSettings] = useState(false);
  const on = useLoad(() => get<{ on: boolean; workflow_id: string | null }>("/v1/dispatch/workflow"), []);
  const turnOn = useAction();
  // A workflow the business made itself is replaced only once the owner says so.
  const turnOnDeliveries = async () => {
    try {
      await post("/v1/dispatch/workflow");
    } catch (e) {
      if (!(e instanceof ApiError) || e.body.code !== "replaces_workflow") throw e;
      if (!confirm(`Your order form starts your own workflow "${String(e.body.workflow ?? "")}". Turning on deliveries takes it out of use. Go ahead?`)) return;
      await post("/v1/dispatch/workflow", { replace: true });
    }
    on.reload();
  };
  const off = dispatchOff(view.error);
  const list = view.data?.deliveries ?? [];
  return (
    <>
      <PageHeader
        title="Deliveries"
        description="Your riders' deliveries: who has each order, how far it got, and how it was paid at the door."
        actions={
          !off && (
            <button type="button" onClick={() => setSettings(true)}>
              How orders are offered
            </button>
          )
        }
      />
      <StorefrontTabs />
      {off ? <EmptyState icon="members">Deliveries need text messages, which are not set up on this Taskiem yet.</EmptyState> : <ErrorBox error={view.error} />}
      {!off && on.data && !on.data.on && (
        <section className="card" aria-labelledby="deliveries-on">
          <h2 id="deliveries-on" style={{ marginTop: 0 }}>
            Offer orders to your riders by themselves
          </h2>
          <p className="hint">
            Each order for delivery you confirm then goes to your riders in turn, and the rider's Delivered completes it. Your order form stays as it is; without
            this, you offer each order from its page.
          </p>
          <ErrorBox error={turnOn.error} />
          <button type="button" className="primary" disabled={turnOn.busy} onClick={() => void turnOn.run(turnOnDeliveries, "Deliveries are on")}>
            Turn on deliveries
          </button>
        </section>
      )}
      {!off && (
        <div className="toolbar">
          <button type="button" className={open ? "small primary" : "small"} aria-pressed={open} onClick={() => setParams(open ? {} : { open: "true" })}>
            Under way now
          </button>
          <Field label="Day">
            <input type="date" value={day} disabled={open} onChange={(e) => setParams(e.target.value ? { day: e.target.value } : {})} />
          </Field>
        </div>
      )}
      {!view.data && !view.error && <Skeleton rows={4} label="Loading deliveries…" />}
      {view.data && list.length === 0 && <EmptyState icon="orders">{open ? "No delivery is under way." : "No deliveries that day."}</EmptyState>}
      {list.length > 0 && (
        <Items label="Deliveries">
          {list.map((d) => (
            <Item
              key={d.id}
              to={`/storefront/orders/${encodeURIComponent(d.reference)}`}
              icon="orders"
              title={d.reference}
              sub={deliverySub(d)}
              side={<span className={`badge ${d.late ? "danger" : deliveryStatus(d.status).tone}`}>{deliveryStatus(d.status).label}</span>}
              sideSub={fmtTime(d.delivered_at ?? d.offered_at)}
              testid="delivery"
            />
          ))}
        </Items>
      )}
      {settings && <DispatchSettingsModal onClose={() => setSettings(false)} />}
    </>
  );
}

/** How orders are offered to riders, how long they have, and the marks for cash owed and ratings. */
function DispatchSettingsModal({ onClose }: { onClose: () => void }) {
  const view = useLoad(() => get<DispatchSettings>("/v1/dispatch/settings"), []);
  return (
    <Modal title="How orders are offered" onClose={onClose}>
      <ErrorBox error={view.error} />
      {!view.data && !view.error && <Skeleton rows={4} label="Loading…" />}
      {view.data && <SettingsFields start={settingsForm(view.data)} onClose={onClose} />}
    </Modal>
  );
}

function SettingsFields({ start, onClose }: { start: SettingsForm; onClose: () => void }) {
  const [f, setF] = useState(start);
  const set = (p: Partial<SettingsForm>) => setF((x) => ({ ...x, ...p }));
  const act = useAction();
  const problem = settingsProblem(f);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => (await put("/v1/dispatch/settings", settingsRequest(f)), onClose()), "Saved");
      }}
    >
      <fieldset className="sf-choices">
        <legend>When an order is ready for a rider</legend>
        <label className="sf-radio">
          <input type="radio" name="mode" checked={f.mode === "rotation"} onChange={() => set({ mode: "rotation" })} />
          Offer it to free riders in turn, those covering the area first
        </label>
        <label className="sf-radio">
          <input type="radio" name="mode" checked={f.mode === "ask"} onChange={() => set({ mode: "ask" })} />
          Ask me in chat who takes it
        </label>
      </fieldset>
      <Field label="Minutes a rider has to accept">
        <input value={f.offer} inputMode="numeric" onChange={(e) => set({ offer: e.target.value })} />
      </Field>
      <Field label="Minutes to pick up" hint="After this, you are told the rider has not picked it up.">
        <input value={f.pickup} inputMode="numeric" onChange={(e) => set({ pickup: e.target.value })} />
      </Field>
      <Field label="Minutes to get to the customer" hint="After this, you are told the rider has not arrived.">
        <input value={f.arrive} inputMode="numeric" onChange={(e) => set({ arrive: e.target.value })} />
      </Field>
      <label className="sf-radio">
        <input type="checkbox" checked={f.proof} onChange={(e) => set({ proof: e.target.checked })} />
        Ask riders for a photo at the door
      </label>
      <Field label="Cash a rider may owe (₦)" hint="Above this, it shows under Money leaks.">
        <input value={f.oweLimit} inputMode="decimal" onChange={(e) => set({ oweLimit: e.target.value })} />
      </Field>
      <Field label="Tell me when a rider's rating falls below (1 to 5)">
        <input value={f.ratingMark} inputMode="decimal" onChange={(e) => set({ ratingMark: e.target.value })} />
      </Field>
      {problem && <p className="error-text">{problem}</p>}
      <ErrorBox error={act.error} />
      <div className="toolbar">
        <button type="submit" className="primary" disabled={act.busy || !!problem}>
          Save
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </form>
  );
}

/** Riders > Pay: the cash riders owe, what each is due, their bank
 * accounts, the weekly payout and paying now. */
export function RidersPayPage() {
  const owed = useLoad(() => get<{ owed: Owed[] }>("/v1/riders/owed"), []);
  const pay = useLoad(() => get<RidersPay>("/v1/riders/pay"), []);
  const [remitting, setRemitting] = useState<Owed | null>(null);
  const [banking, setBanking] = useState<PayLine | null>(null);
  const [setup, setSetup] = useState(false);
  const act = useAction();
  const reload = () => {
    owed.reload();
    pay.reload();
  };
  const off = dispatchOff(owed.error);
  const p = pay.data;
  const due = p ? p.lines.reduce((t, l) => t + l.net_kobo, 0) : 0;
  return (
    <>
      <p className="crumbs">
        <Link to="/storefront/riders" aria-label="Back to Riders">
          Riders
        </Link>
      </p>
      <PageHeader title="Pay riders" description="The cash riders owe you from the door, what you owe them in fees, and their weekly pay." />
      {off ? <EmptyState icon="members">Riders need text messages, which are not set up on this Taskiem yet.</EmptyState> : <ErrorBox error={owed.error ?? pay.error ?? act.error} />}
      {p && (
        <section className="hero" aria-label="Riders' pay">
          <div className="hero-top">
            <span className="hero-label">Due to riders now</span>
          </div>
          <div className="hero-amount">{formatPrice(due)}</div>
          <p className="hero-sub">{p.workflow_id ? "Paid weekly, after you approve" : "Weekly pay is not set up yet"}</p>
          <div className="toolbar">
            <button
              type="button"
              className="primary"
              disabled={act.busy || !p.workflow_id || due === 0}
              onClick={() =>
                confirm(`Pay your riders ${formatPrice(due)} now? You approve it first in Approvals or in chat.`) &&
                void act.run(async () => (await post("/v1/riders/pay"), reload()), "Started: approve it in Approvals or in chat")
              }
            >
              Pay riders now
            </button>
            <button type="button" onClick={() => setSetup(true)}>
              {p.workflow_id ? "Change the weekly pay" : "Set up weekly pay"}
            </button>
          </div>
        </section>
      )}
      {owed.data && (
        <section className="card" aria-labelledby="riders-owed">
          <h2 id="riders-owed">Cash riders owe</h2>
          <p className="hint">Cash customers paid at the door. A transfer from the rider naming the order counts by itself; record cash handed to you here.</p>
          {owed.data.owed.length === 0 ? (
            <EmptyState icon="money">No rider owes you cash.</EmptyState>
          ) : (
            <Items label="Cash riders owe">
              {owed.data.owed.map((o) => (
                <Row
                  key={o.rider_id}
                  letter={o.rider}
                  title={o.rider}
                  sub={<span className="sub">{`${o.deliveries} ${o.deliveries === 1 ? "delivery" : "deliveries"}${o.since ? ` since ${fmtTime(o.since)}` : ""}`}</span>}
                  side={<span className="badge warn">{formatPrice(o.owed_kobo)}</span>}
                  testid="rider-owed"
                >
                  <button type="button" className="small" onClick={() => setRemitting(o)}>
                    Record cash handed in<span className="sr-only"> by {o.rider}</span>
                  </button>
                </Row>
              ))}
            </Items>
          )}
        </section>
      )}
      {p && (
        <section className="card" aria-labelledby="riders-due">
          <h2 id="riders-due">Due to each rider</h2>
          {p.lines.length === 0 ? (
            <EmptyState icon="money">Nothing is due: riders' fees show here once they deliver.</EmptyState>
          ) : (
            <Items label="Due to each rider">
              {p.lines.map((l) => (
                <Row key={l.rider_id} letter={l.rider} title={l.rider} sub={<span className="sub">{payLineSub(l)}</span>} side={<span className="sf-price">{formatPrice(l.net_kobo)}</span>} testid="rider-due">
                  <button type="button" className="small" onClick={() => setBanking(l)}>
                    {l.bank ? "Change bank account" : "Add bank account"}
                    <span className="sr-only"> for {l.rider}</span>
                  </button>
                </Row>
              ))}
            </Items>
          )}
          {p.payouts.length > 0 && (
            <details>
              <summary>Recent payouts</summary>
              <Items label="Recent payouts">
                {p.payouts.map((x) => (
                  <Row
                    key={x.payout_id}
                    letter={x.rider}
                    title={`${x.rider} · ${formatPrice(x.amount_kobo)}`}
                    sub={<span className="sub">{`${x.deliveries} ${x.deliveries === 1 ? "delivery" : "deliveries"}${x.owed_kobo ? `, ${formatPrice(x.owed_kobo)} cash taken off` : ""}`}</span>}
                    side={<span className={`badge ${PAYOUT_STATUS[x.status].tone}`}>{PAYOUT_STATUS[x.status].label}</span>}
                  />
                ))}
              </Items>
            </details>
          )}
        </section>
      )}
      {!owed.data && !owed.error && <Skeleton rows={4} label="Loading riders' pay…" />}
      {remitting && <RemitModal owed={remitting} onClose={() => setRemitting(null)} onDone={() => (setRemitting(null), reload())} />}
      {banking && <BankModal line={banking} onClose={() => setBanking(null)} onDone={() => (setBanking(null), reload())} />}
      {setup && <PaySetupModal onClose={() => setSetup(false)} onDone={() => (setSetup(false), reload())} />}
    </>
  );
}

function RemitModal({ owed, onClose, onDone }: { owed: Owed; onClose: () => void; onDone: () => void }) {
  const [amount, setAmount] = useState(feeInput(owed.owed_kobo));
  const act = useAction();
  const problem = remitProblem(amount, owed.owed_kobo);
  return (
    <Modal title={`Cash from ${owed.rider}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => (await post(`/v1/riders/${owed.rider_id}/remit`, { amount_kobo: feeKobo(amount), how: "cash" }), onDone()), "Recorded");
        }}
      >
        <p className="hint">They owe {formatPrice(owed.owed_kobo)}. The cash is added to your money log.</p>
        <Field label="Amount handed in (₦)">
          <input value={amount} inputMode="decimal" onChange={(e) => setAmount(e.target.value)} autoFocus />
        </Field>
        {problem && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !!problem}>
            Record it
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function BankModal({ line, onClose, onDone }: { line: PayLine; onClose: () => void; onDone: () => void }) {
  const banks = useLoad(() => get<{ banks: Bank[] }>("/v1/receive-money/banks"), []);
  const list = useMemo(() => (banks.data?.banks ?? []).map(bankOf).filter((b) => b.code), [banks.data]);
  const [code, setCode] = useState("");
  const [number, setNumber] = useState("");
  const act = useAction();
  const problem = bankProblem(code, number);
  return (
    <Modal title={`${line.rider}'s bank account`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const bank = list.find((b) => b.code === code);
          void act.run(
            async () => (await put(`/v1/riders/${line.rider_id}/bank`, { bank_code: code.trim(), bank_name: bank?.name ?? "", account_number: number }), onDone()),
            "Saved",
          );
        }}
      >
        <p className="hint">Their payouts go here. Their bank confirms the name before the first payout, and you see it when you approve.</p>
        {list.length > 0 ? (
          <Field label="Bank">
            <select value={code} onChange={(e) => setCode(e.target.value)}>
              <option value="">Choose their bank</option>
              {list.map((b) => (
                <option key={b.code} value={b.code}>
                  {b.name}
                </option>
              ))}
            </select>
          </Field>
        ) : (
          <Field label="Bank code" hint="The bank's code, such as 058 for GTBank.">
            <input value={code} inputMode="numeric" onChange={(e) => setCode(e.target.value)} />
          </Field>
        )}
        <Field label="Account number">
          <input value={number} inputMode="numeric" autoComplete="off" onChange={(e) => setNumber(e.target.value.replace(/\D/g, "").slice(0, 10))} />
        </Field>
        {number.length === 10 && problem && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !!problem}>
            Save
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

function PaySetupModal({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [day, setDay] = useState(5);
  const [time, setTime] = useState("17:00");
  const act = useAction();
  return (
    <Modal title="Weekly pay" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(async () => {
            const out = await put<{ waiting: boolean }>("/v1/riders/pay/setup", { pay_day: day, pay_time: time });
            onDone();
            return out;
          }, "Saved: riders are paid weekly after you approve");
        }}
      >
        <p className="hint">
          Each week, Taskiem adds up each rider's fees, takes off the cash they owe, and checks their bank account names. You approve the list, then each rider is
          paid once from your Taskiem wallet.
        </p>
        <Field label="Day">
          <select value={day} onChange={(e) => setDay(Number(e.target.value))}>
            {WEEKDAYS.map((d, i) => (
              <option key={d} value={i}>
                {d}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Time (Lagos)">
          <input type="time" value={time} onChange={(e) => setTime(e.target.value)} />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !/^\d\d:\d\d$/.test(time)}>
            Save
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
