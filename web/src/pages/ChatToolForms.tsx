import { useState, type ReactNode } from "react";
import { get, patch, post, postOnce, put } from "../api";
import { useConfirm } from "./ConfirmSheet";
import { KitEmpty, KitField, KitLoading, Pill } from "../kit";
import { serviceRow } from "../lib/bookings";
import type { Service, ServiceColumns } from "../lib/bookings";
import type { PriceList } from "../lib/pricelists";
import { quantity, type StockItem } from "../lib/stock";
import { formatPrice } from "../lib/storefront";
import { cellText, cellValue, type Row, type Table } from "../lib/tables";
import type { TradeOrder } from "../lib/tradeorders";
import {
  countLine,
  nairaKobo,
  nairaText,
  orderLine,
  priceLine,
  priceListLine,
  productLine,
  rowLine,
  serviceLine,
  stockInLine,
  type PriceChange,
} from "../lib/toolforms";
import { ErrorBox, useLoad } from "../ui";

// The chat tools' forms (docs/spec/chat-tools.md §3, phase 2): each does
// the common task in the sheet and saves through the same API as the
// page; what it saved comes back as its diary line (null: nothing
// changed). "Open full page" is for the rest.

/** What a form does on Save: its diary line, or null when nothing changed. */
export type Saver = () => Promise<string | null>;

const key = () => `chat-${crypto.randomUUID()}`;

