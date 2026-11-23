import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, postOnce } from "../api";
import { ErrorBox, Field, Items, Modal, Row, useAction, useLoad } from "../ui";
import { toast } from "../toast";
import { keyFor } from "../lib/things";
import {
  STATUS,
  payProblem,
  purchaseActions,
  purchaseSub,
  sizeParents,
  receiveForm,
  receiveProblem,
  receiveRequest,
  type Purchase,
  type ReceiveForm,
  type SuppliersView,
} from "../lib/purchases";
import {
  emptyHandLine,
  fromMemory,
  handLineRequest,
  handProblem,
  nameKey,
  toAnswer,
  type Choice,
  type HandLine,
  type Remembered,
} from "../lib/treat";
import { TrackAskBox, TreatQuestion, useSheetTreat, type Choices } from "./TreatLine";
import { feeInput, feeKobo } from "../lib/orders";
import { quantity, type StockItem, type StockView } from "../lib/stock";
import { formatPrice } from "../lib/storefront";
import { sellerLine } from "../lib/tradeorders";

/** Orders to suppliers on the Stock page (docs/stock.md "Suppliers and
 * purchase orders"): order what is low, approve and send, mark confirmed,
 * receive in part or in full, record a payment. */
export function PurchasesCard({ onChanged }: { onChanged: () => void }) {
  const list = useLoad(
    () => get<{ purchases: Purchase[] }>("/v1/stock/purchases"),
    [],
  );
  // A product's sizes are told together on a row (linesWords).
  const own = useLoad(
    () =>
      get<StockView>("/v1/stock")
        .then((v) => v.items)
        .catch((): StockItem[] => []),
    [],
  );
  const parents = sizeParents(own.data ?? []);
  const [receiving, setReceiving] = useState<Purchase | null>(null);
  const [paying, setPaying] = useState<Purchase | null>(null);
  const [byHand, setByHand] = useState(false);
  const [lowKey, setLowKey] = useState(() => crypto.randomUUID());
  const [skipped, setSkipped] = useState<string[]>([]);
  const act = useAction();
  const reload = () => {
    list.reload();
    onChanged();
  };
  const step = (
    p: Purchase,
    what: "send" | "confirm" | "cancel",
    done: string,
  ) =>
    void act.run(
      async () => (await post(`/v1/stock/purchases/${p.id}/${what}`), reload()),
      done,
    );
  // No suppliers on this Taskiem (no text messages): no card.
  if (list.error instanceof ApiError && list.error.status === 503) return null;
  // Recent orders: the open ones and the last few done.
  const shown = (list.data?.purchases ?? []).slice(0, 20);
  return (
    <section className="card" aria-labelledby="stock-orders">
      <div className="toolbar" style={{ marginBottom: 6 }}>
        <h2 id="stock-orders" className="grow" style={{ margin: 0 }}>
          Orders to suppliers
        </h2>
        <Link className="button small" to="/storefront/suppliers">
          Suppliers
        </Link>
        <button
          type="button"
          className="small"
          onClick={() => setByHand(true)}
        >
          New order
        </button>
        <button
          type="button"
          className="small primary"
          disabled={act.busy}
          onClick={() =>
            void act.run(async () => {
              const out = await postOnce<{
                purchases: Purchase[];
                no_supplier: string[];
              }>("/v1/stock/purchases/low", lowKey);
              setLowKey(crypto.randomUUID());
              setSkipped(out.no_supplier);
              reload();
            }, "Drafted what is low")
          }
        >
          Order what is low
        </button>
      </div>
      <p className="hint">
        Each low item becomes an order to its supplier, for enough to reach its
        order-up-to level. Approve and send it, and the supplier confirms on
        WhatsApp.
      </p>
      {skipped.length > 0 && (
        <div className="notice" role="status">
          {skipped.join(", ")} {skipped.length === 1 ? "has" : "have"} no
          supplier Taskiem knows. Add the supplier, then give the item their
          name.
        </div>
      )}
      <ErrorBox error={list.error ?? act.error} />
      {list.data && shown.length === 0 && (
        <p className="hint">No orders yet.</p>
      )}
      {shown.length > 0 && (
        <Items label="Orders to suppliers">
          {shown.map((p) => {
            const can = purchaseActions(p);
            return (
              <Row
                key={p.id}
                letter={p.supplier}
                title={`${p.reference} · ${p.supplier} · ${formatPrice(p.total_kobo)}`}
                side={
                  <span className={`badge ${STATUS[p.status].tone}`}>
                    {STATUS[p.status].label}
                  </span>
                }
                sub={
                  <span className="sub">
                    {purchaseSub(p, parents)}
                    {p.counter && (
                      <>
                        <br />
                        {p.supplier} asked: “{p.counter}”
                      </>
                    )}
                    {sellerLine(p) && (
                      <>
                        <br />
                        {sellerLine(p)}
                      </>
                    )}
                    {toAnswer(p) && (
                      <>
                        <br />
                        <span data-testid="purchase-ask">{toAnswer(p)}</span>
                      </>
                    )}
                  </span>
                }
                testid="purchase"
              >
                <Link className="button small" to={`/purchases/${p.id}`}>
                  Open<span className="sr-only"> {p.reference}</span>
                </Link>
                {can.send && (
                  <button
                    type="button"
                    className="small primary"
                    disabled={act.busy}
                    onClick={() =>
                      step(p, "send", `${p.reference} sent to ${p.supplier}`)
                    }
                  >
                    Approve and send
                    <span className="sr-only"> {p.reference}</span>
                  </button>
                )}
                {can.confirm && (
                  <button
                    type="button"
                    className="small"
                    disabled={act.busy}
                    onClick={() =>
                      step(p, "confirm", `${p.reference} marked confirmed`)
                    }
                  >
                    Mark confirmed
                    <span className="sr-only"> {p.reference}</span>
                  </button>
                )}
                {can.receive && (
                  <button
                    type="button"
                    className="small"
                    onClick={() => setReceiving(p)}
                  >
                    Goods came in
                    <span className="sr-only"> on {p.reference}</span>
                  </button>
                )}
                {can.answerDelivery && (
                  <Link className="button small" to={`/purchases/${p.id}`}>
                    Goods came in
                    <span className="sr-only"> on {p.reference}</span>
                  </Link>
                )}
                {can.pay && p.owed_kobo > 0 && (
                  <button
                    type="button"
                    className="small"
                    onClick={() => setPaying(p)}
                  >
                    Record a payment
                    <span className="sr-only"> for {p.reference}</span>
                  </button>
                )}
                {can.cancel && (
                  <button
                    type="button"
                    className="small danger"
                    disabled={act.busy}
                    onClick={() =>
                      confirm(`Cancel ${p.reference} to ${p.supplier}?`) &&
                      step(p, "cancel", `${p.reference} cancelled`)
                    }
                  >
                    Cancel<span className="sr-only"> {p.reference}</span>
                  </button>
                )}
              </Row>
            );
          })}
        </Items>
      )}
      {receiving && (
        <ReceiveModal
          purchase={receiving}
          onClose={() => setReceiving(null)}
          onSaved={() => {
            setReceiving(null);
            reload();
          }}
        />
      )}
      {byHand && (
        <OrderByHandModal
          onClose={() => setByHand(false)}
          onSaved={() => {
            setByHand(false);
            reload();
          }}
        />
      )}
      {paying && (
        <PayModal
          purchase={paying}
          onClose={() => setPaying(null)}
          onSaved={() => {
            setPaying(null);
            reload();
          }}
        />
      )}
    </section>
  );
}

