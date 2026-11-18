import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { del, get, post, put } from "../api";
import { Icon } from "../icons";
import { ShareToPerson } from "./People";
import { getDraft, setDraft } from "../lib/drafts";
import { aboutOrderDraft, deliveryStep, photoURL, stateWords, waitingWords } from "../lib/personchat";
import { dayLabel, lagosDay, lagosTime } from "../lib/bookings";
import { PageCache } from "../lib/pagecache";
import { formatPrice } from "../lib/storefront";
import {
  addPage,
  addressBody,
  addressForm,
  addressFormProblem,
  addressLine,
  addressProblem,
  addressSub,
  bookingWhen,
  confirmedWords,
  dueWords,
  EARLIER_WORDS,
  emptyAddress,
  fulfilmentWords,
  freshen,
  MAX_ADDRESSES,
  orderAPI,
  orderIcon,
  orderLine,
  orderPath,
  orderProblem,
  ordersPage,
  orderTitle,
  payProblem,
  type AddressForm,
  type PersonOrder,
  type PersonOrderDetail,
  type PersonOrders,
  type SavedAddress,
} from "../lib/youorders";
import { EmptyState, ErrorBox, Field, fmtTime, Items, Modal, PageHeader, Row, Skeleton, useAction, useLoad } from "../ui";
import { useConfirm } from "./ConfirmSheet";

// Orders on You (docs/spec/taskiem-for-everyone.md §13): every order and
// booking the person has with any business, newest first, each with Pay
// when something is due and Chat about this; one order's page; and the
// person's saved addresses (kept on their side; a business sees only the
// address copied onto an order).

/** A pill tone of the chat's words as a row's badge tone. */
const BADGE: Record<string, string> = { warn: "warn", lime: "ok", plain: "muted", danger: "danger" };

/** Pay one order: its pay link asked for, then opened by the person (a
 * link opened after a request would be blocked as a pop-up). */
export function OrderPay({ order, small = true }: { order: Pick<PersonOrder, "business_id" | "reference" | "due_kobo">; small?: boolean }) {
  const [url, setUrl] = useState<string>();
  const act = useAction();
  if (url)
    return (
      <a className={`button primary${small ? " small" : ""}`} href={url} target="_blank" rel="noopener noreferrer" data-testid="order-pay-open">
        Open the pay page<span className="sr-only"> for {order.reference}</span>
      </a>
    );
  const said = payProblem(act.error);
  return (
    <>
      <button
        type="button"
        className={`primary${small ? " small" : ""}`}
        disabled={act.busy}
        data-testid="order-pay"
        onClick={() =>
          void act.run(async () => {
            const r = await get<{ url: string }>(`${orderAPI(order)}/pay`);
            setUrl(r.url);
          }, false)
        }
      >
        {act.busy ? "One moment…" : `Pay ${order.due_kobo > 0 ? formatPrice(order.due_kobo) : ""}`.trim()}
        <span className="sr-only"> for {order.reference}</span>
      </button>
      {said ? (
        <span className="error" role="alert">
          {said}
        </span>
      ) : (
        <ErrorBox error={act.error} />
      )}
    </>
  );
}

/** Chat about this order: the chat with its business, made when there
 * is none (from: order), with "Hello, about my order …" in the box. */
export function useChatAbout() {
  const nav = useNavigate();
  return (o: Pick<PersonOrder, "business_id" | "reference" | "chat_id">) => {
    if (o.chat_id) {
      const key = `person:${o.chat_id}`;
      if (!getDraft(key)) setDraft(key, aboutOrderDraft(o.reference));
      nav(`/you/chats/${encodeURIComponent(o.chat_id)}`);
      return;
    }
    nav(`/you/chats/new?business=${encodeURIComponent(o.business_id)}&ref=${encodeURIComponent(o.reference)}`);
  };
}

/** The pages of Orders on You as last seen, so Back from an order returns
 * to them (this tab's memory only, for a few minutes). */
interface OrderPages {
  orders: PersonOrder[];
  next: string | null;
  waiting: number;
}
const keptOrders = new PageCache<OrderPages>(1);
const ORDERS_KEY = "orders";

