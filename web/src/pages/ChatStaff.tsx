import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { get, post, postOnce } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { KitEmpty, KitField, KitLoading, SavedTick, Sheet } from "../kit";
import {
  barHeights,
  cashWords,
  changeWords,
  firstName,
  saleOf,
  SHOW_BRIEFING,
  type Briefing,
  type Handover,
  type PaidHow,
  type SalesChart,
} from "../lib/staff";
import type { ToolKey } from "../lib/tools";
import type { StockItem, StockView } from "../lib/stock";
import {
  findSize,
  hasSizes,
  label,
  pickable,
  sizesOf,
  sizesWords,
  type Variant,
} from "../lib/variants";
import { formatPrice } from "../lib/storefront";
import { ErrorBox, useLoad } from "../ui";
import { toast } from "../toast";
import { staffTile } from "../lib/team";
import { tasksTile } from "../lib/tasks";

// Staff who work from chat (docs/spec/chat-tools.md §7): a cashier's Sell,
// a counter sale as the Order sheet looks (the items with steppers, the pine
// bag, then how it was paid), recorded as the Stock page's sales are
// (POST /v1/stock/sales): the stock goes down and the money is written.

const HOWS: { how: PaidHow; words: string }[] = [
  { how: "cash", words: "Cash" },
  { how: "transfer", words: "Transfer" },
  { how: "pos", words: "POS" },
];

