import { Fragment, useEffect, useState, type ReactNode } from "react";
import { TradeOrdersCard } from "./TradeOrders";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { ApiError, get, post } from "../api";
import { useAuth } from "../auth";
import {
  EmptyState,
  ErrorBox,
  Item,
  Items,
  PageHeader,
  Skeleton,
  fmtTime,
  useAction,
  useLiveRefresh,
  useLoad,
} from "../ui";
import { formatPrice, type Storefront } from "../lib/storefront";
import { OrderDelivery } from "./Dispatch";
import {
  KIND_LABEL,
  ORDER_FILTERS,
  ordersPath,
  isMasked,
  orderStatus,
  orderTotal,
  orderWhat,
  paymentSummary,
  priceChangeLine,
  refundLine,
  relativeAge,
  telLink,
  timeLeft,
  whatsappLink,
  type Customer,
  type Order,
} from "../lib/orders";
import { timeSpan, whenLabel } from "../lib/bookings";
import {
  howMatchedOrder,
  orderPaymentRows,
  orderPaymentUndoPath,
  type OrderPayment,
} from "../lib/invoices";
import { StorefrontTabs } from "./Storefront";
import { BookingActions } from "./StorefrontBookings";

/** The money matched to an order (docs/matching.md), each with how; a wrong match is undone here while the order has not gone on. */
function OrderMoney({
  payments,
  manage,
  onUndone,
}: {
  payments?: OrderPayment[] | null;
  manage: boolean;
  onUndone: () => void;
}) {
  const act = useAction();
  const rows = orderPaymentRows(payments);
  if (rows.length === 0) return null;
  return (
    <section className="card" aria-labelledby="order-money">
      <h2 id="order-money">Money matched</h2>
      <ul className="match-candidates">
        {rows.map(({ payment: p, undone }) => (
          <li key={p.id} data-testid="order-payment">
            <div className="match-head">
              <span className="sf-price">{formatPrice(p.amount_kobo)}</span>{" "}
              <span data-testid="how-matched">
                {howMatchedOrder(p.how, p.by)}
              </span>{" "}
              <span className="hint">{fmtTime(p.created_at)}</span>
              {undone && <span className="badge sf-status muted"> Undone</span>}
            </div>
            {(p.reasons ?? []).length > 0 && (
              <ul className="match-reasons" aria-label="Why it was matched">
                {(p.reasons ?? []).map((r) => (
                  <li key={r}>{r}</li>
                ))}
              </ul>
            )}
            {manage && !undone && p.undoable && (
              <button
                type="button"
                disabled={act.busy}
                onClick={() =>
                  confirm(
                    "Undo this match? The order goes back to unpaid, and the money waits to be matched again.",
                  ) &&
                  void act.run(
                    async () => (
                      await post(orderPaymentUndoPath(p.id)),
                      onUndone()
                    ),
                    "Match undone",
                  )
                }
              >
                Undo the match
              </button>
            )}
          </li>
        ))}
      </ul>
      <ErrorBox error={act.error} />
    </section>
  );
}

/** Orders waiting on the owner or the customer: their icon is tinted amber. */
const WAITING = new Set(["received", "awaiting_payment", "price_changed"]);

function StatusBadge({ status }: { status: string }) {
  const s = orderStatus(status);
  return <span className={`badge sf-status ${s.tone}`}>{s.label}</span>;
}

const header = (
  <PageHeader
    title="Orders"
    description="Orders, bookings, questions and call-back requests from your storefront, newest first. The list refreshes itself."
  />
);

/** Accepting and declining happen in the approval request, not here. */
function DecideWhere() {
  return (
    <p className="hint sf-links">
      You accept or decline each one in the approval request Taskiem sends you
      in WhatsApp or Telegram, or on the <Link to="/approvals">Approvals</Link>{" "}
      page.
    </p>
  );
}