/** How many came of each item: the stock rises, the cost price is set. */
function ReceiveModal({
  purchase,
  onClose,
  onSaved,
}: {
  purchase: Purchase;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [f, setF] = useState<ReceiveForm>(() => receiveForm(purchase));
  const [key] = useState(() => crypto.randomUUID());
  const act = useAction();
  const problem = receiveProblem(f);
  return (
    <Modal title={`Goods in on ${purchase.reference}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await postOnce(
                `/v1/stock/purchases/${purchase.id}/receive`,
                key,
                receiveRequest(f),
              ),
              onSaved()
            ),
            "Recorded",
          );
        }}
      >
        <p className="hint">
          From {purchase.supplier}. What goes to stock is counted in at the
          order's cost; what you keep track of goes on Things we have.
        </p>
        {purchase.lines.map((l) => (
          <Field
            key={l.item_id}
            label={`${l.item}: how many came`}
            hint={`Ordered ${quantity(l.quantity, l.unit)}${l.received ? `, ${l.received} came before` : ""}.`}
          >
            <input
              value={f[l.item_id] ?? ""}
              inputMode="numeric"
              onChange={(e) => setF({ ...f, [l.item_id]: e.target.value })}
            />
          </Field>
        ))}
        {problem && <p className="error-text">{problem}</p>}
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

/** A payment to the supplier, in cash or by transfer: an expense. */
function PayModal({
  purchase,
  onClose,
  onSaved,
}: {
  purchase: Purchase;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [amount, setAmount] = useState(() => feeInput(purchase.owed_kobo));
  const [how, setHow] = useState<"cash" | "transfer">("transfer");
  // One key for one order's payment tried again; a new one for another
  // order or amount (the server refuses a key used on another order).
  const key = useRef<{ body: string; key: string }>(undefined);
  const act = useAction();
  const problem = payProblem(amount);
  return (
    <Modal title={`Pay ${purchase.supplier}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => {
              const body = { amount_kobo: feeKobo(amount), paid_how: how };
              key.current = keyFor(key.current, { order: purchase.id, ...body });
              await postOnce(
                `/v1/stock/purchases/${purchase.id}/pay`,
                key.current.key,
                body,
              );
              onSaved();
            },
            "Payment recorded",
          );
        }}
      >
        <p className="hint">
          For {purchase.reference}: {formatPrice(purchase.owed_kobo)} owed. It
          goes into your money record as an expense.
        </p>
        <Field label="Amount paid (₦)">
          <input
            value={amount}
            inputMode="decimal"
            onChange={(e) => setAmount(e.target.value)}
            autoFocus
          />
        </Field>
        <fieldset className="sf-choices">
          <legend>How you paid</legend>
          {(
            [
              ["transfer", "Transfer"],
              ["cash", "Cash"],
            ] as const
          ).map(([k, label]) => (
            <label key={k} className="sf-radio">
              <input
                type="radio"
                name="paid-how"
                checked={how === k}
                onChange={() => setHow(k)}
              />
              {label}
            </label>
          ))}
        </fieldset>
        {problem && amount !== "" && <p className="error-text">{problem}</p>}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !!problem}
          >
            Record the payment
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** An order by hand to a supplier not on Taskiem (what a business buys
 * §6, Purchases by hand): what, how many and at what price, each line
 * asked how to treat it unless the supplier's item is remembered. */