/** /you/orders: the list, a page at a time. */
export function YouOrders() {
  const [pages, setPagesNow] = useState<OrderPages | undefined>(() => keptOrders.get(ORDERS_KEY)?.value);
  const setPages = (f: (cur: OrderPages | undefined) => OrderPages | undefined) =>
    setPagesNow((cur) => {
      const next = f(cur);
      if (next) keptOrders.set(ORDERS_KEY, next, window.scrollY);
      return next;
    });
  const [error, setError] = useState<unknown>();
  const [moreError, setMoreError] = useState<unknown>();
  const [more, setMore] = useState(false);
  const [n, setN] = useState(0);
  const act = useAction();
  const chatAbout = useChatAbout();
  // Back from an order: where the list was. Leaving: where it is now.
  const back = useRef(!!pages);
  useEffect(() => {
    const at = keptOrders.get(ORDERS_KEY)?.scroll ?? 0;
    if (back.current) requestAnimationFrame(() => window.scrollTo(0, at));
    return () => keptOrders.scrolled(ORDERS_KEY, window.scrollY);
  }, []);
  useEffect(() => {
    let live = true;
    // Coming back, the pages already open stay; the newest is read again.
    const keep = back.current && n === 0;
    get<PersonOrders>(ordersPage(null)).then(
      (r) => {
        if (!live) return;
        setPages((cur) =>
          keep && cur ? { orders: freshen(cur.orders, r.orders), next: cur.next, waiting: r.waiting_count } : { orders: r.orders, next: r.next, waiting: r.waiting_count },
        );
        setError(undefined);
      },
      (e: unknown) => live && setError(e),
    );
    return () => {
      live = false;
    };
  }, [n]);
  const loadMore = () => {
    if (!pages?.next || more) return;
    setMore(true);
    setMoreError(undefined);
    get<PersonOrders>(ordersPage(pages.next))
      .then((r) => setPages((cur) => (cur ? { orders: addPage(cur.orders, r.orders), next: r.next, waiting: r.waiting_count } : cur)), setMoreError)
      .finally(() => setMore(false));
  };
  const orders = pages?.orders ?? [];
  return (
    <>
      <PageHeader title="Orders" description="Your orders and bookings with every business, newest first." />
      {pages && pages.waiting > 0 && (
        <div className="notice" role="status" data-testid="these-are-mine">
          {waitingWords(pages.waiting)}{" "}
          <button
            type="button"
            className="small"
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                await post("/v1/me/storefront-orders/confirm");
                back.current = false;
                setN((x) => x + 1);
              }, confirmedWords)
            }
          >
            These are mine
          </button>
        </div>
      )}
      <ErrorBox error={act.error ?? error} />
      {!pages && !error && <Skeleton rows={3} label="Loading your orders…" />}
      {pages && orders.length === 0 && (
        <EmptyState
          icon="orders"
          action={
            <Link className="button" to="/you/discover">
              Find a business
            </Link>
          }
        >
          Orders and bookings you make with businesses show here.
        </EmptyState>
      )}
      {orders.length > 0 && (
        <Items label="Your orders">
          {orders.map((o) => {
            const st = stateWords(o.state);
            const due = o.earlier ? "" : dueWords(o);
            return (
              <Row
                key={`${o.business_id}/${o.reference}`}
                icon={orderIcon(o)}
                image={photoURL(o.photo)}
                tone={o.pay ? "warn" : undefined}
                title={
                  o.earlier ? (
                    <span className="you-order-name">{o.business}</span>
                  ) : (
                    <Link to={orderPath(o)} className="you-order-name">
                      {o.business}
                    </Link>
                  )
                }
                side={<span className="item-figure">{formatPrice(o.total_kobo)}</span>}
                sub={
                  <span className="sub">
                    {orderLine(o, fmtTime(o.made_at))}
                    <span className="block">
                      {st.label && <span className={`badge sf-status ${BADGE[st.tone] ?? "muted"}`}>{st.label}</span>}
                      {due && <> {due}</>}
                    </span>
                  </span>
                }
                testid="you-order"
                attrs={{ "data-status": o.state, ...(o.earlier ? { "data-earlier": "true" } : {}) }}
              >
                {o.pay && !o.earlier && <OrderPay order={o} />}
                <button type="button" className="small" onClick={() => chatAbout(o)} data-testid="order-chat-about">
                  Chat about this<span className="sr-only"> order, {o.reference}</span>
                </button>
                {!o.earlier && (
                  <Link className="button small" to={orderPath(o)}>
                    See the order<span className="sr-only"> {o.reference}</span>
                  </Link>
                )}
              </Row>
            );
          })}
        </Items>
      )}
      {orders.some((o) => o.earlier) && (
        <p className="hint" data-testid="orders-earlier-hint">
          An order marked “{EARLIER_WORDS}” was made with your phone number before it was on your account, so only its total and state are shown. Chat with the business to ask about it.
        </p>
      )}
      {moreError ? <ErrorBox error={moreError} /> : null}
      {pages?.next && (
        <button type="button" className="discover-more" disabled={more} onClick={loadMore} data-testid="orders-more">
          {more ? "Loading…" : moreError ? "Try again" : "Show older orders"}
        </button>
      )}
    </>
  );
}