/** The storefront's orders (GET /v1/storefront/orders), customers masked. */
export function StorefrontOrders() {
  const [params, setParams] = useSearchParams();
  const status = params.get("status") ?? "open";
  // A 404: this Taskiem takes no storefront orders (no public host key).
  const list = useLoad(
    () =>
      get<{ orders: Order[] }>(ordersPath(status)).catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }),
    [status],
  );
  const sf = useLoad(
    () =>
      get<Storefront>("/v1/storefront").catch((e: unknown) => {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }),
    [],
  );
  useLiveRefresh(list.reload);
  // Ages count on between refreshes.
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, [list.data]);
  const orders = list.data?.orders;
  const share = sf.data && sf.data.published_version > 0 && sf.data.preview_url;
  return (
    <>
      {header}
      <StorefrontTabs />
      <DecideWhere />
      <TradeOrdersCard />
      <div className="filters" role="search" aria-label="Filter orders">
        <label>
          Show{" "}
          <select
            value={status}
            onChange={(e) =>
              setParams(
                e.target.value === "open" ? {} : { status: e.target.value },
                { replace: true },
              )
            }
          >
            {ORDER_FILTERS.map((f) => (
              <option key={f.label} value={f.value}>
                {f.label}
              </option>
            ))}
          </select>
        </label>
        <button type="button" onClick={list.reload}>
          Refresh
        </button>
      </div>
      <ErrorBox error={list.error} />
      {orders === undefined && !list.error && (
        <Skeleton rows={5} label="Loading orders…" />
      )}
      {orders === null && (
        <EmptyState icon="storefront">
          This Taskiem does not take storefront orders yet.
        </EmptyState>
      )}
      {orders && orders.length === 0 && (
        <EmptyState icon="storefront">
          <span className="sf-links">
            {status === "open" || status === "all" ? (
              share ? (
                <>
                  No orders yet. Share your page:{" "}
                  <a
                    href={sf.data?.preview_url}
                    target="_blank"
                    rel="noreferrer"
                  >
                    {sf.data?.preview_url.replace(/^https?:\/\//, "")}
                  </a>
                </>
              ) : (
                <>
                  No orders yet.{" "}
                  <Link to="/storefront">Publish your storefront</Link> and
                  switch on its forms to take orders.
                </>
              )
            ) : (
              "Nothing with this status."
            )}
          </span>
        </EmptyState>
      )}
      {orders && orders.length > 0 && (
        <Items label="Orders">
          {orders.map((o) => (
            <Item
              key={o.id}
              testid="order"
              className={o.status === "refund_due" ? "sf-refund" : undefined}
              to={`/storefront/orders/${encodeURIComponent(o.reference)}`}
              icon={
                o.status === "refund_due"
                  ? "refund"
                  : o.kind === "booking"
                    ? "clock"
                    : o.kind === "order" || o.kind === "trade"
                      ? "orders"
                      : "inbox"
              }
              tone={
                o.status === "refund_due"
                  ? "danger"
                  : WAITING.has(o.status)
                    ? "warn"
                    : undefined
              }
              title={
                <>
                  {o.customer.name || o.customer.phone || "A customer"}{" "}
                  <span className="hint">· {KIND_LABEL[o.kind] ?? o.kind}</span>
                </>
              }
              sub={
                <>
                  {o.customer.name && o.customer.phone
                    ? `${o.customer.phone} · `
                    : ""}
                  {o.reference}
                  {orderWhat(o) ? ` · ${orderWhat(o)}` : ""} ·{" "}
                  <time dateTime={o.created_at} title={fmtTime(o.created_at)}>
                    {relativeAge(o.created_at, now)}
                  </time>
                </>
              }
              extra={
                <>
                  <span className="sub">{paymentSummary(o)}</span>
                  {o.status === "awaiting_payment" && o.expires_at && (
                    <span className="sub">
                      Expires {timeLeft(o.expires_at, now)}
                    </span>
                  )}
                  {o.status === "price_changed" && o.price_change && (
                    <span className="sub">
                      {priceChangeLine(o.price_change)}
                    </span>
                  )}
                  {o.note && <span className="sub">{o.note}</span>}
                  {o.status === "refund_due" && (
                    <span className="sf-refund-line">
                      {refundLine(o.total_kobo, o.paid_by)}
                    </span>
                  )}
                </>
              }
              side={orderTotal(o)}
              sideSub={<StatusBadge status={o.status} />}
            />
          ))}
        </Items>
      )}
    </>
  );
}

/** One order (GET /v1/storefront/orders/{reference}), and its customer's details for those who may see them. */
export function StorefrontOrder() {
  const { reference = "" } = useParams();
  const { can } = useAuth();
  const [revealed, setRevealed] = useState<Order>();
  const [changed, setChanged] = useState("");
  const reveal = useAction();
  const refund = useAction();
  const { data, error, reload } = useLoad(
    () =>
      get<{
        order: Order;
        payments?: OrderPayment[] | null;
        revealed: boolean;
      }>(`/v1/storefront/orders/${encodeURIComponent(reference)}`),
    [reference],
  );
  useLiveRefresh(reload);
  const o = data?.order;
  return (
    <>
      <p className="crumbs">
        <Link to="/storefront/orders" aria-label="Back to Orders">
          Orders
        </Link>
      </p>
      <PageHeader
        title={`${o ? (KIND_LABEL[o.kind] ?? "Order") : "Order"} ${reference}`}
      />
      <ErrorBox error={error} />
      {!o && !error && <Skeleton rows={4} label="Loading the order…" />}
      {o && (
        <>
          <OrderHero o={o} />
          {o.status === "refund_due" && (
            <div className="sf-refund-note" role="status">
              {refundLine(o.deposit_kobo ?? o.total_kobo, o.paid_by)}
              {o.note && <div className="hint">{o.note}</div>}
              <div className="toolbar">
                {(!o.paid_by || o.paid_by === "card") && (
                  <button
                    type="button"
                    disabled={refund.busy}
                    onClick={() =>
                      confirm(
                        `Refund ${formatPrice(o.deposit_kobo ?? o.total_kobo)} to the customer now? It comes from your Paystack balance.`,
                      ) &&
                      void refund.run(
                        async () => (
                          await post(
                            `/v1/storefront/orders/${encodeURIComponent(o.reference)}/refund`,
                          ),
                          reload()
                        ),
                        "Refunded",
                      )
                    }
                  >
                    Refund through Paystack
                  </button>
                )}
                <button
                  type="button"
                  className={
                    o.paid_by && o.paid_by !== "card" ? "primary" : undefined
                  }
                  disabled={refund.busy}
                  onClick={() =>
                    confirm(
                      `Mark ${o.reference} refunded? Only once you have sent ${formatPrice(o.deposit_kobo ?? o.total_kobo)} back to the customer. They will be told.`,
                    ) &&
                    void refund.run(
                      async () => (
                        await post(
                          `/v1/storefront/orders/${encodeURIComponent(o.reference)}/refunded`,
                          {},
                        ),
                        reload()
                      ),
                      "Marked refunded",
                    )
                  }
                >
                  Mark refunded
                </button>
              </div>
              <ErrorBox error={refund.error} />
            </div>
          )}
          <section className="card" aria-labelledby="order-summary">
            <h2 id="order-summary">Details</h2>
            <dl className="kv">
              <dt>Status</dt>
              <dd>
                <StatusBadge status={o.status} />
              </dd>
              <dt>Received</dt>
              <dd>
                {relativeAge(o.created_at)}{" "}
                <span className="hint">({fmtTime(o.created_at)})</span>
              </dd>
              {o.kind === "booking" && (
                <>
                  <dt>Service</dt>
                  <dd>{o.service_name || <span className="hint">—</span>}</dd>
                  <dt>When</dt>
                  <dd>
                    {o.starts_at ? (
                      <>
                        {whenLabel(o.starts_at)}{" "}
                        <span className="hint">
                          ({timeSpan(o)}, Lagos time)
                        </span>
                      </>
                    ) : (
                      <span className="hint">—</span>
                    )}
                  </dd>
                  <dt>Price</dt>
                  <dd className="sf-price">{orderTotal(o)}</dd>
                </>
              )}
              {o.status === "price_changed" && o.price_change && (
                <>
                  <dt>New prices</dt>
                  <dd>
                    {priceChangeLine(o.price_change)}
                    <div className="hint">
                      The customer is asked to agree to the new total before the
                      order goes on.
                    </div>
                  </dd>
                </>
              )}
              {o.basket && o.basket.length > 0 && o.kind !== "booking" && (
                <>
                  <dt>Items</dt>
                  <dd>
                    <ul className="sf-lines">
                      {o.basket.map((l, i) => (
                        <li key={l.id ?? i}>
                          {l.quantity ?? 1} × {l.name}
                          {l.line_kobo !== undefined && (
                            <span className="sf-price">
                              {" "}
                              {formatPrice(l.line_kobo)}
                            </span>
                          )}
                        </li>
                      ))}
                      {o.zone && (
                        <li>
                          Delivery to {o.zone}
                          <span className="sf-price">
                            {" "}
                            {formatPrice(o.delivery_kobo ?? 0)}
                          </span>
                        </li>
                      )}
                    </ul>
                  </dd>
                  <dt>Total</dt>
                  <dd className="sf-price">{formatPrice(o.total_kobo)}</dd>
                </>
              )}
              {o.fulfilment && (
                <>
                  <dt>Fulfilment</dt>
                  <dd className="capitalize">{o.fulfilment}</dd>
                </>
              )}
              {(o.kind === "order" || o.kind === "booking") && (
                <>
                  <dt>Payment</dt>
                  <dd>
                    {paymentSummary(o)}
                    {o.paid_at && (
                      <span className="hint"> ({fmtTime(o.paid_at)})</span>
                    )}
                  </dd>
                </>
              )}
              {o.status === "awaiting_payment" && o.expires_at && (
                <>
                  <dt>Payment expires</dt>
                  <dd>{fmtTime(o.expires_at)}</dd>
                </>
              )}
              {o.note && (
                <>
                  <dt>Note</dt>
                  <dd>{o.note}</dd>
                </>
              )}
              {o.decided_at && (
                <>
                  <dt>Decided</dt>
                  <dd>
                    {fmtTime(o.decided_at)}
                    {o.decided_by && ` by ${o.decided_by}`}
                  </dd>
                </>
              )}
              <dt>Workflow</dt>
              <dd>
                {o.run_id ? (
                  <Link to={`/runs/${o.run_id}`}>
                    See where the workflow is
                  </Link>
                ) : (
                  <span className="hint">Not started yet</span>
                )}
              </dd>
            </dl>
            <DecideWhere />
            {o.kind === "booking" && (
              <>
                {changed && (
                  <div className="notice" role="status">
                    {changed}
                  </div>
                )}
                <BookingActions
                  booking={o}
                  onChanged={(msg) => {
                    setChanged(msg);
                    reload();
                  }}
                />
              </>
            )}
          </section>
          {o.kind === "trade" && (
            <section className="card" aria-labelledby="order-linked" data-testid="order-linked">
              <h2 id="order-linked">From a linked business</h2>
              <p className="hint">
                {o.customer.name || "A business"} ordered on Taskiem. Its items, a change to them, and what was delivered of each are on the linked order.
              </p>
              <Link className="button" to={`/trade-orders/${encodeURIComponent(o.reference)}`}>
                Open the linked order
              </Link>
            </section>
          )}
          {(o.kind === "order" || o.kind === "trade") && (
            <OrderDelivery
              reference={o.reference}
              delivery={o.fulfilment === "delivery"}
              onChanged={reload}
            />
          )}
          <OrderMoney
            payments={data?.payments}
            manage={can("invoice.manage")}
            onUndone={reload}
          />
          <section className="card" aria-labelledby="order-customer">
            <h2 id="order-customer">Customer</h2>
            <CustomerDetails c={revealed?.customer ?? o.customer} />
            {!revealed && can("pii.reveal") && (
              <>
                <ErrorBox error={reveal.error} />
                <button
                  disabled={reveal.busy}
                  onClick={() =>
                    void reveal.run(async () => {
                      const r = await get<{ order: Order; revealed: boolean }>(
                        `/v1/storefront/orders/${encodeURIComponent(reference)}?reveal=true`,
                      );
                      setRevealed(r.order);
                    }, false)
                  }
                >
                  Show customer's details
                </button>
                <p className="hint">
                  Showing them is recorded in the audit log.
                </p>
              </>
            )}
            {!revealed && !can("pii.reveal") && (
              <p className="hint">
                The customer's details are hidden. Someone who may see personal
                data can show them.
              </p>
            )}
          </section>
        </>
      )}
    </>
  );
}

/** The top of an order's or a booking's page, on pine: its total, how it is
 * paid, the customer and when. Questions and call-backs carry no money and have none. */
function OrderHero({ o }: { o: Order }) {
  if (o.kind !== "order" && o.kind !== "booking" && o.kind !== "trade") return null;
  const booking = o.kind === "booking";
  const total = orderTotal(o);
  return (
    <section
      className="hero"
      aria-label={booking ? "The booking" : "The order"}
      data-testid="order-hero"
    >
      <div className="hero-top">
        <span className="hero-label">
          {booking ? o.service_name || "Booking" : "Order total"}
        </span>
        <span className="hero-tag">{orderStatus(o.status).label}</span>
      </div>
      <div
        className={total.startsWith("₦") ? "hero-amount" : "hero-amount words"}
      >
        {total}
      </div>
      <p className="hero-sub">{paymentSummary(o)}</p>
      <dl className="hero-foot">
        <div>
          <dt>Customer</dt>
          <dd>{o.customer.name || "—"}</dd>
        </div>
        <div>
          <dt>{booking ? "When" : "Received"}</dt>
          <dd>
            {booking
              ? o.starts_at
                ? whenLabel(o.starts_at)
                : "—"
              : relativeAge(o.created_at)}
          </dd>
        </div>
      </dl>
    </section>
  );
}

function CustomerDetails({ c }: { c: Customer }) {
  const tel = telLink(c.phone);
  const wa = whatsappLink(c.phone);
  const rows: [string, ReactNode][] = [];
  if (c.name) rows.push(["Name", c.name]);
  if (c.phone)
    rows.push([
      "Phone",
      tel ? (
        <>
          <a href={tel}>{c.phone}</a>
          {wa && (
            <>
              {" · "}
              <a href={wa} target="_blank" rel="noreferrer">
                WhatsApp
              </a>
            </>
          )}
        </>
      ) : (
        c.phone
      ),
    ]);
  if (c.email)
    rows.push([
      "Email",
      isMasked(c.email) ? c.email : <a href={`mailto:${c.email}`}>{c.email}</a>,
    ]);
  if (c.address) rows.push(["Address", c.address]);
  if (c.landmark) rows.push(["Landmark", c.landmark]);
  if (c.pin)
    rows.push([
      "Location",
      c.pin.startsWith("https://") ? (
        <a href={c.pin} target="_blank" rel="noreferrer">
          Open the map
        </a>
      ) : (
        c.pin
      ),
    ]);
  if (c.message) rows.push(["Message", c.message]);
  if (c.best_time) rows.push(["Best time", c.best_time]);
  if (rows.length === 0) return <p className="hint">No details given.</p>;
  return (
    <dl className="kv sf-links" style={{ marginBottom: 12 }}>
      {rows.map(([k, v]) => (
        <Fragment key={k}>
          <dt>{k}</dt>
          <dd style={{ whiteSpace: "pre-wrap" }}>{v}</dd>
        </Fragment>
      ))}
    </dl>
  );
}
