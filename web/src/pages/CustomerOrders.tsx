// Orders from customers' messages (docs/spec/chat-five-more-flows.md,
// phase 2): the card above a customer's reply box. What Taskiem read from
// their message, with its questions; staff answer them, change it, ask the
// customer, and confirm it. Nothing goes to the customer until a person
// taps Ask them or Confirm.
import { useEffect, useId, useState } from "react";
import { ApiError, get, post, put } from "../api";
import { Icon } from "../icons";
import { naira } from "../lib/billing";
import {
  lastConfirmed,
  lineKobo,
  lineNote,
  lineWords,
  orderPill,
  orderSource,
  PAY_WORDS,
  payChoices,
  payNow,
  workingOn,
  type OrderDraft,
  type OrderLine,
  type OrdersOfChat,
  type PickItem,
} from "../lib/customerorders";
import { ErrorBox, useAction, useLoad } from "../ui";

export function CustomerOrders({
  chatId,
  canSend,
}: {
  chatId: string;
  /** The customer can be written to (not stopped, not erased). */
  canSend: boolean;
}) {
  const [tick, setTick] = useState(0);
  // Followed as the thread is: a customer's answer lands on the card.
  useEffect(() => {
    const t = window.setInterval(() => setTick((n) => n + 1), 4000);
    return () => window.clearInterval(t);
  }, []);
  const load = useLoad(
    () =>
      get<OrdersOfChat>(
        `/v1/customer-chats/${encodeURIComponent(chatId)}/orders`,
      ),
    [chatId, tick],
  );
  const act = useAction();
  const [told, setTold] = useState<{ id: string; error: string }>();
  const data = load.data;
  const working = workingOn(data?.orders);
  const confirmed = lastConfirmed(data?.orders);
  const reload = () => setTick((n) => n + 1);

  if (!data) return <ErrorBox error={load.error} />;
  return (
    <div className="customer-orders" data-testid="customer-orders">
      <ErrorBox error={act.error} />
      {working ? (
        <OrderCard
          key={working.id}
          d={working}
          data={data}
          canSend={canSend}
          onChanged={reload}
          onTold={(id, error) => setTold(error ? { id, error } : undefined)}
        />
      ) : (
        <>
          {confirmed && (
            <ConfirmedCard
              d={confirmed}
              told={told?.id === confirmed.id ? told.error : undefined}
              onAgain={() =>
                void act
                  .run(
                    () =>
                      post(
                        `/v1/order-drafts/${encodeURIComponent(confirmed.id)}/tell`,
                      ),
                    "Sent to them",
                  )
                  .then((ok) => ok && setTold(undefined))
              }
              busy={act.busy}
            />
          )}
          {canSend && (
            <button
              type="button"
              className="small customer-order-start"
              disabled={act.busy}
              onClick={() =>
                void act
                  .run(
                    () =>
                      post(
                        `/v1/customer-chats/${encodeURIComponent(chatId)}/orders`,
                      ),
                    false,
                  )
                  .then((ok) => ok && reload())
              }
            >
              <Icon name="plus" /> Start an order
            </button>
          )}
        </>
      )}
    </div>
  );
}

/** A confirmed order: where it stands, and a send that failed. */
function ConfirmedCard({
  d,
  told,
  onAgain,
  busy,
}: {
  d: OrderDraft;
  told?: string;
  onAgain: () => void;
  busy: boolean;
}) {
  const pill = orderPill(d);
  return (
    <article
      className="tk-chatcard customer-order done"
      aria-label={`Order ${d.reference ?? ""}`}
      data-testid="customer-order"
    >
      <span className="tk-chatcard-top">
        <span className="tk-kicker">Order {d.reference}</span>
        <span className={`tk-pill ${pill.tone}`}>{pill.word}</span>
      </span>
      <span className="card-title">
        {d.lines.length} item{d.lines.length === 1 ? "" : "s"} ·{" "}
        {naira(d.total_kobo)}
      </span>
      {told && (
        <>
          <p className="customer-note stop" role="alert">
            Not sent to them: {told}
          </p>
          <span className="tk-chatcard-actions">
            <button
              type="button"
              className="small"
              onClick={onAgain}
              disabled={busy}
            >
              Send it again
            </button>
          </span>
        </>
      )}
    </article>
  );
}

