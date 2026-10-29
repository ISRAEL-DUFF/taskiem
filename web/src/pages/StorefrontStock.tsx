import { useState } from "react";
import { Link } from "react-router-dom";
import { get, patch, post, postOnce } from "../api";
import { Icon } from "../icons";
import {
  EmptyState,
  ErrorBox,
  Field,
  Items,
  fmtTime,
  Modal,
  PageHeader,
  Row,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";
import {
  PAID_HOW,
  REASON,
  addRequest,
  byHowLine,
  emptySale,
  saleProblem,
  saleRequest,
  saleSub,
  saleTitle,
  saleTotal,
  unsoldSale,
  type PaidHow,
  type SaleForm,
  type SalesDay,
  type UnsoldEntry,
  changeRequest,
  emptyItem,
  itemForm,
  itemProblem,
  itemSub,
  movePreview,
  moveProblem,
  moveRequest,
  movementSub,
  movementTitle,
  stockLine,
  type ItemForm,
  type MoveForm,
  type Movement,
  type MovementReason,
  type StockItem,
  type StockView,
} from "../lib/stock";
import { formatPrice } from "../lib/storefront";
import { PurchasesCard } from "./StockPurchases";
import { StorefrontTabs } from "./Storefront";

/** A shop's stock (docs/stock.md): its items and how many are left, the
 * low ones first, and every change kept as a movement. */
export function StorefrontStock() {
  const view = useLoad(() => get<StockView>("/v1/stock"), []);
  const [item, setItem] = useState("");
  const [reason, setReason] = useState<MovementReason | "">("");
  const moves = useLoad(
    () =>
      get<{ movements: Movement[] }>(
        `/v1/stock/movements?limit=50${item ? `&item=${encodeURIComponent(item)}` : ""}${reason ? `&reason=${reason}` : ""}`,
      ),
    [item, reason],
  );
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<StockItem | null>(null);
  const [moving, setMoving] = useState<StockItem | null>(null);
  const [selling, setSelling] = useState<{ entry?: UnsoldEntry } | null>(null);
  const act = useAction();
  const v = view.data;
  const sales = useLoad(
    () =>
      v?.keeps_stock ? get<SalesDay>("/v1/stock/sales") : Promise.resolve(null),
    [v?.keeps_stock],
  );
  const reload = () => {
    view.reload();
    moves.reload();
    sales.reload();
  };
  const low = v?.items.filter((i) => i.low) ?? [];
  const stuck = v?.stuck?.length ?? 0;
  return (
    <>
      <PageHeader
        title="Stock"
        description="Your items and how many are left. Every change is kept, so a count can always be explained."
        actions={
          v?.keeps_stock && (
            <button
              type="button"
              className="primary"
              onClick={() => setAdding(true)}
            >
              Add items
            </button>
          )
        }
      />
      <StorefrontTabs />
      <ErrorBox error={view.error ?? act.error} />
      {!v && !view.error && <Skeleton rows={4} label="Loading your stock…" />}
      {v && !v.keeps_stock && (
        <section className="card" aria-labelledby="stock-start">
          <h2 id="stock-start" style={{ marginTop: 0 }}>
            Keep your stock here
          </h2>
          <p className="hint">
            {v.catalogue
              ? `Taskiem keeps the count in your ${v.catalogue} table, adding the columns it needs: stock, unit, cost price, reorder level and supplier.`
              : "Taskiem makes a products table for your items and keeps the count there. You need no page for it."}{" "}
            On WhatsApp, send{" "}
            <strong>add 50 cartons of Indomie at 8,500</strong>.
          </p>
          <div className="toolbar">
            <button
              type="button"
              className="primary"
              disabled={act.busy}
              onClick={() =>
                void act.run(
                  async () => (await post("/v1/stock/setup"), reload()),
                  "Stock is on",
                )
              }
            >
              Keep my stock
            </button>
          </div>
        </section>
      )}
      {v?.keeps_stock && (
        <>
          <ErrorBox error={sales.error} />
          {sales.data && (
            <SalesToday
              day={sales.data}
              onSell={(entry) => setSelling({ entry })}
              canSell={v.items.length > 0}
            />
          )}
          {stuck > 0 && (
            <div className="notice" role="status">
              {stuck === 1 ? "1 change has" : `${stuck} changes have`} not
              reached your {v.catalogue} table yet. Taskiem sends{" "}
              {stuck === 1 ? "it" : "them"} again by itself.
            </div>
          )}
          <section className="card" aria-labelledby="stock-low">
            <h2 id="stock-low" style={{ marginTop: 0 }}>
              Low on stock
            </h2>
            {low.length === 0 ? (
              <p className="hint">
                {v.items.some((i) => i.reorder_level !== undefined)
                  ? "Nothing is at or below its reorder level."
                  : "Give your items a reorder level, and they show here when they run low."}
              </p>
            ) : (
              <Items label="Low on stock">
                {low.map((it) => (
                  <Row
                    key={it.id}
                    letter={it.name}
                    title={it.name}
                    side={<span className="badge warn">{stockLine(it)}</span>}
                    sub={<span className="sub">{itemSub(it)}</span>}
                    testid="low-item"
                  >
                    <button
                      type="button"
                      className="small"
                      onClick={() => setMoving(it)}
                    >
                      Record stock<span className="sr-only"> of {it.name}</span>
                    </button>
                  </Row>
                ))}
              </Items>
            )}
          </section>
          <PurchasesCard onChanged={reload} />
          <section className="card" aria-labelledby="stock-items">
            <h2 id="stock-items" style={{ marginTop: 0 }}>
              Your items
            </h2>
            {v.items.length === 0 ? (
              <EmptyState icon="tables">
                No items yet. Add what you sell, and how many you have.
              </EmptyState>
            ) : (
              <Items label="Your items">
                {v.items.map((it) => (
                  <Row
                    key={it.id}
                    letter={it.name}
                    title={it.name}
                    side={
                      <span
                        className={`badge ${it.low ? "warn" : it.stock === null ? "muted" : "ok"}`}
                      >
                        {stockLine(it)}
                      </span>
                    }
                    sub={<span className="sub">{itemSub(it)}</span>}
                    testid="stock-item"
                  >
                    <button
                      type="button"
                      className="small"
                      onClick={() => setMoving(it)}
                    >
                      Record stock<span className="sr-only"> of {it.name}</span>
                    </button>
                    <button
                      type="button"
                      className="small"
                      onClick={() => setEditing(it)}
                    >
                      Change<span className="sr-only"> {it.name}</span>
                    </button>
                    <button
                      type="button"
                      className="small"
                      onClick={() => setItem(it.id)}
                    >
                      History<span className="sr-only"> of {it.name}</span>
                    </button>
                  </Row>
                ))}
              </Items>
            )}
          </section>
          <section className="card" aria-labelledby="stock-moves">
            <div className="toolbar" style={{ marginBottom: 6 }}>
              <h2 id="stock-moves" className="grow" style={{ margin: 0 }}>
                {item
                  ? `${v.items.find((i) => i.id === item)?.name ?? "The item"}: every change`
                  : "Every change"}
              </h2>
              <label className="sr-only" htmlFor="stock-reason">
                Why
              </label>
              <select
                id="stock-reason"
                value={reason}
                onChange={(e) =>
                  setReason(e.target.value as MovementReason | "")
                }
              >
                <option value="">Every reason</option>
                {(Object.keys(REASON) as MovementReason[]).map((r) => (
                  <option key={r} value={r}>
                    {REASON[r]}
                  </option>
                ))}
              </select>
              {item && (
                <button
                  type="button"
                  className="small"
                  onClick={() => setItem("")}
                >
                  Every item
                </button>
              )}
            </div>
            <ErrorBox error={moves.error} />
            {moves.data && moves.data.movements.length === 0 && (
              <p className="hint">No changes yet.</p>
            )}
            {moves.data && moves.data.movements.length > 0 && (
              <Items label="Every change">
                {moves.data.movements.map((m) => (
                  <Row
                    key={m.id}
                    letter={m.item}
                    title={movementTitle(m)}
                    side={
                      <span
                        className={`badge ${m.status === "pending" ? "warn" : m.reason === "outside" ? "info" : "muted"}`}
                      >
                        {REASON[m.reason]}
                      </span>
                    }
                    sub={<span className="sub">{movementSub(m)}</span>}
                    testid="movement"
                  />
                ))}
              </Items>
            )}
          </section>
        </>
      )}
      {adding && (
        <ItemModal
          item={null}
          onClose={() => setAdding(false)}
          onSaved={() => {
            setAdding(false);
            reload();
          }}
        />
      )}
      {editing && (
        <ItemModal
          item={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
      {selling && v && (
        <SaleModal
          items={v.items}
          entry={selling.entry}
          onClose={() => setSelling(null)}
          onSaved={() => {
            setSelling(null);
            reload();
          }}
        />
      )}
      {moving && (
        <MoveModal
          item={moving}
          onClose={() => setMoving(null)}
          onSaved={() => {
            setMoving(null);
            reload();
          }}
        />
      )}
    </>
  );
}

/** Today's sales (docs/stock.md "Sales"): the total in a pine card with
 * Record a sale, the terminal's sales with no items, then each sale. */
function SalesToday({
  day,
  onSell,
  canSell,
}: {
  day: SalesDay;
  onSell: (entry?: UnsoldEntry) => void;
  canSell: boolean;
}) {
  const by = byHowLine(day);
  return (
    <>
      <section
        className="hero"
        aria-label="Today's sales"
        data-testid="sales-hero"
      >
        <div className="hero-top">
          <span className="hero-label">Today's sales</span>
          {day.waiting > 0 && (
            <span className="hero-tag">{day.waiting} waiting for money</span>
          )}
        </div>
        <div className="hero-amount">{formatPrice(day.total_kobo)}</div>
        <p className="hero-sub">
          {day.count === 0
            ? "No sales recorded today."
            : `${day.count === 1 ? "1 sale" : `${day.count} sales`}${by ? ` · ${by}` : ""}`}
        </p>
        <div className="toolbar">
          <button
            type="button"
            className="primary"
            disabled={!canSell}
            onClick={() => onSell()}
          >
            Record a sale
          </button>
        </div>
      </section>
      {day.unsold.length > 0 && (
        <section className="card" aria-labelledby="sales-unsold">
          <h2 id="sales-unsold" style={{ marginTop: 0 }}>
            Terminal sales with no items
          </h2>
          <p className="hint">
            Your terminal reported these, but nobody said what was sold. Say it,
            and the items come off your stock.
          </p>
          <Items label="Terminal sales with no items">
            {day.unsold.map((u) => (
              <Row
                key={u.entry_id}
                letter="₦"
                title={formatPrice(u.amount_kobo)}
                sub={
                  <span className="sub">On the terminal · {fmtTime(u.at)}</span>
                }
                testid="unsold"
              >
                <button
                  type="button"
                  className="small"
                  disabled={!canSell}
                  onClick={() => onSell(u)}
                >
                  Say what was sold
                  <span className="sr-only">
                    {" "}
                    for {formatPrice(u.amount_kobo)}
                  </span>
                </button>
              </Row>
            ))}
          </Items>
        </section>
      )}
      {day.sales.length > 0 && (
        <section className="card" aria-labelledby="sales-list">
          <h2 id="sales-list" style={{ marginTop: 0 }}>
            Sold today
          </h2>
          <Items label="Sold today">
            {day.sales.map((x) => (
              <Row
                key={x.id}
                letter={x.lines[0]?.item ?? "₦"}
                title={saleTitle(x)}
                side={
                  <span
                    className={`badge ${x.status === "waiting" ? "warn" : "ok"}`}
                  >
                    {formatPrice(x.amount_kobo)}
                  </span>
                }
                sub={<span className="sub">{saleSub(x)}</span>}
                testid="sale"
              />
            ))}
          </Items>
        </section>
      )}
    </>
  );
}

/** Record a sale: the items, how many, how it was paid; or say what a
 * terminal's sale was. */
function SaleModal({
  items,
  entry,
  onClose,
  onSaved,
}: {
  items: StockItem[];
  entry?: UnsoldEntry;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [f, setF] = useState<SaleForm>(() =>
    entry ? unsoldSale(entry) : emptySale(),
  );
  const [key] = useState(() => crypto.randomUUID());
  const act = useAction();
  const set = (p: Partial<SaleForm>) => setF((x) => ({ ...x, ...p }));
  const setLine = (i: number, p: Partial<SaleForm["lines"][number]>) =>
    setF((x) => ({
      ...x,
      lines: x.lines.map((l, j) => (j === i ? { ...l, ...p } : l)),
    }));
  const problem = saleProblem(items, f);
  const total = saleTotal(items, f);
  const touched = f.lines.some((l) => l.item_id !== "");
  return (
    <Modal
      title={
        entry
          ? `What was sold for ${formatPrice(entry.amount_kobo)}`
          : "Record a sale"
      }
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await postOnce("/v1/stock/sales", key, saleRequest(f, entry)),
              onSaved()
            ),
            "Sale recorded",
          );
        }}
      >
        {f.lines.map((l, i) => {
          const it = items.find((x) => x.id === l.item_id);
          return (
            <fieldset key={i} className="sf-choices" data-testid="sale-line">
              <legend>{f.lines.length > 1 ? `Item ${i + 1}` : "Item"}</legend>
              <Field label="What was sold">
                <select
                  value={l.item_id}
                  onChange={(e) => setLine(i, { item_id: e.target.value })}
                >
                  <option value="">Choose an item</option>
                  {items.map((x) => (
                    <option key={x.id} value={x.id}>
                      {x.name} ({stockLine(x)})
                    </option>
                  ))}
                </select>
              </Field>
              <Field label="How many">
                <input
                  value={l.quantity}
                  inputMode="numeric"
                  onChange={(e) => setLine(i, { quantity: e.target.value })}
                />
              </Field>
              <Field
                label="Price for one (₦)"
                hint={
                  it?.price_kobo !== undefined
                    ? `Leave it empty for ${formatPrice(it.price_kobo)}.`
                    : undefined
                }
              >
                <input
                  value={l.price}
                  inputMode="decimal"
                  placeholder={
                    it?.price_kobo !== undefined
                      ? String(it.price_kobo / 100)
                      : ""
                  }
                  onChange={(e) => setLine(i, { price: e.target.value })}
                />
              </Field>
              {f.lines.length > 1 && (
                <button
                  type="button"
                  className="small"
                  onClick={() =>
                    set({ lines: f.lines.filter((_, j) => j !== i) })
                  }
                >
                  Take this item off<span className="sr-only"> {i + 1}</span>
                </button>
              )}
            </fieldset>
          );
        })}
        <div className="toolbar">
          <button
            type="button"
            className="small"
            onClick={() =>
              set({
                lines: [...f.lines, { item_id: "", quantity: "1", price: "" }],
              })
            }
          >
            Add another item
          </button>
        </div>
        {!entry && (
          <>
            <fieldset className="sf-choices">
              <legend>How it was paid</legend>
              {(Object.keys(PAID_HOW) as PaidHow[]).map((h) => (
                <label key={h} className="sf-radio">
                  <input
                    type="radio"
                    name="how"
                    checked={f.how === h}
                    onChange={() => set({ how: h })}
                  />
                  {PAID_HOW[h]}
                </label>
              ))}
            </fieldset>
            <Field
              label="Amount taken (₦)"
              hint={
                total !== null
                  ? `Leave it empty for ${formatPrice(total)}, the items' prices.`
                  : "Needed when an item has no price."
              }
            >
              <input
                value={f.amount}
                inputMode="decimal"
                onChange={(e) => set({ amount: e.target.value })}
              />
            </Field>
          </>
        )}
        <Field label="Note">
          <input
            value={f.note}
            maxLength={200}
            onChange={(e) => set({ note: e.target.value })}
          />
        </Field>
        {!entry && f.how !== "cash" && (
          <p className="hint">
            The sale waits for its money, and is matched when the{" "}
            {f.how === "pos" ? "terminal reports it" : "transfer comes in"}.
          </p>
        )}
        {problem && touched && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
            Record the sale
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Add an item with how many there are, or change one's name, unit, prices, levels and supplier. */
function ItemModal({
  item,
  onClose,
  onSaved,
}: {
  item: StockItem | null;
  onClose: () => void;
  onSaved: () => void;
}) {
  const adding = item === null;
  const [f, setF] = useState<ItemForm>(() =>
    item ? itemForm(item) : emptyItem(),
  );
  const [key] = useState(() => crypto.randomUUID());
  const act = useAction();
  const set = (p: Partial<ItemForm>) => setF((x) => ({ ...x, ...p }));
  const problem = itemProblem(f, adding);
  const save = () =>
    act.run(
      async () => {
        if (adding) await postOnce("/v1/stock/items", key, addRequest(f));
        else await patch(`/v1/stock/items/${item.id}`, changeRequest(item, f));
        onSaved();
      },
      adding ? `${f.name.trim()} added` : "Saved",
    );
  return (
    <Modal
      title={adding ? "Add an item" : `Change ${item.name}`}
      onClose={onClose}
    >
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void save();
        }}
      >
        <Field label="Name">
          <input
            value={f.name}
            maxLength={120}
            onChange={(e) => set({ name: e.target.value })}
            autoFocus
            autoComplete="off"
          />
        </Field>
        {adding && (
          <Field
            label="How many you have"
            hint="Recorded as goods received, so the count is explained from the start."
          >
            <input
              value={f.quantity}
              inputMode="numeric"
              placeholder="50"
              onChange={(e) => set({ quantity: e.target.value })}
            />
          </Field>
        )}
        <Field
          label="Unit"
          hint="One word: bag, carton, piece. Leave it empty to count pieces."
        >
          <input
            value={f.unit}
            maxLength={30}
            placeholder="carton"
            onChange={(e) => set({ unit: e.target.value })}
          />
        </Field>
        <Field
          label="Cost price (₦)"
          hint="What you pay for one, for the value of your stock and what you lose."
        >
          <input
            value={f.cost}
            inputMode="decimal"
            placeholder="8500"
            onChange={(e) => set({ cost: e.target.value })}
          />
        </Field>
        <Field
          label="Selling price (₦)"
          hint="Shown on your page once it is published. Without one, the item is not shown."
        >
          <input
            value={f.price}
            inputMode="decimal"
            placeholder="9500"
            onChange={(e) => set({ price: e.target.value })}
          />
        </Field>
        <Field
          label="Reorder at"
          hint="When the count is this or less, the item is low and you are told each morning."
        >
          <input
            value={f.reorder}
            inputMode="numeric"
            placeholder="10"
            onChange={(e) => set({ reorder: e.target.value })}
          />
        </Field>
        <Field label="Order up to" hint="What to order a low item back up to.">
          <input
            value={f.target}
            inputMode="numeric"
            placeholder="50"
            onChange={(e) => set({ target: e.target.value })}
          />
        </Field>
        <Field label="Supplier">
          <input
            value={f.supplier}
            maxLength={120}
            placeholder="Dangote Depot"
            onChange={(e) => set({ supplier: e.target.value })}
          />
        </Field>
        {problem && f.name !== "" && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
            {adding ? "Add the item" : "Save"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Goods received, an adjustment with a reason, or a count. */
function MoveModal({
  item,
  onClose,
  onSaved,
}: {
  item: StockItem;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [f, setF] = useState<MoveForm>({
    kind: "received",
    amount: "",
    off: true,
    note: "",
  });
  const [key] = useState(() => crypto.randomUUID());
  const act = useAction();
  const set = (p: Partial<MoveForm>) => setF((x) => ({ ...x, ...p }));
  const problem = moveProblem(f);
  const preview = movePreview(item, f);
  return (
    <Modal title={`Record stock of ${item.name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await postOnce(
                `/v1/stock/movements`,
                `${key}-${f.kind}`,
                moveRequest(item, f),
              ),
              onSaved()
            ),
            "Recorded",
          );
        }}
      >
        <p className="hint">Now: {stockLine(item)}.</p>
        <fieldset className="sf-choices">
          <legend>What happened</legend>
          {(
            [
              ["received", "Goods came in"],
              ["adjustment", "Some were spoilt, lost or found"],
              ["count", "I counted what is on the shelf"],
            ] as const
          ).map(([k, label]) => (
            <label key={k} className="sf-radio">
              <input
                type="radio"
                name="kind"
                checked={f.kind === k}
                onChange={() => set({ kind: k })}
              />
              {label}
            </label>
          ))}
        </fieldset>
        {f.kind === "adjustment" && (
          <fieldset className="sf-choices">
            <legend>Take off or add</legend>
            <label className="sf-radio">
              <input
                type="radio"
                name="off"
                checked={f.off}
                onChange={() => set({ off: true })}
              />
              Take them off
            </label>
            <label className="sf-radio">
              <input
                type="radio"
                name="off"
                checked={!f.off}
                onChange={() => set({ off: false })}
              />
              Add them
            </label>
          </fieldset>
        )}
        <Field label={f.kind === "count" ? "How many you counted" : "How many"}>
          <input
            value={f.amount}
            inputMode="numeric"
            placeholder={f.kind === "count" ? "12" : "5"}
            onChange={(e) => set({ amount: e.target.value })}
            autoFocus
          />
        </Field>
        <Field
          label={f.kind === "adjustment" ? "Why" : "Note"}
          hint={
            f.kind === "adjustment" ? "Spoilt, lost, found, a gift." : undefined
          }
        >
          <input
            value={f.note}
            maxLength={200}
            onChange={(e) => set({ note: e.target.value })}
          />
        </Field>
        {preview && !problem && (
          <p className="hint">The count goes from {preview}.</p>
        )}
        {problem && f.amount !== "" && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
            Record
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Get started's offer (docs/stock.md): Keep my stock, to a business with
 * no items keeping stock yet, as it offers a page. */
export function StockOffer() {
  const view = useLoad(() => get<StockView>("/v1/stock"), []);
  const v = view.data;
  if (!v || (v.keeps_stock && v.items.length > 0)) return null;
  return (
    <section aria-labelledby="stock-offer">
      <h2 id="stock-offer">Keep my stock</h2>
      <ul className="checklist">
        <li data-testid="first-stock">
          <span className="list-icon">
            <Icon name="tables" />
          </span>
          <div className="grow">
            <strong>Know what is on your shelf</strong>
            <div className="hint">
              Taskiem keeps how many of each item you have, and tells you each
              morning what is low. On WhatsApp, send add 50 cartons of Indomie
              at 8,500.
            </div>
          </div>
          <Link className="button small" to="/storefront/stock">
            Keep my stock
          </Link>
        </li>
      </ul>
    </section>
  );
}