/** /you/orders/:business/:reference: one order or booking. */
export function YouOrder() {
  const { business = "", reference = "" } = useParams();
  const view = useLoad(() => get<PersonOrderDetail>(orderAPI({ business_id: business, reference })), [business, reference]);
  const chatAbout = useChatAbout();
  const [sharing, setSharing] = useState(false);
  const o = view.data;
  const st = stateWords(o?.state);
  const step = o?.delivery ? deliveryStep(o.delivery) : 0;
  const said = orderProblem(view.error);
  return (
    <>
      <p className="crumbs">
        <Link to="/you/orders" aria-label="Back to Orders">
          Orders
        </Link>
      </p>
      {!o && !view.error && <Skeleton rows={4} label="Loading the order…" />}
      {said ? (
        <EmptyState icon="orders">
          <span data-testid="you-order-problem">{said}</span>
        </EmptyState>
      ) : (
        <ErrorBox error={view.error} />
      )}
      {o?.earlier && (
        <>
          <PageHeader title={orderTitle(o)} />
          <section className="hero" aria-label="The total" data-testid="you-order-hero">
            <div className="hero-top">
              <span className="hero-label">Total</span>
              {st.label && <span className="hero-tag">{st.label}</span>}
            </div>
            <div className="hero-amount">{formatPrice(o.total_kobo)}</div>
            <p className="hero-sub">{EARLIER_WORDS}</p>
            <dl className="hero-foot">
              <div>
                <dt>Business</dt>
                <dd>{o.business}</dd>
              </div>
              <div>
                <dt>Made</dt>
                <dd>{fmtTime(o.made_at)}</dd>
              </div>
            </dl>
          </section>
          <div className="row-actions page-actions you-order-actions">
            <button type="button" className="primary" onClick={() => chatAbout(o)} data-testid="order-chat-about">
              <Icon name="chat" /> Chat about this
            </button>
          </div>
          <p className="hint" data-testid="you-order-earlier">
            This order was made with your phone number before it was on your account, so only its total and state are shown here. Chat with {o.business} to ask about it.
          </p>
        </>
      )}
      {o && !o.earlier && (
        <>
          <PageHeader title={orderTitle(o)} />
          <section className="hero" aria-label={o.due_kobo > 0 ? "What is due" : "The total"} data-testid="you-order-hero">
            <div className="hero-top">
              <span className="hero-label">{o.due_kobo > 0 ? "Still to pay" : "Total"}</span>
              {st.label && <span className="hero-tag">{st.label}</span>}
            </div>
            <div className="hero-amount">{formatPrice(o.due_kobo > 0 ? o.due_kobo : o.total_kobo)}</div>
            <p className="hero-sub">
              {o.due_kobo > 0 && o.due_kobo !== o.total_kobo ? `of ${formatPrice(o.total_kobo)} · ${formatPrice(o.paid_kobo)} paid` : o.paid_kobo > 0 ? `${formatPrice(o.paid_kobo)} paid` : o.summary}
            </p>
            <dl className="hero-foot">
              <div>
                <dt>Business</dt>
                <dd>{o.business}</dd>
              </div>
              <div>
                <dt>Made</dt>
                <dd>{fmtTime(o.made_at)}</dd>
              </div>
            </dl>
          </section>
          <div className="row-actions page-actions you-order-actions">
            {o.pay && <OrderPay order={o} small={false} />}
            <button type="button" className={o.pay ? "" : "primary"} onClick={() => chatAbout(o)} data-testid="order-chat-about">
              <Icon name="chat" /> Chat about this
            </button>
            <button type="button" onClick={() => setSharing(true)} data-testid="order-share">
              <Icon name="forward" /> Share with someone
            </button>
          </div>
          {sharing && (
            <ShareToPerson card={{ type: o.delivery ? "delivery" : "order", business_id: o.business_id, reference: o.reference }} what={`order ${o.reference}`} onClose={() => setSharing(false)} />
          )}
          {o.delivery && (
            <section className="card" aria-labelledby="you-order-delivery">
              <h2 id="you-order-delivery">Delivery</h2>
              <p data-testid="you-order-delivery">{o.delivery.line}</p>
              <span className="app-card-steps" role="img" aria-label={`${o.delivery.line}: step ${deliveryStep(o.delivery)} of 4`}>
                {[1, 2, 3, 4].map((x) => (
                  <span key={x} className={x <= step ? "done" : undefined} />
                ))}
              </span>
            </section>
          )}
          {o.kind === "booking" && (
            <section className="card" aria-labelledby="you-order-when">
              <h2 id="you-order-when">When</h2>
              <p>
                {/* A service's name; a hire's, a space's, a class's or tickets' summary ("2 × Canopy, 3 days"). */}
                <strong data-testid="you-order-booked">{(o.booking_type ?? "service") === "service" ? o.service_name || o.summary : o.summary || o.service_name}</strong>
                {o.starts_at && (
                  <>
                    <br />
                    {bookingWhen(o.starts_at, o.ends_at, (t) => dayLabel(lagosDay(t)), lagosTime)} (Lagos time)
                  </>
                )}
              </p>
            </section>
          )}
          {o.lines.length > 0 && (
            <section className="card" aria-labelledby="you-order-lines">
              <h2 id="you-order-lines">What you ordered</h2>
              <table className="cards you-order-lines">
                <thead>
                  <tr>
                    <th scope="col">Item</th>
                    <th scope="col">How many</th>
                    <th scope="col" className="num">
                      Price
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {o.lines.map((l, i) => (
                    <tr key={i}>
                      <td data-label="Item">{l.name}</td>
                      <td data-label="How many">
                        {l.quantity} × {formatPrice(l.price_kobo)}
                      </td>
                      <td data-label="Price" className="num">
                        {formatPrice(l.line_kobo)}
                      </td>
                    </tr>
                  ))}
                  {(o.delivery_kobo ?? 0) > 0 && (
                    <tr>
                      <td data-label="Item">Delivery{o.zone ? ` to ${o.zone}` : ""}</td>
                      <td data-label="How many"></td>
                      <td data-label="Price" className="num">
                        {formatPrice(o.delivery_kobo ?? 0)}
                      </td>
                    </tr>
                  )}
                  <tr className="total">
                    <td data-label="Item">
                      <strong>Total</strong>
                    </td>
                    <td data-label="How many"></td>
                    <td data-label="Price" className="num">
                      <strong>{formatPrice(o.total_kobo)}</strong>
                    </td>
                  </tr>
                </tbody>
              </table>
            </section>
          )}
          {(o.fulfilment || o.address) && (
            <section className="card" aria-labelledby="you-order-how">
              <h2 id="you-order-how">How you get it</h2>
              {o.fulfilment && <p>{fulfilmentWords(o.fulfilment, o.zone)}</p>}
              {o.address && (
                <p data-testid="you-order-address">
                  {o.address.address}
                  {o.address.landmark && (
                    <>
                      <br />
                      <span className="hint">{o.address.landmark}</span>
                    </>
                  )}
                </p>
              )}
            </section>
          )}
        </>
      )}
    </>
  );
}