function OrderCard({
  d,
  data,
  canSend,
  onChanged,
  onTold,
}: {
  d: OrderDraft;
  data: OrdersOfChat;
  canSend: boolean;
  onChanged: () => void;
  onTold: (id: string, error?: string) => void;
}) {
  const act = useAction();
  const [open, setOpen] = useState(true);
  const ids = useId();
  const pill = orderPill(d);
  const base = `/v1/order-drafts/${encodeURIComponent(d.id)}`;
  // One change on the version shown; a change someone made since reloads.
  const change = (
    body: Record<string, unknown>,
    done: string | false = false,
  ) =>
    void act
      .run(async () => {
        try {
          await put(base, { version: d.version, ...body });
        } finally {
          onChanged();
        }
      }, done)
      .then(() => undefined);
  const step = (path: string, done: string) =>
    void act
      .run(async () => {
        try {
          const out = await post<{ told?: boolean; told_error?: string }>(
            `${base}/${path}`,
            { version: d.version },
          );
          if (path === "confirm")
            onTold(d.id, out?.told === false ? out.told_error : undefined);
        } catch (e) {
          if (e instanceof ApiError && e.status === 409) onChanged();
          throw e;
        }
        onChanged();
      }, done)
      .then(() => undefined);
  const saved = payNow(d, data.pay_page);
  const [pay, setPay] = useState(saved);
  useEffect(() => setPay(saved), [saved]);
  const waitsWhy = !d.ready
    ? d.questions > 0
      ? "Answer its questions first."
      : !d.lines.length
        ? "Add what they want."
        : !d.fulfilment
          ? "Say whether it is delivered or picked up."
          : d.fulfilment === "delivery" && !d.address && !d.zone
            ? "Say where to deliver it."
            : "Every line needs an item, how many and a price."
    : "";

  return (
    <article
      className="tk-chatcard customer-order"
      aria-labelledby={`${ids}-title`}
      data-testid="customer-order"
    >
      <span className="tk-chatcard-top">
        <span className="tk-kicker" id={`${ids}-title`}>
          {orderSource(d)}
        </span>
        <span className={`tk-pill ${pill.tone}`}>{pill.word}</span>
        <button
          type="button"
          className="icon-button small customer-order-fold"
          aria-expanded={open}
          aria-controls={`${ids}-body`}
          onClick={() => setOpen((o) => !o)}
        >
          <Icon name="chevron" />
          <span className="sr-only">
            {open ? "Fold the order" : "Open the order"}
          </span>
        </button>
      </span>
      <span className="card-title">
        {d.lines.length} item{d.lines.length === 1 ? "" : "s"} ·{" "}
        {naira(d.total_kobo)}
      </span>
      {open && (
        <div className="customer-order-body" id={`${ids}-body`}>
          <ErrorBox error={act.error} />
          <ul className="customer-order-lines">
            {d.lines.map((l, i) => (
              <LineRow
                key={`${i}-${l.item_id ?? l.said}`}
                l={l}
                i={i}
                items={data.items}
                busy={act.busy}
                change={change}
              />
            ))}
          </ul>
          <AddItem items={data.items} busy={act.busy} change={change} />
          <Details d={d} data={data} busy={act.busy} change={change} />
          <fieldset className="customer-order-pay">
            <legend>How they pay</legend>
            {payChoices(d, data.pay_page).map((p) => (
              <label key={p} className="check">
                <input
                  type="radio"
                  name={`${ids}-pay`}
                  checked={pay === p}
                  disabled={act.busy}
                  onChange={() => {
                    setPay(p);
                    change({ op: "details", pay: p });
                  }}
                />
                {PAY_WORDS[p]}
              </label>
            ))}
          </fieldset>
          {(d.when || d.note) && (
            <p className="customer-order-said">
              {[d.when, d.note].filter(Boolean).join(" · ")}
            </p>
          )}
          <dl className="customer-order-sums">
            <dt>Items</dt>
            <dd>{naira(d.items_kobo)}</dd>
            {d.fulfilment === "delivery" && (
              <>
                <dt>Delivery</dt>
                <dd>{naira(d.delivery_kobo)}</dd>
              </>
            )}
            <dt>Total</dt>
            <dd>
              <strong>{naira(d.total_kobo)}</strong>
            </dd>
          </dl>
        </div>
      )}
      <span className="tk-chatcard-actions">
        {d.questions > 0 && canSend && (
          <button
            type="button"
            className="small"
            disabled={act.busy}
            onClick={() => step("ask", "Asked them")}
          >
            Ask them
          </button>
        )}
        <button
          type="button"
          className="small primary"
          disabled={act.busy || !d.ready || !canSend}
          aria-describedby={waitsWhy ? `${ids}-why` : undefined}
          onClick={() => step("confirm", "Order confirmed")}
        >
          Confirm
        </button>
        <button
          type="button"
          className="small"
          disabled={act.busy}
          onClick={() => step("drop", "Order dropped")}
        >
          Drop
        </button>
      </span>
      {waitsWhy && (
        <span className="tk-chatcard-sub" id={`${ids}-why`}>
          {waitsWhy}
        </span>
      )}
    </article>
  );
}

