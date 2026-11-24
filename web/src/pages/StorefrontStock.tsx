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
  SHOW,
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
  itemProblem,
  movePreview,
  moveProblem,
  moveRequest,
  movementSub,
  movementTitle,
  quantity,
  rowCount,
  rowCounted,
  rowEdit,
  rowForm,
  rowSub,
  soldAsLine,
  stockLine,
  stockList,
  worthLine,
  type Counted,
  type ItemForm,
  type MoveForm,
  type Movement,
  type MovementReason,
  type StockItem,
  type StockItemRow,
  type StockItemsView,
  type StockShow,
  type StockView,
} from "../lib/stock";
import { formatPrice } from "../lib/storefront";
import { THINGS_PATH, thingsCount, type ThingsView } from "../lib/things";
import { naira } from "../lib/invoices";
import { useAuth } from "../auth";
import { sizesWords } from "../lib/variants";
import { CountsCard } from "./StockCounts";
import { PurchasesCard } from "./StockPurchases";
import { StorefrontTabs } from "./Storefront";

/** A shop's stock (docs/stock.md; what a business buys §4.5): what it
 * counts, each stock item with what it is sold as (or Not sold), the low
 * ones first, and every change kept as a movement. */
export function StorefrontStock() {
  const view = useLoad(() => get<StockView>("/v1/stock"), []);
  const v = view.data;
  const [show, setShow] = useState<StockShow>("all");
  const counted = useLoad(
    () =>
      v?.keeps_stock
        ? get<StockItemsView>(`/v1/stock/stock-items?show=${show}`)
        : Promise.resolve(null),
    [v?.keeps_stock, show],
  );
  // The low card is of everything counted, whatever the filter shows.
  const everything = useLoad(
    () =>
      v?.keeps_stock && show !== "all"
        ? get<StockItemsView>("/v1/stock/stock-items?show=all")
        : Promise.resolve(null),
    [v?.keeps_stock, show],
  );
  const all = show === "all" ? counted.data : everything.data;
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
  const [editing, setEditing] = useState<StockItemRow | null>(null);
  const [moving, setMoving] = useState<StockItemRow | null>(null);
  const [selling, setSelling] = useState<{ entry?: UnsoldEntry } | null>(null);
  const act = useAction();
  const sales = useLoad(
    () =>
      v?.keeps_stock ? get<SalesDay>("/v1/stock/sales") : Promise.resolve(null),
    [v?.keeps_stock],
  );
  const reload = () => {
    view.reload();
    counted.reload();
    everything.reload();
    moves.reload();
    sales.reload();
  };
  const low = all?.items.filter((r) => r.low) ?? [];
  // What can be sold: products, not materials.
  const sellable = v?.items.filter((i) => !i.material) ?? [];
  const named = (id: string) =>
    all?.items.find((r) => r.id === id)?.name ??
    counted.data?.items.find((r) => r.id === id)?.name ??
    "The item";
  return (
    <>
      <PageHeader
        title="Stock"
        description="What you count and how many are left: what you sell, and what you use. Every change is kept, so a count can always be explained."
        actions={
          v?.keeps_stock && (
            <button
              type="button"
              className="primary"
              onClick={() => setAdding(true)}
            >
              Add something to count
            </button>
          )
        }
      />
      <StorefrontTabs />
      <ErrorBox error={view.error} />
      {!v && !view.error && <Skeleton rows={4} label="Loading your stock…" />}
      {v && !v.keeps_stock && (
        <section className="card" aria-labelledby="stock-start">
          <h2 id="stock-start" style={{ marginTop: 0 }}>
            Keep your stock here
          </h2>
          <p className="hint">
            {v.catalogue
              ? `Taskiem counts your items itself, each change with its reason. Your ${v.catalogue} table keeps what you sell and its prices.`
              : "Taskiem makes a products table for what you sell, and counts your items itself. You need no page for it."}{" "}
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
          {/* Beside the button that was pressed, where the person is looking. */}
          <ErrorBox error={act.error} />
        </section>
      )}
      {v?.keeps_stock && (
        <>
          <ErrorBox error={sales.error} />
          {sales.data && (
            <SalesToday
              day={sales.data}
              onSell={(entry) => setSelling({ entry })}
              canSell={sellable.length > 0}
            />
          )}
          <section className="card" aria-labelledby="stock-low">
            <h2 id="stock-low" style={{ marginTop: 0 }}>
              Low on stock
            </h2>
            {low.length === 0 ? (
              <p className="hint">
                {all?.items.some((r) => r.low_level !== null)
                  ? "Nothing is at or below its reorder level."
                  : "Give your items a reorder level, and they show here when they run low."}
              </p>
            ) : (
              <Items label="Low on stock">
                {low.map((r) => (
                  <Row
                    key={r.id}
                    letter={r.name}
                    title={r.name}
                    side={<span className="badge warn">{rowCount(r)}</span>}
                    sub={<span className="sub">{rowSub(r)}</span>}
                    testid="low-item"
                  >
                    <button
                      type="button"
                      className="small"
                      onClick={() => setMoving(r)}
                    >
                      Record stock<span className="sr-only"> of {r.name}</span>
                    </button>
                  </Row>
                ))}
              </Items>
            )}
          </section>
          <PurchasesCard onChanged={reload} />
          <CountsCard items={v.items} onChanged={reload} />
          <ThingsCard />
          <section className="card" aria-labelledby="stock-items">
            <h2 id="stock-items" style={{ marginTop: 0 }}>
              What you count
            </h2>
            <div className="period-bar">
              <div className="segmented" role="group" aria-label="Show">
                {(Object.keys(SHOW) as StockShow[]).map((s) => (
                  <button
                    key={s}
                    type="button"
                    aria-pressed={show === s}
                    onClick={() => setShow(s)}
                  >
                    {SHOW[s]}
                  </button>
                ))}
              </div>
            </div>
            <ErrorBox error={counted.error} />
            {!counted.data && !counted.error && (
              <Skeleton rows={3} label="Loading what you count…" />
            )}
            {counted.data && counted.data.items.length === 0 && (
              <EmptyState icon="tables">
                {show === "sold"
                  ? "Nothing you sell is counted yet."
                  : show === "not_sold"
                    ? "Nothing counted that you don't sell. Fabric, thread, packaging: add them and answer No to Do you sell it?"
                    : "Nothing counted yet. Add what you sell, or what you use, and how many you have."}
              </EmptyState>
            )}
            {counted.data && counted.data.items.length > 0 && (
              <>
                <p className="hint" data-testid="stock-worth">
                  {worthLine(counted.data, show)}
                </p>
                <Items label="What you count">
                  {stockList(counted.data.items).map((x) =>
                    x.kind === "group" ? (
                      <Row
                        key={`g-${x.id}`}
                        letter={x.name}
                        title={x.name}
                        side={
                          <span className={`badge ${x.low ? "warn" : "ok"}`}>
                            {`${sizesWords(x.sizes)} · ${quantity(x.count)} in stock${x.low ? ` · ${x.low} low` : ""}`}
                          </span>
                        }
                        sub={
                          <span className="sub">
                            Each size and colour is counted on its own, below.
                          </span>
                        }
                        testid="stock-group"
                      />
                    ) : (
                      <Row
                        key={x.row.id}
                        letter={x.group ? undefined : x.row.name}
                        icon={x.group ? "tables" : undefined}
                        title={x.row.name}
                        side={
                          <span
                            className={`badge ${x.row.low ? "warn" : "ok"}`}
                          >
                            {rowCount(x.row)}
                          </span>
                        }
                        sub={
                          <span className="sub">
                            <span className="block" data-testid="sold-as">
                              {x.group ? `Of ${x.group} · ` : ""}
                              {soldAsLine(x.row)}
                            </span>
                            <span className="block">{rowSub(x.row)}</span>
                          </span>
                        }
                        testid="stock-row"
                      >
                        <button
                          type="button"
                          className="small"
                          onClick={() => setMoving(x.row)}
                        >
                          Record stock
                          <span className="sr-only"> of {x.row.name}</span>
                        </button>
                        <button
                          type="button"
                          className="small"
                          onClick={() => setEditing(x.row)}
                        >
                          Change<span className="sr-only"> {x.row.name}</span>
                        </button>
                        <button
                          type="button"
                          className="small"
                          onClick={() => setItem(x.row.id)}
                        >
                          History<span className="sr-only"> of {x.row.name}</span>
                        </button>
                      </Row>
                    ),
                  )}
                </Items>
              </>
            )}
          </section>
          <section className="card" aria-labelledby="stock-moves">
            <div className="toolbar" style={{ marginBottom: 6 }}>
              <h2 id="stock-moves" className="grow" style={{ margin: 0 }}>
                {item ? `${named(item)}: every change` : "Every change"}
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
                    side={<span className="badge muted">{REASON[m.reason]}</span>}
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
        <AddModal
          onClose={() => setAdding(false)}
          onSaved={() => {
            setAdding(false);
            reload();
          }}
        />
      )}
      {editing && (
        <ChangeModal
          row={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            reload();
          }}
        />
      )}
      {selling && v && (
        <SaleModal
          items={sellable}
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
          item={rowCounted(moving)}
          name={moving.name}
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
/** Things we have, from the Stock page (what a business buys §5.3): what
 * the business keeps track of rather than counts, how many and what they
 * cost, for those who may see them (table.read). */
function ThingsCard() {
  const { can } = useAuth();
  const read = can("table.read");
  const view = useLoad(
    () => (read ? get<ThingsView>(THINGS_PATH).catch(() => undefined) : Promise.resolve(undefined)),
    [read],
  );
  if (!read) return null;
  const v = view.data;
  return (
    <section className="card" aria-labelledby="stock-things" data-testid="stock-things">
      <h2 id="stock-things" style={{ marginTop: 0 }}>
        Things we have
      </h2>
      <p className="hint">
        {v
          ? v.count === 0
            ? "What you keep track of rather than count, one row each, like machines and tools. Nothing yet."
            : `${thingsCount(v.count)} you keep track of, one row each, that cost ${naira(v.total_kobo)}.`
          : "What you keep track of rather than count, one row each, like machines and tools."}
      </p>
      <div className="toolbar">
        <Link className="button small" to="/money/things">
          See Things we have
        </Link>
      </div>
    </section>
  );
}

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

/** Add something to count (what a business buys §4.5): its name, then Do
 * you sell it? Yes makes its product too, counted by it (with a selling
 * price, when there is one); No makes a material only. */
function AddModal({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const [f, setF] = useState<ItemForm>(emptyItem);
  const [sells, setSells] = useState<boolean | null>(null);
  const [key] = useState(() => crypto.randomUUID());
  const act = useAction();
  const set = (p: Partial<ItemForm>) => setF((x) => ({ ...x, ...p }));
  const problem =
    itemProblem(sells ? f : { ...f, price: "" }, true) ??
    (sells === null ? "Say whether you sell it." : null);
  return (
    <Modal title="Add something to count" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (sells === null) return;
          void act.run(
            async () => (
              await postOnce("/v1/stock/items", key, addRequest(f, sells)),
              onSaved()
            ),
            sells
              ? `${f.name.trim()} added to your products`
              : `Now counting ${f.name.trim()}`,
          );
        }}
      >
        <Field label="What is it">
          <input
            value={f.name}
            maxLength={120}
            placeholder="Ankara fabric"
            onChange={(e) => set({ name: e.target.value })}
            autoFocus
            autoComplete="off"
          />
        </Field>
        <fieldset className="sf-choices">
          <legend>Do you sell it?</legend>
          <label className="sf-radio">
            <input
              type="radio"
              name="sells"
              checked={sells === true}
              onChange={() => setSells(true)}
            />
            Yes, I sell it
          </label>
          <label className="sf-radio">
            <input
              type="radio"
              name="sells"
              checked={sells === false}
              onChange={() => setSells(false)}
            />
            No, I don't
          </label>
        </fieldset>
        {sells !== null && (
          <p className="hint" data-testid="sells-hint">
            {sells
              ? "It is added to your products too, and selling it lowers its count."
              : "Only counted: it is not added to your products, and customers never see it."}
          </p>
        )}
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
        <Field
          label="Unit"
          hint="One word: yard, bag, carton, piece. Leave it empty to count pieces."
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
        {sells && (
          <Field
            label="Selling price (₦)"
            hint="Shown on your page once it is published. Without one, it is not shown."
          >
            <input
              value={f.price}
              inputMode="decimal"
              placeholder="9500"
              onChange={(e) => set({ price: e.target.value })}
            />
          </Field>
        )}
        <LevelFields f={f} set={set} />
        {problem && f.name !== "" && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
            Add it
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Reorder at, order up to and the supplier, in the add and change forms. */
function LevelFields({
  f,
  set,
}: {
  f: ItemForm;
  set: (p: Partial<ItemForm>) => void;
}) {
  return (
    <>
      <Field
        label="Reorder at"
        hint="When the count is this or less, it is low and you are told each morning."
      >
        <input
          value={f.reorder}
          inputMode="numeric"
          placeholder="10"
          onChange={(e) => set({ reorder: e.target.value })}
        />
      </Field>
      <Field label="Order up to" hint="What to order a low one back up to.">
        <input
          value={f.target}
          inputMode="numeric"
          placeholder="50"
          onChange={(e) => set({ target: e.target.value })}
        />
      </Field>
      <Field label="Supplier" hint="One of your suppliers, by name.">
        <input
          value={f.supplier}
          maxLength={120}
          placeholder="Dangote Depot"
          onChange={(e) => set({ supplier: e.target.value })}
        />
      </Field>
    </>
  );
}

/** Change a stock item: its unit, cost, levels and supplier; its name
 * (a material's, or with its one product's); its selling price when it is
 * sold as one product, one for one. */
function ChangeModal({
  row,
  onClose,
  onSaved,
}: {
  row: StockItemRow;
  onClose: () => void;
  onSaved: () => void;
}) {
  const edit = rowEdit(row);
  const [before] = useState(() => rowForm(row));
  const [f, setF] = useState<ItemForm>(before);
  const act = useAction();
  const set = (p: Partial<ItemForm>) => setF((x) => ({ ...x, ...p }));
  const problem = itemProblem(f, false);
  return (
    <Modal title={`Change ${row.name}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await patch(
                `/v1/stock/items/${encodeURIComponent(edit.id)}`,
                changeRequest(before, f),
              ),
              onSaved()
            ),
            "Saved",
          );
        }}
      >
        <p className="hint">{soldAsLine(row)}.</p>
        {edit.name ? (
          <Field label="Name">
            <input
              value={f.name}
              maxLength={120}
              onChange={(e) => set({ name: e.target.value })}
              autoFocus
              autoComplete="off"
            />
          </Field>
        ) : (
          <p className="hint">
            {row.group_of
              ? `Its name follows ${row.group_name || "its product"}.`
              : "Its products' names and prices are changed in Products."}
          </p>
        )}
        <Field
          label="Unit"
          hint="One word: yard, bag, carton, piece. Leave it empty to count pieces."
        >
          <input
            value={f.unit}
            maxLength={30}
            placeholder="carton"
            onChange={(e) => set({ unit: e.target.value })}
          />
        </Field>
        <Field label="Cost price (₦)" hint="What you pay for one.">
          <input
            value={f.cost}
            inputMode="decimal"
            placeholder="8500"
            onChange={(e) => set({ cost: e.target.value })}
          />
        </Field>
        {edit.price && (
          <Field
            label="Selling price (₦)"
            hint="Shown on your page once it is published. Without one, it is not shown."
          >
            <input
              value={f.price}
              inputMode="decimal"
              placeholder="9500"
              onChange={(e) => set({ price: e.target.value })}
            />
          </Field>
        )}
        <LevelFields f={f} set={set} />
        {problem && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
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

/** Goods received, an adjustment with a reason, or a count. */
function MoveModal({
  item,
  name,
  onClose,
  onSaved,
}: {
  item: Counted;
  name: string;
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
    <Modal title={`Record stock of ${name}`} onClose={onClose}>
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
        <p className="hint">Now: {item.stock === null ? "no count yet" : quantity(item.stock, item.unit)}.</p>
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
