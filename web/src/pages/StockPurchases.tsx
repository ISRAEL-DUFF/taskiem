import { useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, postOnce } from "../api";
import { ErrorBox, Field, Items, Modal, Row, useAction, useLoad } from "../ui";
import {
  STATUS,
  payProblem,
  purchaseActions,
  purchaseSub,
  receiveForm,
  receiveProblem,
  receiveRequest,
  type Purchase,
  type ReceiveForm,
} from "../lib/purchases";
import { feeInput, feeKobo } from "../lib/orders";
import { quantity } from "../lib/stock";
import { formatPrice } from "../lib/storefront";

/** Orders to suppliers on the Stock page (docs/stock.md "Suppliers and
 * purchase orders"): order what is low, approve and send, mark confirmed,
 * receive in part or in full, record a payment. */
export function PurchasesCard({ onChanged }: { onChanged: () => void }) {
  const list = useLoad(
    () => get<{ purchases: Purchase[] }>("/v1/stock/purchases"),
    [],
  );
  const [receiving, setReceiving] = useState<Purchase | null>(null);
  const [paying, setPaying] = useState<Purchase | null>(null);
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
                    {purchaseSub(p)}
                    {p.counter && (
                      <>
                        <br />
                        {p.supplier} asked: “{p.counter}”
                      </>
                    )}
                  </span>
                }
                testid="purchase"
              >
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
          From {purchase.supplier}. Each item comes into your stock, and its
          cost price becomes the order's.
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
  const [key] = useState(() => crypto.randomUUID());
  const act = useAction();
  const problem = payProblem(amount);
  return (
    <Modal title={`Pay ${purchase.supplier}`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          void act.run(
            async () => (
              await postOnce(`/v1/stock/purchases/${purchase.id}/pay`, key, {
                amount_kobo: feeKobo(amount),
                paid_how: how,
              }),
              onSaved()
            ),
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