/** Sell: what was sold, and how it was paid. */
export function SellSheet({
  from,
  onClose,
  onSold,
}: {
  from?: DOMRect | null;
  onClose: () => void;
  onSold?: () => void;
}) {
  const items = useLoad(
    () =>
      get<StockView>("/v1/stock").then((r) =>
        r.items.filter((x) => x.price_kobo !== undefined && x.available),
      ),
    [],
  );
  const [qty, setQty] = useState<Record<string, number>>({});
  const [search, setSearch] = useState("");
  const [step, setStep] = useState<"pick" | "pay">("pick");
  const [how, setHow] = useState<PaidHow>("cash");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [done, setDone] = useState<string>();
  const [shut, setShut] = useState(false);
  // One sale, one key: a retry of the same sale after a failure records it
  // once, and a changed sale (other things, or paid another way) is new.
  const keys = useRef(new Map<string, string>());
  const sale = saleOf(items.data ?? [], qty);
  const keyOf = () => {
    const sig = JSON.stringify([
      sale.lines.map((l) => [l.item.id, l.quantity]),
      how,
    ]);
    let k = keys.current.get(sig);
    if (!k) keys.current.set(sig, (k = `sell-${crypto.randomUUID()}`));
    return k;
  };
  const hows = useRef<(HTMLButtonElement | null)[]>([]);
  const moveHow = (by: number) => {
    const i =
      (HOWS.findIndex((h) => h.how === how) + by + HOWS.length) % HOWS.length;
    const next = HOWS[i];
    if (!next) return;
    setHow(next.how);
    hows.current[i]?.focus();
  };
  const q = search.trim().toLowerCase();
  // A product's sizes and colours are picked under it, not listed apart.
  const shown = (items.data ?? []).filter(
    (it) => !it.variant_of && (!q || it.name.toLowerCase().includes(q)),
  );
  const [open, setOpen] = useState<string>();
  const [picked, setPicked] = useState<Record<string, Variant>>({});
  const record = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await postOnce("/v1/stock/sales", keyOf(), {
        lines: sale.lines.map((l) => ({
          item_id: l.item.id,
          quantity: l.quantity,
        })),
        paid_how: how,
      });
      setDone(`Sold: ${formatPrice(sale.total)}`);
      onSold?.();
      window.setTimeout(() => setShut(true), 1300);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  const set = (it: StockItem, n: number) =>
    setQty({ ...qty, [it.id]: Math.max(0, n) });
  return (
    <Sheet
      title={step === "pay" ? "How was it paid?" : "Sell"}
      from={from}
      onClose={onClose}
      onBack={step === "pay" ? () => setStep("pick") : undefined}
      shut={shut}
      testid="sell-sheet"
      footer={
        step === "pick" ? (
          <div className="tk-bag" data-testid="sell-bag">
            <span>
              <span className="tk-bag-count">{sale.count}</span>
              <span className="tk-bag-total">{formatPrice(sale.total)}</span>
            </span>
            <button
              type="button"
              className="tk-bag-go"
              disabled={sale.lines.length === 0}
              onClick={() => setStep("pay")}
            >
              Next <Icon name="chevron" />
            </button>
          </div>
        ) : (
          <div className="tk-review-foot">
            <div className="tk-review-total">
              <span>Total</span>
              <b>{formatPrice(sale.total)}</b>
            </div>
            <button
              type="button"
              className="tk-cta"
              disabled={busy}
              onClick={() => void record()}
            >
              {busy ? "Recording…" : `Record the sale`}
            </button>
          </div>
        )
      }
    >
      {done && <SavedTick words={done} />}
      {!items.data ? (
        items.error ? (
          <ErrorBox error={items.error} />
        ) : (
          <KitLoading label="Loading what you sell…" />
        )
      ) : items.data.length === 0 ? (
        <KitEmpty icon="box" title="Nothing with a price yet">
          Give your products a price to sell them here.
        </KitEmpty>
      ) : step === "pick" ? (
        <>
          <label className="tk-search">
            <Icon name="search" />
            <span className="sr-only">Search what you sell</span>
            <input
              type="search"
              placeholder="Search what you sell"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <ul className="tk-sheet-list tk-order-list tk-stagger">
            {shown.map((it) => {
              if (hasSizes(it))
                return (
                  <SizedRow
                    key={it.id}
                    item={it}
                    all={items.data ?? []}
                    qty={qty}
                    set={set}
                    open={open === it.id}
                    onOpen={() => setOpen(open === it.id ? undefined : it.id)}
                    picked={picked[it.id] ?? {}}
                    onPick={(v) => setPicked({ ...picked, [it.id]: v })}
                  />
                );
              const n = qty[it.id] ?? 0;
              const out = it.stock !== null && it.stock <= 0;
              const over = it.stock !== null && n > Math.max(0, it.stock);
              return (
                <li
                  key={it.id}
                  className={`tk-order-row${n > 0 ? " on" : ""}`}
                  data-testid="sell-row"
                >
                  <span className="tk-row-tile" aria-hidden="true">
                    <Icon name="box" />
                  </span>
                  <span className="tk-row-words">
                    <span className="tk-row-name">{it.name}</span>
                    <span className="tk-row-sub">
                      {formatPrice(it.price_kobo ?? 0)}
                      {it.unit ? ` a ${it.unit}` : ""}
                      {it.stock !== null
                        ? ` · ${out ? "none left" : `${it.stock} left`}`
                        : ""}
                    </span>
                    {over && (
                      <span className="tk-row-warn" role="status">
                        {out
                          ? "None left in your stock"
                          : `Only ${it.stock} left in your stock`}
                      </span>
                    )}
                  </span>
                  <span
                    className="tk-qty"
                    role="group"
                    aria-label={`How many ${it.name}`}
                  >
                    <button
                      type="button"
                      aria-label={`One fewer ${it.name}`}
                      disabled={n === 0}
                      onClick={() => set(it, n - 1)}
                    >
                      −
                    </button>
                    <span aria-live="polite">{n}</span>
                    <button
                      type="button"
                      aria-label={`One more ${it.name}`}
                      onClick={() => set(it, n + 1)}
                    >
                      +
                    </button>
                  </span>
                </li>
              );
            })}
          </ul>
        </>
      ) : (
        <div className="tk-sheet-list tk-form-body">
          <ul className="tk-lines">
            {sale.lines.map((l) => (
              <li key={l.item.id}>
                <span>
                  {l.quantity} × {l.item.name}
                </span>
                <b>{formatPrice(l.quantity * (l.item.price_kobo ?? 0))}</b>
              </li>
            ))}
          </ul>
          <div
            className="tk-hows"
            role="radiogroup"
            aria-label="How it was paid"
          >
            {HOWS.map((h, i) => (
              <button
                key={h.how}
                ref={(el) => {
                  hows.current[i] = el;
                }}
                type="button"
                role="radio"
                aria-checked={how === h.how}
                tabIndex={how === h.how ? 0 : -1}
                className={how === h.how ? "on" : undefined}
                onClick={() => setHow(h.how)}
                onKeyDown={(e) => {
                  if (e.key === "ArrowRight" || e.key === "ArrowDown") {
                    e.preventDefault();
                    moveHow(1);
                  } else if (e.key === "ArrowLeft" || e.key === "ArrowUp") {
                    e.preventDefault();
                    moveHow(-1);
                  }
                }}
              >
                {h.words}
              </button>
            ))}
          </div>
          <p className="tk-form-now">
            The stock goes down, and the money is written in your money log.
          </p>
          <ErrorBox error={error} />
        </div>
      )}
    </Sheet>
  );
}