type Change = (body: Record<string, unknown>, done?: string | false) => void;

function LineRow({
  l,
  i,
  items,
  busy,
  change,
}: {
  l: OrderLine;
  i: number;
  items: PickItem[];
  busy: boolean;
  change: Change;
}) {
  const kobo = lineKobo(l);
  const note = lineNote(l);
  const what = l.name ?? l.said ?? "this line";
  const [howMany, setHowMany] = useState("");
  const [price, setPrice] = useState("");
  const ids = useId();
  return (
    <li className="customer-order-line" data-testid="customer-order-line">
      <span className="customer-order-row">
        <span className="grow">{lineWords(l)}</span>
        {kobo !== undefined && <span>{naira(kobo)}</span>}
      </span>
      {l.said && l.item_id && (
        <span className="customer-order-said">They wrote “{l.said}”</span>
      )}
      {note && <span className="customer-order-warn">{note}</span>}
      {l.ask && (
        <span
          className="customer-order-ask"
          role="group"
          aria-label={l.ask.text}
        >
          <span>{l.ask.text}</span>
          {l.ask.kind === "how_many" ? (
            <span className="customer-order-row">
              <label className="sr-only" htmlFor={`${ids}-n`}>
                How many {l.name}
              </label>
              <input
                id={`${ids}-n`}
                inputMode="numeric"
                value={howMany}
                onChange={(e) => setHowMany(e.target.value.replace(/\D/g, ""))}
              />
              <button
                type="button"
                className="small"
                disabled={busy || !Number(howMany)}
                onClick={() =>
                  change({ op: "quantity", line: i, quantity: Number(howMany) })
                }
              >
                Set
              </button>
            </span>
          ) : (
            <span className="tk-chatcard-actions">
              {l.ask.choices?.map((c) => (
                <button
                  key={c.item_id}
                  type="button"
                  className="small"
                  disabled={busy}
                  onClick={() =>
                    change({ op: "choose", line: i, item_id: c.item_id })
                  }
                >
                  {c.name}
                  {c.price_kobo !== undefined
                    ? ` · ${naira(c.price_kobo)}`
                    : ""}
                </button>
              ))}
            </span>
          )}
        </span>
      )}
      {!l.item_id && !l.ask && (
        <span className="customer-order-row">
          <label className="sr-only" htmlFor={`${ids}-pick`}>
            The item for “{l.said}”
          </label>
          <select
            id={`${ids}-pick`}
            value=""
            disabled={busy}
            onChange={(e) =>
              e.target.value &&
              change({ op: "choose", line: i, item_id: e.target.value })
            }
          >
            <option value="">Pick the item…</option>
            {items.map((it) => (
              <option key={it.id} value={it.id}>
                {it.name}
              </option>
            ))}
          </select>
        </span>
      )}
      {l.item_id && l.no_price && (
        <span className="customer-order-row">
          <label htmlFor={`${ids}-price`}>Price, ₦</label>
          <input
            id={`${ids}-price`}
            inputMode="decimal"
            value={price}
            onChange={(e) => setPrice(e.target.value.replace(/[^\d.]/g, ""))}
          />
          <button
            type="button"
            className="small"
            disabled={busy || !Number(price)}
            onClick={() =>
              change({
                op: "price",
                line: i,
                price_kobo: Math.round(Number(price) * 100),
              })
            }
          >
            Set
          </button>
        </span>
      )}
      <span className="customer-order-row customer-order-tools">
        {l.item_id && (l.quantity ?? 0) > 0 && (
          <>
            <button
              type="button"
              className="icon-button small"
              disabled={busy || (l.quantity ?? 0) <= 1}
              onClick={() =>
                change({
                  op: "quantity",
                  line: i,
                  quantity: (l.quantity ?? 1) - 1,
                })
              }
            >
              <Icon name="minus" />
              <span className="sr-only">One fewer {what}</span>
            </button>
            <button
              type="button"
              className="icon-button small"
              disabled={busy}
              onClick={() =>
                change({
                  op: "quantity",
                  line: i,
                  quantity: (l.quantity ?? 0) + 1,
                })
              }
            >
              <Icon name="plus" />
              <span className="sr-only">One more {what}</span>
            </button>
          </>
        )}
        <button
          type="button"
          className="icon-button small"
          disabled={busy}
          onClick={() => change({ op: "remove", line: i })}
        >
          <Icon name="trash" />
          <span className="sr-only">Remove {what}</span>
        </button>
      </span>
    </li>
  );
}

