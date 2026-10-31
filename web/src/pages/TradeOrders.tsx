import { useState } from "react";
import { Link, useParams } from "react-router-dom";
import { get, post, put } from "../api";
import { useAuth } from "../auth";
import { naira, toKobo } from "../lib/invoices";
import { buyerSteps, linesTotal, sameName, sellerAsk, sellerLine, sellerSteps, tradeStatus, type TradeLine, type TradeOrder } from "../lib/tradeorders";
import { STATUS, type Purchase } from "../lib/purchases";
import type { LinksView } from "../lib/links";
import { quantity, type StockItem, type StockView } from "../lib/stock";
import { EmptyState, ErrorBox, Field, fmtTime, Item, Items, Modal, PageHeader, Skeleton, useAction, useLoad } from "../ui";

// Orders between linked businesses (business links L2;
// docs/spec/business-links.md §7). The seller's side: a linked buyer's
// purchase order as an order here, with Accept, Change, Decline and its
// delivery. The buyer's side: its purchase order with the seller's
// progress, Accept the change, Cancel, All came and Not all came.

function TradePill({ status }: { status: TradeOrder["status"] }) {
  const s = tradeStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

const tradePath = (id: string) => `/v1/trade-orders/${encodeURIComponent(id)}`;

/** The business's own items, when it keeps its stock on Taskiem; none
 * otherwise. */
function useOwnItems() {
  return useLoad(
    () =>
      get<StockView>("/v1/stock")
        .then((v) => (v.keeps_stock ? v.items : []))
        .catch((): StockItem[] => []),
    [],
  );
}

/** On the Orders page, where linked businesses' orders are among the
 * rest: the way to their own page (a change, what was delivered, an order
 * drafted for a business); nothing while there are none. */
export function TradeOrdersCard() {
  const list = useLoad(() => get<{ orders: TradeOrder[] }>("/v1/trade-orders").catch(() => ({ orders: [] })), []);
  if ((list.data?.orders.length ?? 0) === 0) return null;
  return (
    <p className="hint" data-testid="trade-open">
      Orders from businesses you are linked with are among these. <Link to="/trade-orders">Orders from linked businesses</Link> has what is theirs alone.
    </p>
  );
}

function TradeRow({ o }: { o: TradeOrder }) {
  return (
    <Item
      to={`/trade-orders/${encodeURIComponent(o.id)}`}
      icon="orders"
      tone={tradeStatus(o.status).tone}
      title={o.buyer_name}
      side={naira(o.total_kobo)}
      sub={`${o.reference} · ${o.lines.map((l) => `${quantity(l.quantity, l.unit)} ${l.item}`).join(", ")}`}
      sideSub={<TradePill status={o.status} />}
      testid="trade-order"
    />
  );
}

/** Every order linked businesses sent us: those needing us first. */
export function TradeOrdersPage() {
  const list = useLoad(() => get<{ orders: TradeOrder[] }>("/v1/trade-orders"), []);
  const [drafting, setDrafting] = useState(false);
  const orders = list.data?.orders;
  return (
    <>
      <p className="crumbs">
        <Link to="/storefront/orders" aria-label="Back to Orders">
          Orders
        </Link>
      </p>
      <PageHeader
        title="Orders from linked businesses"
        description="Purchase orders businesses you are linked with on Taskiem sent you. What you do here reaches their order at once."
        actions={
          <button type="button" className="primary" onClick={() => setDrafting(true)}>
            Draft an order for them
          </button>
        }
      />
      <ErrorBox error={list.error} />
      {drafting && (
        <DraftOrder
          onClose={() => setDrafting(false)}
          onDone={() => {
            setDrafting(false);
            list.reload();
          }}
        />
      )}
      {!orders ? (
        <Skeleton rows={3} label="Loading the orders…" />
      ) : orders.length === 0 ? (
        <EmptyState icon="orders" action={<Link className="button" to="/chat">Linked businesses</Link>}>
          No orders yet. When a business that buys from you orders on Taskiem, it arrives here.
        </EmptyState>
      ) : (
        <Items label="Orders from linked businesses">
          {orders.map((o) => (
            <TradeRow key={o.id} o={o} />
          ))}
        </Items>
      )}
    </>
  );
}

/** One order from a linked business: its total first, what it needs from
 * us, its lines, and the steps its status allows. */
export function TradeOrderPage() {
  const { id = "" } = useParams();
  const { data, error, reload } = useLoad(() => get<{ order: TradeOrder }>(tradePath(id)), [id]);
  const { can } = useAuth();
  const o = data?.order;
  return (
    <>
      <p className="crumbs">
        <Link to="/trade-orders" aria-label="Back to Orders from linked businesses">
          Orders from linked businesses
        </Link>
      </p>
      <PageHeader title={o ? `${o.reference} from ${o.buyer_name}` : "Order"} />
      <ErrorBox error={error} />
      {!o ? (
        !error && <Skeleton rows={3} label="Loading the order…" />
      ) : (
        <>
          <section className="hero" aria-label="The order" data-testid="trade-hero">
            <div className="hero-top">
              <span className="hero-label">Order total</span>
              <span className="hero-tag">{tradeStatus(o.status).label}</span>
            </div>
            <div className="hero-amount">{naira(o.total_kobo)}</div>
            <p className="hero-sub">From {o.buyer_name}</p>
            <dl className="hero-foot">
              <div>
                <dt>Ordered</dt>
                <dd>{fmtTime(o.created_at)}</dd>
              </div>
              <div>
                <dt>Invoiced</dt>
                <dd>{o.invoice_id ? "Yes" : o.invoice_at === "accepted" ? "When accepted" : "When received"}</dd>
              </div>
            </dl>
          </section>
          {sellerAsk(o) && (
            <div className="notice" role="status" data-testid="trade-ask">
              {sellerAsk(o)}
            </div>
          )}
          {o.note && <p className="hint">Their note: “{o.note}”</p>}
          <LinesCard lines={o.lines} short={o.short} buyer={o.buyer_name} />
          {can("storefront.manage") && o.status === "received" && <OwnItems o={o} onDone={reload} />}
          {o.change && (
            <section className="card" aria-labelledby="trade-change">
              <h2 id="trade-change">Your change</h2>
              <LinesTable lines={o.change} />
            </section>
          )}
          {(o.status === "accepted" || o.status === "ready" || o.status === "dispatched" || o.status === "delivered") && (
            <p>
              <Link to={`/storefront/orders/${encodeURIComponent(o.order_reference)}`}>
                {o.fulfilment === "delivery" ? "Its delivery and riders" : "It on the Orders page"}
              </Link>
              {o.fulfilment === "delivery" ? `: a rider takes it to where ${o.buyer_name} said.` : "."}
            </p>
          )}
          {o.invoice_id && (
            <p>
              <Link to={`/invoices/${o.invoice_id}`}>Its invoice</Link>, which is {o.buyer_name}'s bill on Taskiem.
            </p>
          )}
          {can("storefront.manage") && <SellerActions o={o} onDone={reload} />}
          {can("link.manage") && <TradeSettings o={o} onDone={reload} />}
        </>
      )}
    </>
  );
}

function LinesTable({ lines, short }: { lines: TradeLine[]; short?: number[] }) {
  const delivered = lines.some((l) => l.delivered !== undefined);
  return (
    <table>
      <thead>
        <tr>
          <th scope="col">Item</th>
          <th scope="col">Quantity</th>
          <th scope="col">Price each</th>
          {delivered && <th scope="col">Delivered</th>}
          {short && <th scope="col">They counted</th>}
        </tr>
      </thead>
      <tbody>
        {lines.map((l, i) => (
          <tr key={i}>
            <td>{l.item}</td>
            <td>{quantity(l.quantity, l.unit)}</td>
            <td className="sf-price">{naira(l.price_kobo)}</td>
            {delivered && <td>{l.delivered ?? 0}</td>}
            {short && <td>{short[i] ?? 0}</td>}
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function LinesCard({ lines, short, buyer }: { lines: TradeLine[]; short?: number[]; buyer: string }) {
  return (
    <section className="card" aria-labelledby="trade-lines">
      <h2 id="trade-lines">What {buyer} ordered</h2>
      <LinesTable lines={lines} short={short} />
      <p className="lines-total">
        <span>Total</span>
        <strong className="sf-price">{naira(linesTotal(lines))}</strong>
      </p>
    </section>
  );
}

type SellerStep = "accept" | "decline" | "change" | "ready" | "dispatched" | "delivered" | "cancel" | "keep" | "agree";

/** The seller's steps on an order, as its status allows. */
function SellerActions({ o, onDone }: { o: TradeOrder; onDone: () => void }) {
  const can = sellerSteps(o);
  const act = useAction();
  const [open, setOpen] = useState<"change" | "decline" | "delivered">();
  const [note, setNote] = useState("");
  // Each dialog's form is filled as it opens, from the order as it is then.
  const [lines, setLines] = useState<{ quantity: string; price: string }[]>([]);
  const [got, setGot] = useState<string[]>([]);
  const step = (s: SellerStep, body: object | undefined, done: string) =>
    act.run(async () => (await post(`${tradePath(o.id)}/${s}`, body), onDone()), done).then((ok) => ok && (setOpen(undefined), setNote("")));
  const close = () => (setOpen(undefined), act.clear());
  const whole = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : NaN);
  if (!Object.values(can).some(Boolean)) return null;
  return (
    <section className="card" aria-labelledby="trade-steps">
      <h2 id="trade-steps">Answer {o.buyer_name}</h2>
      <ErrorBox error={open ? undefined : act.error} />
      <div className="row-actions">
        {can.accept && (
          <button type="button" className="primary" disabled={act.busy} onClick={() => void step("accept", undefined, `${o.reference} accepted`)}>
            Accept
          </button>
        )}
        {can.change && (
          <button type="button" onClick={() => (setLines(o.lines.map((l) => ({ quantity: String(l.quantity), price: String(l.price_kobo / 100) }))), setOpen("change"))}>
            Change
          </button>
        )}
        {can.ready && (
          <button type="button" disabled={act.busy} onClick={() => void step("ready", undefined, "They are told it is ready")}>
            It is ready
          </button>
        )}
        {can.dispatched && (
          <button type="button" disabled={act.busy} onClick={() => void step("dispatched", undefined, "They are told it is on its way")}>
            Sent on its way
          </button>
        )}
        {can.delivered && (
          <button type="button" className="primary" onClick={() => (setGot(o.lines.map((l) => String(l.quantity))), setOpen("delivered"))}>
            Delivered
          </button>
        )}
        {can.keep && (
          <button type="button" disabled={act.busy} onClick={() => void step("keep", undefined, "Kept, and they are told")}>
            Keep the order
          </button>
        )}
        {can.agree && (
          <button type="button" className="primary" disabled={act.busy} onClick={() => void step("agree", undefined, "Agreed, and they are told")}>
            Agree their count
          </button>
        )}
        {can.cancel && (
          <button
            type="button"
            className="danger"
            disabled={act.busy}
            onClick={() => confirm(`Cancel ${o.reference} for ${o.buyer_name}?`) && void step("cancel", undefined, `${o.reference} cancelled`)}
          >
            Cancel the order
          </button>
        )}
        {can.decline && (
          <button type="button" className="danger" onClick={() => setOpen("decline")}>
            Decline
          </button>
        )}
      </div>
      {open === "change" && (
        <Modal title={`Change ${o.reference}`} onClose={close}>
          <p className="hint">What you can supply, at your prices. {o.buyer_name} accepts it, or cancels. Put 0 for what you cannot supply.</p>
          {o.lines.map((l, i) => (
            <fieldset key={i} className="sf-choices">
              <legend>{l.item}</legend>
              <Field label="How many">
                <input inputMode="numeric" value={lines[i]?.quantity ?? ""} onChange={(e) => setLines(lines.map((x, j) => (j === i ? { ...x, quantity: e.target.value } : x)))} />
              </Field>
              <Field label="Price each (₦)">
                <input inputMode="decimal" value={lines[i]?.price ?? ""} onChange={(e) => setLines(lines.map((x, j) => (j === i ? { ...x, price: e.target.value } : x)))} />
              </Field>
            </fieldset>
          ))}
          <Field label="A word for them (optional)">
            <input maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
          <ErrorBox error={act.error} />
          <button
            type="button"
            className="primary"
            disabled={act.busy}
            onClick={() =>
              void step(
                "change",
                { lines: o.lines.map((l, i) => ({ item: l.item, unit: l.unit, quantity: whole(lines[i]?.quantity ?? ""), price_kobo: toKobo(lines[i]?.price ?? "") ?? 0 })), note: note.trim() },
                "Your change is sent",
              )
            }
          >
            Send the change
          </button>
        </Modal>
      )}
      {open === "decline" && (
        <Modal title={`Decline ${o.reference}`} onClose={close}>
          <Field label="Why (they are told)">
            <textarea rows={3} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
          <ErrorBox error={act.error} />
          <button type="button" className="danger" disabled={act.busy} onClick={() => void step("decline", { note: note.trim() }, `${o.reference} declined`)}>
            Decline the order
          </button>
        </Modal>
      )}
      {open === "delivered" && (
        <Modal title={`Delivered ${o.reference}`} onClose={close}>
          <p className="hint">What you delivered of each. {o.buyer_name} says All came or Not all came within a day; after that, your count is taken.</p>
          {o.lines.map((l, i) => (
            <Field key={i} label={`${l.item} (ordered ${quantity(l.quantity, l.unit)})`}>
              <input inputMode="numeric" value={got[i] ?? ""} onChange={(e) => setGot(got.map((x, j) => (j === i ? e.target.value : x)))} />
            </Field>
          ))}
          <ErrorBox error={act.error} />
          <button
            type="button"
            className="primary"
            disabled={act.busy || got.some((g) => Number.isNaN(whole(g)))}
            onClick={() => void step("delivered", { delivered: got.map(whole) }, "Delivered, and they are told")}
          >
            Record the delivery
          </button>
        </Modal>
      )}
    </section>
  );
}

/** How this linked business's orders are handled: when their invoice is
 * raised, and whether they are accepted by themselves. */
function TradeSettings({ o, onDone }: { o: TradeOrder; onDone: () => void }) {
  const act = useAction();
  const save = (body: object) => void act.run(async () => (await put(`/v1/links/${o.link}/trade`, body), onDone()), "Saved");
  return (
    <section className="card" aria-labelledby="trade-settings">
      <h2 id="trade-settings">{o.buyer_name}'s orders</h2>
      <p className="hint">Each order's invoice is raised by itself and becomes their bill on Taskiem.</p>
      <ErrorBox error={act.error} />
      <Field label="Raise the invoice">
        <select value={o.invoice_at} disabled={act.busy} onChange={(e) => save({ invoice_at: e.target.value })}>
          <option value="delivered">Once the delivery is received</option>
          <option value="accepted">When I accept an order</option>
        </select>
      </Field>
      <label className="sf-radio">
        <input type="checkbox" checked={o.auto_accept} disabled={act.busy} onChange={(e) => save({ auto_accept: e.target.checked })} />
        Accept their orders by themselves when every item has a price
      </label>
    </section>
  );
}

/** Which of our own items each line of a new order is: matched by a match
 * we confirmed, or by the same name, to confirm. Our price applies, and
 * accepting lowers our stock. */
function OwnItems({ o, onDone }: { o: TradeOrder; onDone: () => void }) {
  const items = useOwnItems();
  const act = useAction();
  const list = items.data ?? [];
  if (list.length === 0) return null;
  const set = (i: number, id: string) => void act.run(async () => (await put(`${tradePath(o.id)}/lines/${i}/item`, { item_id: id }), onDone()), "Remembered for their next orders");
  return (
    <section className="card" aria-labelledby="trade-items" data-testid="trade-items">
      <h2 id="trade-items">Your items</h2>
      <p className="hint">Which of your items each is. Your price applies, accepting takes it off your stock, and Taskiem remembers it for {o.buyer_name}'s next orders.</p>
      <ErrorBox error={act.error} />
      {o.lines.map((l, i) => (
        <div key={i} className="toolbar">
          <Field label={`Their ${l.item}`}>
            <select value={l.item_id ?? ""} disabled={act.busy} onChange={(e) => e.target.value && set(i, e.target.value)}>
              <option value="">Not one of mine</option>
              {list.map((it) => (
                <option key={it.id} value={it.id}>
                  {it.name}
                </option>
              ))}
            </select>
          </Field>
          {l.matched === "name" && l.item_id && (
            <button type="button" className="small" disabled={act.busy} onClick={() => set(i, l.item_id ?? "")}>
              Yes, that is it<span className="sr-only"> for {l.item}</span>
            </button>
          )}
        </div>
      ))}
    </section>
  );
}

/** An order we draft for a linked business that buys from us (a phone
 * order): it reaches them as a purchase order waiting for their Confirm. */
function DraftOrder({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const links = useLoad(() => get<LinksView>("/v1/links"), []);
  const items = useOwnItems();
  const act = useAction();
  const buyers = (links.data?.links ?? []).filter((l) => l.state === "active" && (l.role === "customer" || l.role === "both"));
  const own = items.data ?? [];
  const [link, setLink] = useState("");
  const [lines, setLines] = useState([{ item_id: "", item: "", quantity: "1", price: "" }]);
  const [note, setNote] = useState("");
  const whole = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : NaN);
  const setLine = (i: number, p: Partial<(typeof lines)[number]>) => setLines(lines.map((x, j) => (j === i ? { ...x, ...p } : x)));
  const body = {
    link: link || buyers[0]?.id,
    lines: lines.map((l) =>
      l.item_id ? { item_id: l.item_id, quantity: whole(l.quantity) } : { item: l.item.trim(), quantity: whole(l.quantity), price_kobo: toKobo(l.price) ?? 0 },
    ),
    note: note.trim(),
  };
  return (
    <Modal title="Draft an order for them" onClose={onClose}>
      <ErrorBox error={links.error ?? act.error} />
      {links.data && buyers.length === 0 ? (
        <p className="hint">
          You are not linked with a business that buys from you yet. <Link to="/chat">Find them</Link>
        </p>
      ) : (
        <>
          <p className="hint">They confirm it, or cancel it, in their Taskiem; until then it goes no further.</p>
          <Field label="Which business">
            <select value={link || buyers[0]?.id || ""} onChange={(e) => setLink(e.target.value)}>
              {buyers.map((b) => (
                <option key={b.id} value={b.id}>
                  {b.name}
                </option>
              ))}
            </select>
          </Field>
          {lines.map((l, i) => (
            <fieldset key={i} className="sf-choices">
              <legend>Item {i + 1}</legend>
              {own.length > 0 && (
                <Field label="Your item">
                  <select value={l.item_id} onChange={(e) => setLine(i, { item_id: e.target.value })}>
                    <option value="">Another, named here</option>
                    {own.map((it) => (
                      <option key={it.id} value={it.id}>
                        {it.name}
                      </option>
                    ))}
                  </select>
                </Field>
              )}
              {!l.item_id && (
                <>
                  <Field label="What it is">
                    <input maxLength={120} value={l.item} onChange={(e) => setLine(i, { item: e.target.value })} />
                  </Field>
                  <Field label="Price each (₦)">
                    <input inputMode="decimal" value={l.price} onChange={(e) => setLine(i, { price: e.target.value })} />
                  </Field>
                </>
              )}
              <Field label="How many">
                <input inputMode="numeric" value={l.quantity} onChange={(e) => setLine(i, { quantity: e.target.value })} />
              </Field>
            </fieldset>
          ))}
          <div className="toolbar">
            <button type="button" className="small" disabled={lines.length >= 50} onClick={() => setLines([...lines, { item_id: "", item: "", quantity: "1", price: "" }])}>
              Add an item
            </button>
          </div>
          <Field label="A word for them (optional)">
            <input maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
          <button
            type="button"
            className="primary"
            disabled={act.busy || !body.link || lines.some((l) => Number.isNaN(whole(l.quantity)))}
            onClick={() => void act.run(async () => (await post("/v1/trade-orders", body), onDone()), "Sent for them to confirm")}
          >
            Send for them to confirm
          </button>
        </>
      )}
    </Modal>
  );
}

// --- the buyer's side ---

/** One purchase order of ours, with a linked supplier's progress on it and
 * what we may do: accept its change, cancel or ask to, say what came. */
export function PurchasePage() {
  const { id = "" } = useParams();
  const { data, error, reload } = useLoad(() => get<{ purchase: Purchase }>(`/v1/stock/purchases/${encodeURIComponent(id)}`), [id]);
  const { can } = useAuth();
  const p = data?.purchase;
  return (
    <>
      <p className="crumbs">
        <Link to="/storefront/stock" aria-label="Back to Stock">
          Stock
        </Link>
      </p>
      <PageHeader title={p ? `${p.reference} to ${p.supplier}` : "Order"} />
      <ErrorBox error={error} />
      {!p ? (
        !error && <Skeleton rows={3} label="Loading the order…" />
      ) : (
        <>
          <section className="hero" aria-label="The order" data-testid="purchase-hero">
            <div className="hero-top">
              <span className="hero-label">Order total</span>
              <span className="hero-tag">{STATUS[p.status].label}</span>
            </div>
            <div className="hero-amount">{naira(p.total_kobo)}</div>
            <p className="hero-sub">To {p.supplier}</p>
            <dl className="hero-foot">
              <div>
                <dt>Received</dt>
                <dd>{naira(p.received_kobo)}</dd>
              </div>
              <div>
                <dt>Owed</dt>
                <dd>{naira(p.owed_kobo)}</dd>
              </div>
            </dl>
          </section>
          {sellerLine(p) && (
            <div className="notice" role="status" data-testid="purchase-seller">
              {sellerLine(p)}
              {p.seller_note ? ` “${p.seller_note}”` : ""}
            </div>
          )}
          {p.cancel_asked_at && <p className="hint">You asked {p.supplier} to cancel it; they cancel it or keep it.</p>}
          <section className="card" aria-labelledby="purchase-lines">
            <h2 id="purchase-lines">What you ordered</h2>
            <table>
              <thead>
                <tr>
                  <th scope="col">Item</th>
                  <th scope="col">Ordered</th>
                  <th scope="col">Price each</th>
                  {p.delivered_at && <th scope="col">Delivered</th>}
                  <th scope="col">Received</th>
                </tr>
              </thead>
              <tbody>
                {p.lines.map((l) => (
                  <tr key={l.item_id}>
                    <td>{l.item}</td>
                    <td>{quantity(l.quantity, l.unit)}</td>
                    <td className="sf-price">{naira(l.cost_kobo)}</td>
                    {p.delivered_at && <td>{l.delivered ?? 0}</td>}
                    <td>{l.received}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </section>
          {p.change && p.seller_status === "changed" && (
            <section className="card" aria-labelledby="purchase-change" data-testid="purchase-change">
              <h2 id="purchase-change">{p.supplier} can supply it like this</h2>
              <table>
                <thead>
                  <tr>
                    <th scope="col">Item</th>
                    <th scope="col">Quantity</th>
                    <th scope="col">Price each</th>
                  </tr>
                </thead>
                <tbody>
                  {p.change.map((l) => (
                    <tr key={l.item_id}>
                      <td>{l.item}</td>
                      <td>{l.quantity === 0 ? "None" : quantity(l.quantity, l.unit)}</td>
                      <td className="sf-price">{naira(l.cost_kobo)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <p className="lines-total">
                <span>New total</span>
                <strong className="sf-price">{naira(p.change.reduce((n, l) => n + l.quantity * l.cost_kobo, 0))}</strong>
              </p>
            </section>
          )}
          {can("storefront.manage") && buyerSteps(p).confirmProposed && <ConfirmProposed p={p} onDone={reload} />}
          <BuyerActions p={p} onDone={reload} manage={can("storefront.manage")} receive={can("table.edit")} />
        </>
      )}
    </>
  );
}

/** Confirming an order a linked supplier drafted for us: which of our own
 * items each line is (what comes is received into it). */
function ConfirmProposed({ p, onDone }: { p: Purchase; onDone: () => void }) {
  const items = useOwnItems();
  const act = useAction();
  const own = items.data ?? [];
  const [chosen, setChosen] = useState<Record<number, string>>({});
  const pick = (i: number) => chosen[i] ?? sameName(own, p.lines[i]?.item ?? "");
  const all = p.lines.map((_, i) => pick(i));
  return (
    <section className="card" aria-labelledby="purchase-confirm" data-testid="purchase-confirm">
      <h2 id="purchase-confirm">Confirm it</h2>
      {items.data && own.length === 0 ? (
        <p className="hint">
          Keep your stock on Taskiem to confirm it, so what comes is received into your items. <Link to="/storefront/stock">Stock</Link>
        </p>
      ) : (
        <>
          <p className="hint">Which of your items each is. What comes is received into it.</p>
          {p.lines.map((l, i) => (
            <Field key={i} label={`Their ${l.item}`}>
              <select value={pick(i)} onChange={(e) => setChosen({ ...chosen, [i]: e.target.value })}>
                <option value="">Choose your item</option>
                {own.map((it) => (
                  <option key={it.id} value={it.id}>
                    {it.name}
                  </option>
                ))}
              </select>
            </Field>
          ))}
          <ErrorBox error={act.error} />
          <button
            type="button"
            className="primary"
            disabled={act.busy || all.some((x) => !x)}
            onClick={() => void act.run(async () => (await post(`/v1/stock/purchases/${p.id}/confirm-proposed`, { items: all }), onDone()), "Confirmed, and the supplier is told")}
          >
            Confirm the order
          </button>
        </>
      )}
    </section>
  );
}

function BuyerActions({ p, onDone, manage, receive }: { p: Purchase; onDone: () => void; manage: boolean; receive: boolean }) {
  const can = buyerSteps(p);
  const act = useAction();
  const [short, setShort] = useState(false);
  const [counts, setCounts] = useState<string[]>([]);
  const go = (what: string, body: object | undefined, done: string) =>
    act.run(async () => (await post(`/v1/stock/purchases/${p.id}/${what}`, body), onDone()), done).then((ok) => ok && setShort(false));
  const whole = (v: string) => (/^\d+$/.test(v.trim()) ? Number(v.trim()) : NaN);
  const shown = (manage && (can.acceptChange || can.cancel)) || (receive && can.answerDelivery);
  const asking = p.seller_status === "accepted" || p.seller_status === "ready" || p.seller_status === "dispatched";
  if (!shown) return null;
  return (
    <section className="card" aria-labelledby="purchase-steps">
      <h2 id="purchase-steps">Answer {p.supplier}</h2>
      <ErrorBox error={short ? undefined : act.error} />
      {receive && can.answerDelivery && <p className="hint">Within a day of the delivery; after that, {p.supplier}'s count is taken.</p>}
      <div className="row-actions">
        {manage && can.acceptChange && (
          <button type="button" className="primary" disabled={act.busy} onClick={() => void go("accept-change", undefined, "Change accepted")}>
            Accept the change
          </button>
        )}
        {receive && can.answerDelivery && (
          <>
            <button type="button" className="primary" disabled={act.busy} onClick={() => void go("all-came", undefined, "Received, and your stock is up")}>
              All came
            </button>
            <button type="button" onClick={() => (setCounts(p.lines.map((l) => String(l.delivered ?? 0))), setShort(true))}>
              Not all came
            </button>
          </>
        )}
        {manage && can.cancel && (
          <button
            type="button"
            className="danger"
            disabled={act.busy}
            onClick={() =>
              confirm(asking ? `Ask ${p.supplier} to cancel ${p.reference}?` : `Cancel ${p.reference}?`) &&
              void go("cancel", undefined, "Done, and the supplier is told")
            }
          >
            Cancel the order
          </button>
        )}
      </div>
      {short && (
        <Modal title="Not all came" onClose={() => (setShort(false), act.clear())}>
          <p className="hint">How many came of each. Your stock goes up by your count, and {p.supplier} is told.</p>
          {p.lines.map((l, i) => (
            <Field key={l.item_id} label={`${l.item} (delivered ${l.delivered ?? 0})`}>
              <input inputMode="numeric" value={counts[i] ?? ""} onChange={(e) => setCounts(counts.map((x, j) => (j === i ? e.target.value : x)))} />
            </Field>
          ))}
          <ErrorBox error={act.error} />
          <button
            type="button"
            className="primary"
            disabled={act.busy || counts.some((c) => Number.isNaN(whole(c)))}
            onClick={() => void go("not-all-came", { counts: counts.map(whole) }, "Recorded, and the supplier is told")}
          >
            Record what came
          </button>
        </Modal>
      )}
    </section>
  );
}