function OrderByHandModal({
  onClose,
  onSaved,
}: {
  onClose: () => void;
  onSaved: () => void;
}) {
  const suppliers = useLoad(
    () =>
      get<SuppliersView>("/v1/stock/suppliers").then((v) =>
        v.suppliers.filter((s) => !s.link_id),
      ),
    [],
  );
  const [supplier, setSupplier] = useState("");
  const memory = useLoad(
    () =>
      supplier
        ? get<{ items: Remembered[] }>(
            `/v1/stock/suppliers/${encodeURIComponent(supplier)}/items`,
          ).then((r) => r.items)
        : Promise.resolve(undefined),
    [supplier],
  );
  const [lines, setLines] = useState<HandLine[]>([emptyHandLine()]);
  const [key] = useState(() => crypto.randomUUID());
  const treat = useSheetTreat();
  const act = useAction();
  const name = suppliers.data?.find((s) => s.id === supplier)?.name ?? "";
  // A name is asked about once it settles (on leaving its field, or once
  // typing stops), not from half of it ("Ny").
  const typed = lines.map((l) => l.item.trim()).join("\n");
  const [settledKey, setSettledKey] = useState(typed);
  useEffect(() => {
    const t = setTimeout(() => setSettledKey(typed), 600);
    return () => clearTimeout(t);
  }, [typed]);
  const settled = settledKey.split("\n");
  // Taskiem's own answer, not touched by the person, goes with the name it
  // was worked out for: a new name is asked afresh (or as remembered).
  const [auto, setAuto] = useState<Record<string, { name: string; c: Choice }>>({});
  const [seen, setSeen] = useState(settledKey);
  if (seen !== settledKey) {
    setSeen(settledKey);
    const gone = Object.entries(auto).filter(
      ([k, a]) => nameKey(settled[Number(k)] ?? "") !== a.name,
    );
    for (const [k, a] of gone)
      if (treat.choices[k] === a.c) treat.setter(k)(undefined);
    if (gone.length > 0)
      setAuto(
        Object.fromEntries(
          Object.entries(auto).filter(([k]) => !gone.some(([g]) => g === k)),
        ),
      );
  }
  const filled = lines
    .map((l, i) => ({ l, k: String(i), name: settled[i] ?? "" }))
    .filter(({ name }) => name !== "");
  const memoryOf = (item: string) => {
    const m = memory.data?.find((x) => x.name_key === nameKey(item));
    return m ? fromMemory(m) : undefined;
  };
  const memoryAt = (k: string | undefined) =>
    memoryOf(settled[Number(k)] ?? "");
  const problem = handProblem(supplier, lines);
  const answered =
    !!memory.data &&
    typed === settledKey &&
    treat.ready(
      filled.map(({ name, k }) => ({ key: k, memory: memoryOf(name) })),
    );
  const set = (i: number, patch: Partial<HandLine>) =>
    setLines(lines.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  const draft = (cs?: Choices) =>
    void act.run(async () => {
      const body = [];
      for (const { l, k } of filled)
        body.push({
          ...handLineRequest(l),
          ...(await treat.body(k, undefined, cs)),
        });
      let out: { purchase: Purchase };
      try {
        out = await postOnce<{ purchase: Purchase }>(
          "/v1/stock/purchases",
          `${key}-${JSON.stringify(body)}`,
          { supplier_id: supplier, lines: body },
        );
      } catch (e) {
        if (treat.refused(e, filled.map(({ k }) => k))) return;
        throw e;
      }
      await treat.remember(
        out.purchase.id,
        filled.map(({ k }, i) => ({ key: k, position: i })),
        cs,
      );
      // Said here, not by run: a many-to-track question returns with
      // nothing drafted.
      toast("Order drafted");
      onSaved();
    }, false);
  return (
    <Modal title="A new order" onClose={onClose}>
      <p className="hint">
        To a supplier not on Taskiem. Approve and send it from the list, and
        they get it by text message.
      </p>
      <ErrorBox error={suppliers.error ?? memory.error ?? act.error} />
      <Field label="Supplier">
        <select value={supplier} onChange={(e) => setSupplier(e.target.value)}>
          <option value="">Choose a supplier</option>
          {(suppliers.data ?? []).map((s) => (
            <option key={s.id} value={s.id}>
              {s.name}
            </option>
          ))}
        </select>
      </Field>
      {lines.map((l, i) => (
        <div key={i} className="row" data-testid="hand-line">
          <Field label={`What ${i + 1}`}>
            <input
              value={l.item}
              maxLength={200}
              onChange={(e) => set(i, { item: e.target.value })}
              onBlur={() => setSettledKey(typed)}
            />
          </Field>
          <Field label={`How many ${i + 1}`}>
            <input
              inputMode="numeric"
              value={l.quantity}
              onChange={(e) => set(i, { quantity: e.target.value })}
            />
          </Field>
          <Field label={`Price of one ${i + 1} (₦)`}>
            <input
              inputMode="decimal"
              value={l.price}
              onChange={(e) => set(i, { price: e.target.value })}
            />
          </Field>
        </div>
      ))}
      {lines.length < 50 && (
        <button
          type="button"
          className="small"
          onClick={() => setLines([...lines, emptyHandLine()])}
        >
          Add another
        </button>
      )}
      {supplier && memory.data && filled.length > 0 && (
        <div className="treat-list" data-testid="treat-lines">
          <h3>How should we treat each?</h3>
          {filled.map(({ name: item, k }) => (
            <TreatQuestion
              key={`${k}-${nameKey(item)}`}
              item={item}
              supplier={name}
              data={treat.data}
              memory={memoryOf(item)}
              value={treat.choices[k]}
              onChange={(c, seeded) => {
                if (seeded && c)
                  setAuto((cur) => ({ ...cur, [k]: { name: nameKey(item), c } }));
                treat.setter(k)(c);
              }}
            />
          ))}
        </div>
      )}
      {treat.ask && (
        <TrackAskBox
          ask={treat.ask.ask}
          busy={act.busy}
          onYes={() => draft(treat.answer(true, memoryAt(treat.ask?.key)))}
          onRecord={() => draft(treat.answer(false, memoryAt(treat.ask?.key)))}
        />
      )}
      {problem && supplier && <p className="hint">{problem}</p>}
      <div className="toolbar">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !!problem || !answered}
          onClick={() => draft()}
        >
          Draft the order
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}