const HOW_WORDS: Record<string, string> = {
  cash: "Cash",
  transfer: "Transfer",
  pos: "POS",
  credit: "On credit",
};

/** Handover: the shift so far, the cash counted against the cash sold, a
 * note; told to those who run the business. */
export function HandoverSheet({
  from,
  onClose,
  onSent,
}: {
  from?: DOMRect | null;
  onClose: () => void;
  onSent?: () => void;
}) {
  const shift = useLoad(() => get<Handover>("/v1/chat-handover"), []);
  const [cash, setCash] = useState("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [done, setDone] = useState(false);
  const [shut, setShut] = useState(false);
  const h = shift.data;
  const counted =
    cash.trim() === ""
      ? undefined
      : Math.round(Number(cash.replace(/[,\s₦]/g, "")) * 100);
  const sold = h?.sales?.by_how.cash ?? 0;
  const send = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await post("/v1/chat-handover", {
        ...(counted !== undefined &&
          !Number.isNaN(counted) && { cash_counted_kobo: counted }),
        note: note.trim(),
      });
      setDone(true);
      onSent?.();
      window.setTimeout(() => setShut(true), 1300);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  return (
    <Sheet
      title="Hand the shift over"
      from={from}
      onClose={onClose}
      shut={shut}
      testid="handover-sheet"
      footer={
        <button
          type="button"
          className="tk-cta"
          disabled={
            busy || !h || (counted !== undefined && Number.isNaN(counted))
          }
          onClick={() => void send()}
        >
          {busy ? "Sending…" : "Hand over"}
        </button>
      }
    >
      {done && <SavedTick words="Handed over" />}
      {!h ? (
        shift.error ? (
          <ErrorBox error={shift.error} />
        ) : (
          <KitLoading rows={3} label="Adding up the shift…" />
        )
      ) : (
        <div className="tk-sheet-list tk-form-body">
          {h.sales && (
            <div className="tk-bill-hero" data-testid="handover-hero">
              <span className="tk-kicker">Sold today</span>
              <b>{formatPrice(h.sales.total_kobo)}</b>
              <span className="tk-row-sub">
                {h.sales.count === 1 ? "1 sale" : `${h.sales.count} sales`}
                {h.sales.waiting > 0
                  ? ` · ${h.sales.waiting} waiting for their money`
                  : ""}
              </span>
            </div>
          )}
          <ul className="tk-lines">
            {Object.entries(h.sales?.by_how ?? {})
              .filter(([, v]) => v > 0)
              .map(([how, v]) => (
                <li key={how}>
                  <span>{HOW_WORDS[how] ?? how}</span>
                  <b>{formatPrice(v)}</b>
                </li>
              ))}
            {h.orders_open !== undefined && (
              <li>
                <span>Orders still open</span>
                <b>{h.orders_open}</b>
              </li>
            )}
            {h.deliveries && (
              <li>
                <span>Deliveries on the way · done</span>
                <b>
                  {h.deliveries.out} · {h.deliveries.done}
                </b>
              </li>
            )}
            {h.low.length > 0 && (
              <li>
                <span>Low</span>
                <b>{h.low.join(", ")}</b>
              </li>
            )}
          </ul>
          <KitField
            label="Cash counted (₦)"
            hint={
              counted !== undefined && !Number.isNaN(counted)
                ? `${cashWords(counted, sold, formatPrice)} · ${formatPrice(sold)} sold in cash`
                : `${formatPrice(sold)} sold in cash`
            }
          >
            <input
              inputMode="decimal"
              value={cash}
              onChange={(e) => setCash(e.target.value)}
              placeholder="0"
            />
          </KitField>
          <KitField label="A note for the next shift (optional)">
            <textarea
              rows={2}
              value={note}
              maxLength={500}
              onChange={(e) => setNote(e.target.value)}
              placeholder="The freezer is making a noise"
            />
          </KitField>
          <ErrorBox error={error} />
        </div>
      )}
    </Sheet>
  );
}

/** A chart card: "how did we do?" answered (a Taskiem message of kind
 * chart), the period's bars, the one asked about lit. */
export function ChartCard({ chart }: { chart: SalesChart }) {
  const heights = barHeights(chart.bars);
  const change = changeWords(chart.change_pct, chart.than);
  const step = chart.bars.length > 10 ? 5 : 1;
  return (
    <article
      className="tk-chart"
      aria-label={`${chart.title}: ${formatPrice(chart.total_kobo)}`}
      data-testid="chart-card"
    >
      <span className="tk-kicker">{chart.title}</span>
      <b className="tk-chart-total">{formatPrice(chart.total_kobo)}</b>
      {change && (
        <span
          className={`tk-chart-change ${chart.change_pct !== null && chart.change_pct < 0 ? "down" : "up"}`}
        >
          {change}
        </span>
      )}
      <span
        className="tk-bars"
        role="img"
        aria-label={chart.bars
          .map((b) => `${b.label} ${formatPrice(b.kobo)}`)
          .join(", ")}
      >
        {chart.bars.map((b, i) => (
          <span
            key={b.day}
            className={`tk-bar${i === chart.highlight || chart.highlight < 0 ? " on" : ""}`}
          >
            <span
              style={{ height: `${Math.round((heights[i] ?? 0) * 100)}%` }}
            />
            <small aria-hidden="true">
              {i % step === 0 || i === chart.bars.length - 1 ? b.label : ""}
            </small>
          </span>
        ))}
      </span>
    </article>
  );
}

/** The morning briefing (the Alerts design): a pine card from 8am with
 * yesterday's sales and the week's bars, and what is owed, low and due
 * today, each opening its tool or page; Got it puts it away for the day. */
export function BriefingCard({
  openTool,
  onAsk,
}: {
  openTool: (k: ToolKey, from?: DOMRect) => void;
  onAsk: (words: string) => void;
}) {
  const { me } = useAuth();
  const [b, setB] = useState<Briefing>();
  const [gone, setGone] = useState<string>();
  const [error, setError] = useState<unknown>();
  const card = useRef<HTMLElement>(null);
  const asked = useRef(false);
  // Read again when the page comes back, and every ten minutes: the card
  // appears at 8am and a new day's card replaces yesterday's. Asked for
  // (the tools menu's Briefing, or "briefing"), today's comes back, put
  // away or not.
  useEffect(() => {
    let live = true;
    const load = () => {
      if (document.visibilityState === "hidden") return;
      get<Briefing>("/v1/chat-briefing").then(
        (x) => live && !asked.current && setB(x),
        (e: unknown) => console.warn("the morning briefing", e),
      );
    };
    const show = () => {
      asked.current = true;
      get<Briefing>("/v1/chat-briefing?show=1").then(
        (x) => {
          asked.current = false;
          if (!live) return;
          setB(x);
          setGone(undefined);
          setError(undefined);
          requestAnimationFrame(() => {
            card.current?.scrollIntoView({ block: "nearest" });
            card.current?.focus();
          });
        },
        (e: unknown) => {
          asked.current = false;
          console.warn("the morning briefing", e);
          toast("The briefing didn't load. Try again.", "error");
        },
      );
    };
    load();
    const every = window.setInterval(load, 10 * 60_000);
    document.addEventListener("visibilitychange", load);
    window.addEventListener("focus", load);
    window.addEventListener(SHOW_BRIEFING, show);
    return () => {
      live = false;
      window.clearInterval(every);
      document.removeEventListener("visibilitychange", load);
      window.removeEventListener("focus", load);
      window.removeEventListener(SHOW_BRIEFING, show);
    };
  }, []);
  if (!b || !b.ready || b.put_off || gone === b.key) return null;
  const gotIt = () => {
    setGone(b.key);
    setError(undefined);
    post("/v1/chat-alerts/later", { key: b.key, hours: 24 }).catch(
      (e: unknown) => {
        // Not put away: the card comes back, saying so.
        setGone(undefined);
        setError(e);
      },
    );
  };
  const day = new Date(`${b.day}T12:00:00`);
  const hello =
    b.part === "afternoon"
      ? "Good afternoon"
      : b.part === "evening"
        ? "Good evening"
        : "Good morning";
  const change =
    b.sales && changeWords(b.sales.change_pct, `last ${b.sales.weekday}`);
  const heights = b.sales ? barHeights(b.sales.week) : [];
  return (
    <article
      ref={card}
      tabIndex={-1}
      className="tk-brief"
      aria-label="Today's briefing"
      data-testid="briefing-card"
    >
      <span className="tk-brief-head">
        <span>
          <span className="tk-kicker">
            {hello}, {firstName(me?.user?.name, me?.user?.email)}
          </span>
          <span className="tk-brief-day">
            {day.toLocaleDateString("en-GB", { weekday: "long" })}
            {b.now ? ` · ${b.now}` : ""}
          </span>
        </span>
        {/* Live: today's numbers (the Alerts board's dot). */}
        <span className="tk-brief-live" aria-hidden="true" />
      </span>
      {b.sales && (
        <button
          type="button"
          className="tk-brief-sales"
          onClick={() => onAsk("how did we do yesterday")}
        >
          <span>
            <span className="tk-brief-label">Sales yesterday</span>
            <b>{formatPrice(b.sales.yesterday_kobo)}</b>
            {change && <span className="tk-brief-change">{change}</span>}
          </span>
          <span className="tk-brief-bars" aria-hidden="true">
            {b.sales.week.map((d, i, week) => (
              <span
                key={d.day}
                className={i === week.length - 1 ? "on" : undefined}
                style={{ height: `${Math.round((heights[i] ?? 0) * 100)}%` }}
              />
            ))}
          </span>
          <span className="sr-only">: ask how we did yesterday</span>
        </button>
      )}
      <span className="tk-brief-tiles">
        {b.owed_kobo !== undefined && (
          <Link to="/invoices" className="tk-brief-tile">
            <span>Owed to you</span>
            <b>{formatPrice(b.owed_kobo)}</b>
          </Link>
        )}
        {b.low !== undefined && (
          <button
            type="button"
            className="tk-brief-tile"
            onClick={(e) =>
              openTool("stock", e.currentTarget.getBoundingClientRect())
            }
          >
            <span>Low stock</span>
            <b>{b.low === 1 ? "1 item" : `${b.low} items`}</b>
          </button>
        )}
        {b.deliveries !== undefined && (
          <Link to="/storefront/deliveries" className="tk-brief-tile">
            <span>Deliveries</span>
            <b>{b.deliveries} today</b>
          </Link>
        )}
        {b.staff && (
          <Link to="/staff" className="tk-brief-tile">
            <span>Staff</span>
            <b>{staffTile(b.staff)}</b>
          </Link>
        )}
        {b.tasks && (
          <Link to="/tasks" className="tk-brief-tile">
            <span>Tasks</span>
            <b>{tasksTile(b.tasks)}</b>
          </Link>
        )}
        {b.bookings !== undefined && b.bookings > 0 && (
          <Link to="/storefront/bookings" className="tk-brief-tile">
            <span>Bookings</span>
            <b>{b.bookings} today</b>
          </Link>
        )}
      </span>
      <button type="button" className="tk-brief-ok" onClick={gotIt}>
        Got it<span className="sr-only">: put the briefing away for today</span>
      </button>
      {error !== undefined && (
        <p className="tk-brief-err" role="alert">
          That didn't go through. Try Got it again.
        </p>
      )}
    </article>
  );
}

/** A product with sizes and colours in Sell: Choose opens its choices, one
 * row of buttons each (what is off sale or has none left greyed out);
 * the size picked gets its own − n +, and those chosen so far are listed
 * under the product. */
function SizedRow({
  item,
  all,
  qty,
  set,
  open,
  onOpen,
  picked,
  onPick,
}: {
  item: StockItem;
  all: StockItem[];
  qty: Record<string, number>;
  set: (it: StockItem, n: number) => void;
  open: boolean;
  onOpen: () => void;
  picked: Variant;
  onPick: (v: Variant) => void;
}) {
  const cs = item.choices ?? [];
  const sizes = sizesOf(all, item.id);
  const chosen = sizes.filter((v) => (qty[v.id] ?? 0) > 0);
  const n = chosen.reduce((a, v) => a + (qty[v.id] ?? 0), 0);
  const left = sizes.reduce((a, v) => a + (v.stock ?? 0), 0);
  const found = cs.every((c) => picked[c.name])
    ? findSize(all, item.id, picked)
    : undefined;
  const stepper = (v: StockItem) => {
    const m = qty[v.id] ?? 0;
    const name = `${item.name}, ${label(v.variant, cs)}`;
    return (
      <span className="tk-qty" role="group" aria-label={`How many ${name}`}>
        <button
          type="button"
          aria-label={`One fewer ${name}`}
          disabled={m === 0}
          onClick={() => set(v, m - 1)}
        >
          −
        </button>
        <span aria-live="polite">{m}</span>
        <button
          type="button"
          aria-label={`One more ${name}`}
          onClick={() => set(v, m + 1)}
        >
          +
        </button>
      </span>
    );
  };
  return (
    <li
      className={`tk-order-row tk-sized${n > 0 ? " on" : ""}`}
      data-testid="sell-row"
    >
      <div className="tk-sized-head">
        <span className="tk-row-tile" aria-hidden="true">
          <Icon name="box" />
        </span>
        <span className="tk-row-words">
          <span className="tk-row-name">{item.name}</span>
          <span className="tk-row-sub">
            {item.price_kobo !== undefined ? formatPrice(item.price_kobo) : ""}
            {` · ${sizesWords(sizes.length)} · ${left} left`}
          </span>
        </span>
        <button
          type="button"
          className="tk-btn soft"
          aria-expanded={open}
          onClick={onOpen}
        >
          {open ? "Done" : "Choose"}
          <span className="sr-only"> a size or colour of {item.name}</span>
        </button>
      </div>
      {chosen.length > 0 && (
        <ul className="tk-sized-chosen" aria-label={`${item.name} chosen`}>
          {chosen.map((v) => (
            <li key={v.id}>
              <span>
                {label(v.variant, cs)}
                {v.price_kobo !== undefined && v.price_kobo !== item.price_kobo
                  ? ` · ${formatPrice(v.price_kobo)}`
                  : ""}
              </span>
              {stepper(v)}
            </li>
          ))}
        </ul>
      )}
      {open && (
        <div className="tk-sized-pick">
          {cs.map((c) => {
            const can = pickable(all, item.id, c.name, picked);
            return (
              <div
                key={c.name}
                className="tk-chips tk-field-chips"
                role="group"
                aria-label={c.name}
              >
                {c.values.map((x) => (
                  <button
                    key={x}
                    type="button"
                    aria-pressed={picked[c.name] === x}
                    disabled={!can.has(x)}
                    onClick={() => onPick({ ...picked, [c.name]: x })}
                  >
                    {x}
                  </button>
                ))}
              </div>
            );
          })}
          {found ? (
            <div className="tk-sized-found">
              <span>
                {label(found.variant, cs)}
                {found.stock !== null ? ` · ${found.stock} left` : ""}
              </span>
              {stepper(found)}
            </div>
          ) : (
            <p className="hint">
              Choose {cs.map((c) => c.name.toLowerCase()).join(" and ")}.
            </p>
          )}
        </div>
      )}
    </li>
  );
}