/** The scrolling body and the Save at the foot, as every form has them. */
function FormFrame({
  children,
  save,
  saveWords = "Save",
  onSaved,
  ready = true,
}: {
  children: ReactNode;
  save: Saver;
  saveWords?: string;
  onSaved: (line: string | null) => void;
  ready?: boolean;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  return (
    <form
      className="tk-form"
      onSubmit={(e) => {
        e.preventDefault();
        if (busy || !ready) return;
        setBusy(true);
        setError(undefined);
        save().then(
          (line) => onSaved(line),
          (err: unknown) => (setError(err), setBusy(false)),
        );
      }}
    >
      <div className="tk-sheet-list tk-form-body">
        {children}
        <ErrorBox error={error} />
      </div>
      <div className="tk-sheet-foot">
        <button type="submit" className="tk-cta" disabled={busy || !ready}>
          {busy ? "Saving…" : saveWords}
        </button>
      </div>
    </form>
  );
}

/** − n + for a count. */
function Stepper({ label, value, onChange }: { label: string; value: number; onChange: (n: number) => void }) {
  return (
    <div className="tk-stepper" role="group" aria-label={label}>
      <button type="button" aria-label="One fewer" onClick={() => onChange(Math.max(0, value - 1))}>
        −
      </button>
      <input
        inputMode="numeric"
        aria-label={label}
        value={String(value)}
        onChange={(e) => onChange(Math.max(0, Number(e.target.value.replace(/\D/g, "")) || 0))}
      />
      <button type="button" aria-label="One more" onClick={() => onChange(value + 1)}>
        +
      </button>
    </div>
  );
}

function Money({ label, value, onChange }: { label: string; value: string; onChange: (v: string) => void }) {
  return (
    <KitField label={label}>
      <span className="tk-money">
        <span aria-hidden="true">₦</span>
        <input inputMode="decimal" value={value} aria-label={`${label} in naira`} onChange={(e) => onChange(e.target.value)} />
      </span>
    </KitField>
  );
}

/** A product: its price and its count on the shelf. */
export function ProductForm({ item, onSaved }: { item: StockItem; onSaved: (line: string | null) => void }) {
  const [price, setPrice] = useState(item.price_kobo !== undefined ? nairaText(item.price_kobo) : "");
  const [count, setCount] = useState(item.stock ?? 0);
  const kobo = nairaKobo(price);
  const save: Saver = async () => {
    const lines: string[] = [];
    if (!Number.isNaN(kobo) && kobo !== item.price_kobo) {
      await patch(`/v1/stock/items/${encodeURIComponent(item.id)}`, { price_kobo: kobo });
      lines.push(priceLine({ name: item.name, from: item.price_kobo ?? 0, to: kobo }));
    }
    if (count !== item.stock) {
      await postOnce("/v1/stock/movements", key(), { item_id: item.id, reason: "count", count, note: "Counted in chat" });
      lines.push(countLine(item.name, item.stock, count, item.unit));
    }
    return lines.length ? lines.join(". ") : null;
  };
  return (
    <FormFrame save={save} onSaved={onSaved} ready={price.trim() === "" || !Number.isNaN(kobo)}>
      <Money label="Price" value={price} onChange={setPrice} />
      <div className="tk-form-row">
        <span>
          <span className="tk-form-row-title">In stock</span>
          <span className="tk-form-row-sub">Counts down as orders are accepted</span>
        </span>
        <Stepper label="In stock" value={count} onChange={setCount} />
      </div>
    </FormFrame>
  );
}

/** A new product: its name, price, unit and how many there are. */
export function ProductAdd({ onSaved }: { onSaved: (line: string | null) => void }) {
  const [name, setName] = useState("");
  const [price, setPrice] = useState("");
  const [unit, setUnit] = useState("");
  const [count, setCount] = useState(0);
  const kobo = nairaKobo(price);
  const save: Saver = async () => {
    await postOnce("/v1/stock/items", key(), { name: name.trim(), price_kobo: kobo, unit: unit.trim(), quantity: count });
    return productLine(name.trim(), kobo, unit.trim() || undefined);
  };
  return (
    <FormFrame save={save} onSaved={onSaved} saveWords="Add product" ready={name.trim() !== "" && kobo > 0}>
      <KitField label="Name">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Rice, 50kg bag" />
      </KitField>
      <Money label="Price" value={price} onChange={setPrice} />
      <KitField label="Sold by the" hint="bag, carton, piece…">
        <input value={unit} onChange={(e) => setUnit(e.target.value)} />
      </KitField>
      <div className="tk-form-row">
        <span className="tk-form-row-title">In stock now</span>
        <Stepper label="In stock now" value={count} onChange={setCount} />
      </div>
    </FormFrame>
  );
}

/** Goods that came in. */
export function StockInForm({ item, onSaved }: { item: StockItem; onSaved: (line: string | null) => void }) {
  const [n, setN] = useState(1);
  const [note, setNote] = useState("");
  const save: Saver = async () => {
    await postOnce("/v1/stock/movements", key(), { item_id: item.id, reason: "received", change: n, note: note.trim() || "Recorded in chat" });
    return stockInLine(item.name, n, item.stock, item.unit);
  };
  return (
    <FormFrame save={save} onSaved={onSaved} saveWords="Record stock in" ready={n > 0}>
      <p className="tk-form-now">Now: {item.stock === null ? "no count" : quantity(item.stock, item.unit)}</p>
      <div className="tk-form-row">
        <span className="tk-form-row-title">Came in</span>
        <Stepper label="Came in" value={n} onChange={setN} />
      </div>
      <KitField label="Note" hint="Who brought it, a delivery note…">
        <input value={note} onChange={(e) => setNote(e.target.value)} />
      </KitField>
    </FormFrame>
  );
}

/** A new service: its name, how long it takes and its price. */
export function ServiceAdd({ onSaved }: { onSaved: (line: string | null) => void }) {
  const sf = useLoad(() => get<{ services_table?: string | null; services_columns?: ServiceColumns }>("/v1/storefront"), []);
  const [name, setName] = useState("");
  const [minutes, setMinutes] = useState(30);
  const [price, setPrice] = useState("");
  if (!sf.data) return sf.error ? <ErrorBox error={sf.error} /> : <KitLoading />;
  const table = sf.data.services_table;
  if (!table)
    return (
      <KitEmpty icon="clock" title="Services are not set up yet">
        Open services to choose where your services are kept.
      </KitEmpty>
    );
  const save: Saver = async () => {
    const made = serviceRow(sf.data?.services_columns ?? {}, name, String(minutes), price);
    if ("error" in made) throw new Error(made.error);
    await post(`/v1/tables/${table}/rows`, { row: made.row });
    await post("/v1/storefront/catalogue/refresh");
    const kobo = nairaKobo(price);
    return serviceLine(name.trim(), minutes, Number.isNaN(kobo) ? null : kobo);
  };
  return (
    <FormFrame save={save} onSaved={onSaved} saveWords="Add service" ready={name.trim() !== "" && minutes > 0}>
      <KitField label="Name">
        <input value={name} onChange={(e) => setName(e.target.value)} placeholder="Rice milling, per bag" />
      </KitField>
      <div className="tk-form-row">
        <span>
          <span className="tk-form-row-title">Takes</span>
          <span className="tk-form-row-sub">Minutes</span>
        </span>
        <Stepper label="Minutes it takes" value={minutes} onChange={setMinutes} />
      </div>
      <Money label="Price" value={price} onChange={setPrice} />
    </FormFrame>
  );
}

/** A service, as it is (changes are on its page). */
export function ServiceView({ service }: { service: Service }) {
  return (
    <div className="tk-sheet-list tk-form-body">
      <p className="tk-form-now">
        {service.price_kobo !== undefined && service.price_kobo !== null ? formatPrice(service.price_kobo) : "Price on the day"}
        {service.duration_minutes ? ` · ${service.duration_minutes} min` : ""}
      </p>
      {service.description && <p>{service.description}</p>}
    </div>
  );
}

/** A price list's prices. */
export function PriceListForm({ list, onSaved }: { list: PriceList; onSaved: (line: string | null) => void }) {
  const [prices, setPrices] = useState(() => list.items.map((it) => nairaText(it.price_kobo)));
  const bad = prices.some((p) => !(nairaKobo(p) > 0));
  const save: Saver = async () => {
    const changes: PriceChange[] = [];
    const items = list.items.map((it, i) => {
      const to = nairaKobo(prices[i] ?? "");
      if (to !== it.price_kobo) changes.push({ name: it.name, from: it.price_kobo, to });
      return { item_id: it.item_id, price_kobo: to, min_quantity: it.min_quantity, available: it.available };
    });
    if (changes.length === 0) return null;
    await put(`/v1/price-lists/${list.id}/items`, { items });
    return priceListLine(list.name, changes);
  };
  if (list.items.length === 0)
    return <KitEmpty icon="tag" title="Nothing on this list yet">Open price lists to add items to it.</KitEmpty>;
  return (
    <FormFrame save={save} onSaved={onSaved} ready={!bad}>
      {list.items.map((it, i) => (
        <Money
          key={it.item_id}
          label={`${it.name}${it.unit ? `, a ${it.unit}` : ""}`}
          value={prices[i] ?? ""}
          onChange={(v) => setPrices((cur) => cur.map((p, j) => (j === i ? v : p)))}
        />
      ))}
    </FormFrame>
  );
}

const STATUS: Record<string, string> = { received: "New", changed: "Changed", accepted: "Accepted", declined: "Declined" };

/** An order from a linked business: what it asks for, Accept or Decline. */
export function OrderView({ order, onSaved }: { order: TradeOrder; onSaved: (line: string | null) => void }) {
  const { confirm, node } = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const open = order.status === "received" || order.status === "changed";
  const step = async (s: "accept" | "decline") => {
    if (s === "decline" && !(await confirm({ title: `Decline ${order.order_reference}?`, ok: "Decline", danger: true }))) return;
    setBusy(true);
    try {
      await post(`/v1/trade-orders/${encodeURIComponent(order.id)}/${s}`);
      onSaved(orderLine(s, order.order_reference, order.buyer_name));
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        <p className="tk-form-now">
          {order.buyer_name} · <Pill tone={order.status === "received" ? "lime" : "plain"}>{STATUS[order.status] ?? order.status}</Pill>
        </p>
        <ul className="tk-lines">
          {order.lines.map((l, i) => (
            <li key={i}>
              <span>
                {l.quantity} × {l.item}
              </span>
              <b>{formatPrice(l.quantity * (l.price_kobo ?? 0))}</b>
            </li>
          ))}
          <li className="tk-lines-total">
            <span>Total</span>
            <b>{formatPrice(order.total_kobo)}</b>
          </li>
        </ul>
        {order.note && <p className="tk-form-now">Their note: “{order.note}”</p>}
        <ErrorBox error={error} />
      </div>
      {open && (
        <div className="tk-sheet-foot tk-foot-two">
          <button type="button" className="tk-btn ghost" disabled={busy} onClick={() => void step("decline")}>
            Decline
          </button>
          <button type="button" className="tk-cta" disabled={busy} onClick={() => void step("accept")}>
            Accept
          </button>
        </div>
      )}
      {node}
    </>
  );
}

/** Any data table: its latest rows, and a row added. */
export function TableTool({ table, onSaved }: { table: Table; onSaved: (line: string | null) => void }) {
  const rows = useLoad(() => get<{ rows: Row[] }>(`/v1/tables/${encodeURIComponent(table.id)}/rows?limit=20`), [table.id]);
  const [adding, setAdding] = useState(false);
  const [values, setValues] = useState<Record<string, string>>({});
  const cols = table.columns.slice(0, 3);
  if (adding) {
    const save: Saver = async () => {
      const row = Object.fromEntries(
        table.columns.filter((c) => (values[c.name] ?? "") !== "").map((c) => [c.name, cellValue(c.type, values[c.name] ?? "")]),
      );
      await post(`/v1/tables/${encodeURIComponent(table.id)}/rows`, { row });
      return rowLine(table.title || table.name);
    };
    const missing = table.columns.some((c) => c.required && (values[c.name] ?? "").trim() === "");
    return (
      <FormFrame save={save} onSaved={onSaved} saveWords="Add the row" ready={!missing}>
        {table.columns.map((c) => (
          <KitField key={c.name} label={`${c.title || c.name}${c.required ? "" : " (optional)"}`}>
            <input value={values[c.name] ?? ""} onChange={(e) => setValues({ ...values, [c.name]: e.target.value })} />
          </KitField>
        ))}
      </FormFrame>
    );
  }
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        {!rows.data ? (
          rows.error ? <ErrorBox error={rows.error} /> : <KitLoading />
        ) : rows.data.rows.length === 0 ? (
          <KitEmpty icon="tables" title="No rows yet" />
        ) : (
          <ul className="tk-table-rows">
            {rows.data.rows.map((r, i) => (
              <li key={String(r.id ?? i)}>
                {cols.map((c) => (
                  <span key={c.name}>
                    <span className="tk-cell-name">{c.title || c.name}</span> {cellText(r[c.name])}
                  </span>
                ))}
              </li>
            ))}
          </ul>
        )}
      </div>
      <div className="tk-sheet-foot">
        <button type="button" className="tk-cta" onClick={() => setAdding(true)}>
          Add a row
        </button>
      </div>
    </>
  );
}