/** The person's saved addresses, in You's settings: add, change, delete,
 * and the default. */
export function SavedAddresses() {
  const list = useLoad(() => get<{ addresses: SavedAddress[] }>("/v1/me/addresses"), []);
  const [editing, setEditing] = useState<SavedAddress | "new">();
  const act = useAction();
  const sure = useConfirm();
  const all = list.data?.addresses ?? [];
  const remove = async (a: SavedAddress) => {
    const yes = await sure.confirm({ title: `Delete ${a.label}?`, body: "Orders you placed with it keep their copy of the address.", ok: "Delete", danger: true });
    if (!yes) return;
    await act.run(async () => {
      await del(`/v1/me/addresses/${encodeURIComponent(a.id)}`);
      list.reload();
    }, "Address deleted");
  };
  const makeDefault = (a: SavedAddress) =>
    void act.run(async () => {
      await put(`/v1/me/addresses/${encodeURIComponent(a.id)}`, { default: true });
      list.reload();
    }, `${a.label} is your default address`);
  return (
    <section className="card" aria-labelledby="you-addresses" data-testid="saved-addresses">
      <div className="toolbar">
        <h2 id="you-addresses" className="grow" style={{ margin: 0 }}>
          Your addresses
        </h2>
        {list.data && all.length < MAX_ADDRESSES && (
          <button type="button" className="small" onClick={() => setEditing("new")} data-testid="address-add">
            <Icon name="plus" /> Add an address
          </button>
        )}
      </div>
      <p className="hint">Choose one when you order, so you don't type it each time. Only the business you order from sees it, on that order.</p>
      <ErrorBox error={list.error ?? act.error} />
      {!list.data && !list.error && <Skeleton rows={2} label="Loading your addresses…" />}
      {list.data && all.length === 0 && <EmptyState icon="pin">No saved addresses yet.</EmptyState>}
      {all.length > 0 && (
        <Items label="Your addresses">
          {all.map((a) => (
            <Row key={a.id} icon="pin" title={a.label} sub={<span className="sub">{addressLine(a)}{addressSub(a) && <span className="block">{addressSub(a)}</span>}</span>} testid="saved-address">
              <button type="button" className="small" onClick={() => setEditing(a)}>
                Change<span className="sr-only"> {a.label}</span>
              </button>
              {!a.default && (
                <button type="button" className="small" disabled={act.busy} onClick={() => makeDefault(a)}>
                  Make default<span className="sr-only"> {a.label}</span>
                </button>
              )}
              <button type="button" className="small danger" disabled={act.busy} onClick={() => void remove(a)}>
                Delete<span className="sr-only"> {a.label}</span>
              </button>
            </Row>
          ))}
        </Items>
      )}
      {list.data && all.length >= MAX_ADDRESSES && <p className="hint">You have {MAX_ADDRESSES} addresses, the most you can keep.</p>}
      {editing && (
        <AddressSheet
          address={editing === "new" ? undefined : editing}
          first={all.length === 0}
          onClose={() => setEditing(undefined)}
          onSaved={() => {
            setEditing(undefined);
            list.reload();
          }}
        />
      )}
      {sure.node}
    </section>
  );
}