function AddItem({
  items,
  busy,
  change,
}: {
  items: PickItem[];
  busy: boolean;
  change: Change;
}) {
  const id = useId();
  if (!items.length) return null;
  return (
    <span className="customer-order-row">
      <label className="sr-only" htmlFor={id}>
        Add an item
      </label>
      <select
        id={id}
        value=""
        disabled={busy}
        onChange={(e) =>
          e.target.value &&
          change({ op: "add", item_id: e.target.value, quantity: 1 })
        }
      >
        <option value="">Add an item…</option>
        {items.map((it) => (
          <option key={it.id} value={it.id}>
            {it.name}
            {it.price_kobo !== undefined ? ` · ${naira(it.price_kobo)}` : ""}
          </option>
        ))}
      </select>
    </span>
  );
}

/** Delivered or picked up; where, and the delivery fee. */
function Details({
  d,
  data,
  busy,
  change,
}: {
  d: OrderDraft;
  data: OrdersOfChat;
  busy: boolean;
  change: Change;
}) {
  const ids = useId();
  // The choice shows at once; the saved order follows.
  const [getBy, setGetBy] = useState(d.fulfilment);
  useEffect(() => setGetBy(d.fulfilment), [d.fulfilment]);
  const [address, setAddress] = useState(d.address ?? "");
  const [fee, setFee] = useState(
    d.delivery_kobo ? String(d.delivery_kobo / 100) : "",
  );
  useEffect(() => setAddress(d.address ?? ""), [d.address]);
  useEffect(
    () => setFee(d.delivery_kobo ? String(d.delivery_kobo / 100) : ""),
    [d.delivery_kobo],
  );
  const dirty =
    address !== (d.address ?? "") ||
    Math.round(Number(fee || 0) * 100) !== d.delivery_kobo;
  return (
    <div className="customer-order-details">
      <fieldset>
        <legend>How they get it</legend>
        {(["delivery", "pickup"] as const).map((f) => (
          <label key={f} className="check">
            <input
              type="radio"
              name={`${ids}-get`}
              checked={getBy === f}
              disabled={busy}
              onChange={() => {
                setGetBy(f);
                change({ op: "details", fulfilment: f });
              }}
            />
            {f === "delivery" ? "Delivered" : "They pick it up"}
          </label>
        ))}
      </fieldset>
      {getBy === "delivery" && (
        <>
          <label htmlFor={`${ids}-address`}>Deliver to</label>
          <input
            id={`${ids}-address`}
            value={address}
            maxLength={300}
            onChange={(e) => setAddress(e.target.value)}
          />
          {data.zones.length > 0 && (
            <>
              <label htmlFor={`${ids}-zone`}>Delivery area</label>
              <select
                id={`${ids}-zone`}
                value={d.zone ?? ""}
                disabled={busy}
                onChange={(e) => {
                  const z = data.zones.find((x) => x.name === e.target.value);
                  change({
                    op: "details",
                    zone: e.target.value,
                    delivery_kobo: z?.fee_kobo ?? 0,
                  });
                }}
              >
                <option value="">Pick the area…</option>
                {data.zones.map((z) => (
                  <option key={z.name} value={z.name}>
                    {z.name} · {naira(z.fee_kobo)}
                  </option>
                ))}
              </select>
            </>
          )}
          <label htmlFor={`${ids}-fee`}>Delivery fee, ₦</label>
          <input
            id={`${ids}-fee`}
            inputMode="decimal"
            value={fee}
            onChange={(e) => setFee(e.target.value.replace(/[^\d.]/g, ""))}
          />
          {dirty && (
            <button
              type="button"
              className="small"
              disabled={busy}
              onClick={() =>
                change({
                  op: "details",
                  address,
                  delivery_kobo: Math.round(Number(fee || 0) * 100),
                })
              }
            >
              Save
            </button>
          )}
        </>
      )}
    </div>
  );
}
