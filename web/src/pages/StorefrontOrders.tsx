import { Fragment, useEffect, useState, type ReactNode } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { ApiError, get, post } from "../api";
import { useAuth } from "../auth";
import { EmptyState, ErrorBox, PageHeader, Skeleton, fmtTime, useAction, useLiveRefresh, useLoad } from "../ui";
import { formatPrice, type Storefront } from "../lib/storefront";
import {
  KIND_LABEL,
  ORDER_FILTERS,
  ordersPath,
  customerLine,
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
import { StorefrontTabs } from "./Storefront";
import { BookingActions } from "./StorefrontBookings";

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
      You accept or decline each one in the approval request Taskiem sends you in WhatsApp or Telegram, or on the <Link to="/approvals">Approvals</Link> page.
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
      <div className="filters" role="search" aria-label="Filter orders">
        <label>
          Show{" "}
          <select value={status} onChange={(e) => setParams(e.target.value === "open" ? {} : { status: e.target.value }, { replace: true })}>
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
      {orders === undefined && !list.error && <Skeleton rows={5} label="Loading orders…" />}
      {orders === null && <EmptyState icon="storefront">This Taskiem does not take storefront orders yet.</EmptyState>}
      {orders && orders.length === 0 && (
        <EmptyState icon="storefront">
          <span className="sf-links">
          {status === "open" || status === "all" ? (
            share ? (
              <>
                No orders yet. Share your page:{" "}
                <a href={sf.data?.preview_url} target="_blank" rel="noreferrer">
                  {sf.data?.preview_url.replace(/^https?:\/\//, "")}
                </a>
              </>
            ) : (
              <>
                No orders yet. <Link to="/storefront">Publish your storefront</Link> and switch on its forms to take orders.
              </>
            )
          ) : (
            "Nothing with this status."
          )}
          </span>
        </EmptyState>
      )}
      {orders && orders.length > 0 && (
        <table className="cards">
          <thead>
            <tr>
              <th scope="col">Reference</th>
              <th scope="col">Status</th>
              <th scope="col">Customer</th>
              <th scope="col">What</th>
              <th scope="col">Total</th>
              <th scope="col">Payment</th>
              <th scope="col">Received</th>
            </tr>
          </thead>
          <tbody>
            {orders.map((o) => (
              <tr key={o.id} className={o.status === "refund_due" ? "sf-refund" : undefined} data-testid="order">
                <td className="primary" data-label="Reference">
                  <Link to={`/storefront/orders/${encodeURIComponent(o.reference)}`}>{o.reference}</Link> <span className="hint">{KIND_LABEL[o.kind] ?? o.kind}</span>
                </td>
                <td data-label="Status">
                  <StatusBadge status={o.status} />
                  {o.status === "awaiting_payment" && o.expires_at && <div className="hint">Expires {timeLeft(o.expires_at, now)}</div>}
                </td>
                <td data-label="Customer">{customerLine(o.customer)}</td>
                <td data-label="What">
                  {orderWhat(o) || <span className="hint">—</span>}
                  {o.status === "price_changed" && o.price_change && <div className="hint">{priceChangeLine(o.price_change)}</div>}
                  {o.note && <div className="hint">{o.note}</div>}
                  {o.status === "refund_due" && <div className="sf-refund-line">{refundLine(o.total_kobo)}</div>}
                </td>
                <td data-label="Total" className="sf-price">
                  {orderTotal(o)}
                </td>
                <td data-label="Payment">{paymentSummary(o)}</td>
                <td data-label="Received">
                  <time dateTime={o.created_at} title={fmtTime(o.created_at)}>
                    {relativeAge(o.created_at, now)}
                  </time>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
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
  const { data, error, reload } = useLoad(() => get<{ order: Order; revealed: boolean }>(`/v1/storefront/orders/${encodeURIComponent(reference)}`), [reference]);
  useLiveRefresh(reload);
  const o = data?.order;
  return (
    <>
      <PageHeader
        title={`${o ? (KIND_LABEL[o.kind] ?? "Order") : "Order"} ${reference}`}
        actions={<Link to="/storefront/orders">All orders</Link>}
      />
      <ErrorBox error={error} />
      {!o && !error && <Skeleton rows={4} label="Loading the order…" />}
      {o && (
        <>
          {o.status === "refund_due" && (
            <div className="sf-refund-note" role="status">
              {refundLine(o.deposit_kobo ?? o.total_kobo)}
              {o.note && <div className="hint">{o.note}</div>}
              <div>
                <button
                  type="button"
                  disabled={refund.busy}
                  onClick={() =>
                    confirm(`Refund ${formatPrice(o.deposit_kobo ?? o.total_kobo)} to the customer now? It comes from your Paystack balance.`) &&
                    void refund.run(async () => (await post(`/v1/storefront/orders/${encodeURIComponent(o.reference)}/refund`), reload()))
                  }
                >
                  Refund through Paystack
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
                {relativeAge(o.created_at)} <span className="hint">({fmtTime(o.created_at)})</span>
              </dd>
              {o.kind === "booking" && (
                <>
                  <dt>Service</dt>
                  <dd>{o.service_name || <span className="hint">—</span>}</dd>
                  <dt>When</dt>
                  <dd>
                    {o.starts_at ? (
                      <>
                        {whenLabel(o.starts_at)} <span className="hint">({timeSpan(o)}, Lagos time)</span>
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
                    <div className="hint">The customer is asked to agree to the new total before the order goes on.</div>
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
                          {l.line_kobo !== undefined && <span className="sf-price"> {formatPrice(l.line_kobo)}</span>}
                        </li>
                      ))}
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
                    {o.paid_at && <span className="hint"> ({fmtTime(o.paid_at)})</span>}
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
              <dd>{o.run_id ? <Link to={`/runs/${o.run_id}`}>See where the workflow is</Link> : <span className="hint">Not started yet</span>}</dd>
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
                      const r = await get<{ order: Order; revealed: boolean }>(`/v1/storefront/orders/${encodeURIComponent(reference)}?reveal=true`);
                      setRevealed(r.order);
                    })
                  }
                >
                  Show customer's details
                </button>
                <p className="hint">Showing them is recorded in the audit log.</p>
              </>
            )}
            {!revealed && !can("pii.reveal") && <p className="hint">The customer's details are hidden. Someone who may see personal data can show them.</p>}
          </section>
        </>
      )}
    </>
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
  if (c.email) rows.push(["Email", isMasked(c.email) ? c.email : <a href={`mailto:${c.email}`}>{c.email}</a>]);
  if (c.address) rows.push(["Address", c.address]);
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