/** Add or change one address. */
function AddressSheet({ address, first, onClose, onSaved }: { address?: SavedAddress; first: boolean; onClose: () => void; onSaved: () => void }) {
  const [f, setF] = useState<AddressForm>(() => (address ? addressForm(address) : emptyAddress(first)));
  const [problem, setProblem] = useState<string>();
  const act = useAction();
  const set = (patch: Partial<AddressForm>) => setF((cur) => ({ ...cur, ...patch }));
  const save = () => {
    const p = addressFormProblem(f);
    setProblem(p ?? undefined);
    if (p) return;
    void act
      .run(async () => {
        if (address) await put(`/v1/me/addresses/${encodeURIComponent(address.id)}`, addressBody(f, true));
        else await post("/v1/me/addresses", addressBody(f, false));
      }, "Address saved")
      .then((ok) => ok && onSaved());
  };
  const said = addressProblem(act.error);
  return (
    <Modal title={address ? `Change ${address.label}` : "Add an address"} onClose={onClose}>
      <form
        className="address-form"
        onSubmit={(e) => {
          e.preventDefault();
          save();
        }}
        data-testid="address-form"
      >
        <Field label="Name it" hint="Such as Home or Work.">
          <input value={f.label} maxLength={40} onChange={(e) => set({ label: e.target.value })} required />
        </Field>
        <Field label="Street and number">
          <input value={f.line} maxLength={200} onChange={(e) => set({ line: e.target.value })} required />
        </Field>
        <Field label="Area" hint="Optional. Such as Yaba.">
          <input value={f.area} maxLength={80} onChange={(e) => set({ area: e.target.value })} />
        </Field>
        <Field label="Landmark" hint="Optional. Something a rider can find, such as: opposite the filling station.">
          <input value={f.landmark} maxLength={200} onChange={(e) => set({ landmark: e.target.value })} />
        </Field>
        <Field label="Phone for the rider" hint="Optional, when it is not your own number.">
          <input type="tel" inputMode="tel" value={f.phone} onChange={(e) => set({ phone: e.target.value })} />
        </Field>
        {!(address?.default ?? false) && (
          <label className="switch-line">
            <input type="checkbox" checked={f.default} onChange={(e) => set({ default: e.target.checked })} /> <span>Make this my default address</span>
          </label>
        )}
        {(problem || said) && (
          <p className="error" role="alert">
            {problem ?? said}
          </p>
        )}
        {!said && <ErrorBox error={act.error} />}
        <div className="row-actions">
          <button type="submit" className="primary" disabled={act.busy} data-testid="address-save">
            {act.busy ? "Saving…" : "Save address"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}
