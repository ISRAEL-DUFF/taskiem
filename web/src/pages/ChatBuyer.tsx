import { useMemo, useState } from "react";
import { Link } from "react-router-dom";
import { get, post, postOnce, put } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { KitEmpty, KitField, KitLoading, Pill, SavedTick, Sheet } from "../kit";
import {
  billHero,
  billStatus,
  canPay,
  sentLine,
  type Bill,
  type BillPaySettings,
} from "../lib/bills";
import {
  bagOf,
  priceLine,
  purchaseState,
  quoteText,
  reorderQty,
  stepQty,
} from "../lib/buyer";
import { ORDER_STEPS } from "../lib/cardframe";
import type { ItemLink, PriceItem, SupplierPrices } from "../lib/pricelists";
import type { Purchase } from "../lib/purchases";
import type { StockItem, StockView } from "../lib/stock";
import type { Supplier } from "../lib/purchases";
import { formatPrice } from "../lib/storefront";
import { nairaKobo } from "../lib/toolforms";
import { ErrorBox, useLoad } from "../ui";
import { initialsOf } from "./ChatAvatar";
import { useConfirm } from "./ConfirmSheet";

// The buyer's tools in a conversation with a supplier (docs/spec/chat-tools.md
// §5, phase 3): an order from their price list (the Buyer design), the
// orders sent them, and their bills, each through the same API as its page.

const key = () => `chat-${crypto.randomUUID()}`;

/** Where the Order sheet starts: an item in the bag, or a past order's lines. */
export interface OrderStart {
  itemId?: string;
  qty?: Record<string, number>;
  /** Reorder: the last order's lines, as the price list has them now. */
  last?: Purchase;
}

/** An order from a supplier's price list: pick, review, place (or ask them
 * to quote). Placing drafts the order at their prices and sends it. */
export function OrderSheet({
  link,
  business,
  start,
  from,
  onClose,
  onPlaced,
}: {
  link: string;
  business: string;
  start?: OrderStart;
  from?: DOMRect | null;
  onClose: () => void;
  onPlaced?: (words: string) => void;
}) {
  const { can } = useAuth();
  const prices = useLoad(
    () =>
      get<{ suppliers: SupplierPrices[] }>(
        `/v1/supplier-prices?link=${encodeURIComponent(link)}`,
      ),
    [link],
  );
  const links = useLoad(
    () =>
      get<{ item_links: ItemLink[] }>(
        `/v1/item-links/${encodeURIComponent(link)}`,
      ).catch(() => ({ item_links: [] })),
    [link],
  );
  // An order goes to one of our suppliers: the one this business is.
  const suppliers = useLoad(
    () =>
      get<{ suppliers: Supplier[] }>("/v1/stock/suppliers").then(
        (r) => r.suppliers,
        () => [] as Supplier[],
      ),
    [],
  );
  const theirs = suppliers.data?.find((x) => x.link_id === link);
  const [who, setWho] = useState<string>();
  const [phone, setPhone] = useState("");
  const own = useLoad(
    () =>
      get<StockView>("/v1/stock").then(
        (r) => r.items,
        () => [] as StockItem[],
      ),
    [],
  );
  const list = prices.data?.suppliers[0];
  const [qty, setQty] = useState<Record<string, number>>(
    () => start?.qty ?? {},
  );
  const [seeded, setSeeded] = useState(false);
  if (!seeded && list && links.data && (start?.itemId || start?.last)) {
    if (start.last)
      setQty(reorderQty(start.last, links.data.item_links, list.items));
    const it = list.items.find((x) => x.item_id === start.itemId);
    if (it && !qty[it.item_id])
      setQty({ ...qty, [it.item_id]: Math.max(1, it.min_quantity) });
    setSeeded(true);
  }
  const [step, setStep] = useState<"pick" | "review">("pick");
  const [search, setSearch] = useState("");
  const [note, setNote] = useState("");
  const [mine, setMine] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [done, setDone] = useState<string>();
  const [shut, setShut] = useState(false);
  const bag = bagOf(list?.items ?? [], qty);
  const linkedTo = (sellerItem: string) =>
    links.data?.item_links.find((l) => l.seller_item_id === sellerItem)
      ?.item_id;
  // A line not linked to one of our items: which it is (the same name found
  // first), or a new item of ours.
  const ownFor = (it: PriceItem) =>
    linkedTo(it.item_id) ??
    mine[it.item_id] ??
    own.data?.find((x) => x.name.toLowerCase() === it.name.toLowerCase())?.id ??
    "new";
  const shown = (list?.items ?? []).filter((it) =>
    it.name.toLowerCase().includes(search.trim().toLowerCase()),
  );
  const finish = (words: string) => {
    setDone(words);
    onPlaced?.(words);
    window.setTimeout(() => setShut(true), 1300);
  };
  // Not yet one of our suppliers: an unlinked one of ours, or a new one.
  const unlinkedSuppliers = (suppliers.data ?? []).filter((x) => !x.link_id);
  const supplierFor = theirs
    ? theirs.id
    : (who ??
      unlinkedSuppliers.find(
        (x) => x.name.toLowerCase() === business.toLowerCase(),
      )?.id ??
      "new");
  const place = async () => {
    setBusy(true);
    setError(undefined);
    try {
      if (!theirs) {
        let id = supplierFor;
        if (id === "new") {
          const made = await post<{ supplier: { id: string } }>(
            "/v1/stock/suppliers",
            { name: business, phone: phone.trim() },
          );
          id = made.supplier.id;
        }
        await put(`/v1/stock/suppliers/${encodeURIComponent(id)}/link`, {
          link_id: link,
        });
        suppliers.reload();
      }
      const lines = [];
      for (const l of bag.lines) {
        let itemId = ownFor(l.item);
        if (itemId === "new") {
          const made = await postOnce<{ item: { id: string } }>(
            "/v1/stock/items",
            key(),
            { name: l.item.name, unit: l.item.unit ?? "", quantity: 0 },
          );
          itemId = made.item.id;
        }
        lines.push({
          seller_item_id: l.item.item_id,
          quantity: l.quantity,
          item_id: linkedTo(l.item.item_id) ? undefined : itemId,
        });
      }
      const out = await postOnce<{ purchase: Purchase }>(
        `/v1/supplier-prices/${encodeURIComponent(link)}/order`,
        key(),
        { lines, note: note.trim() },
      );
      await post(
        `/v1/stock/purchases/${encodeURIComponent(out.purchase.id)}/send`,
      );
      finish(`Order sent to ${business}: ${formatPrice(bag.total)}`);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  const askQuote = async () => {
    setBusy(true);
    setError(undefined);
    try {
      await post(`/v1/link-chats/${encodeURIComponent(link)}/messages`, {
        text: quoteText(bag.lines),
      });
      finish(`Asked ${business} for a quote`);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  const updated =
    list?.updated_at &&
    new Date(list.updated_at).toDateString() === new Date().toDateString()
      ? "updated today"
      : "as they share them";
  return (
    <Sheet
      title={step === "review" ? "Review your order" : `Order from ${business}`}
      from={from}
      onClose={onClose}
      onBack={step === "review" ? () => setStep("pick") : undefined}
      shut={shut}
      testid="order-sheet"
      footer={
        step === "pick" ? (
          <div className="tk-bag" data-testid="order-bag">
            <span>
              <span className="tk-bag-count">{bag.count}</span>
              <span className="tk-bag-total">{formatPrice(bag.total)}</span>
            </span>
            <button
              type="button"
              className="tk-bag-go"
              disabled={bag.lines.length === 0}
              onClick={() => setStep("review")}
            >
              Review order <Icon name="chevron" />
            </button>
          </div>
        ) : (
          <div className="tk-review-foot">
            <div className="tk-review-total">
              <span>Total</span>
              <b>{formatPrice(bag.total)}</b>
            </div>
            <div className="tk-foot-two">
              <button
                type="button"
                className="tk-btn ghost"
                disabled={busy}
                onClick={() => void askQuote()}
              >
                Ask for a quote
              </button>
              <button
                type="button"
                className="tk-cta"
                disabled={
                  busy ||
                  !can("table.edit") ||
                  (!!suppliers.data &&
                    !theirs &&
                    supplierFor === "new" &&
                    phone.trim() === "")
                }
                onClick={() => void place()}
              >
                {busy ? "Sending…" : "Place order"}
              </button>
            </div>
          </div>
        )
      }
    >
      {done && <SavedTick words={done} />}
      <div className="tk-order-who">
        <span
          className="chat-avatar"
          aria-hidden="true"
          data-initials={initialsOf(business)}
        />
        <span className="tk-row-sub">Your prices · {updated}</span>
      </div>
      {!prices.data ? (
        prices.error ? (
          <ErrorBox error={prices.error} />
        ) : (
          <KitLoading label="Loading their prices…" />
        )
      ) : !list ? (
        <KitEmpty
          icon="tag"
          title={`${business} shares no price list with you`}
        >
          Ask them in the chat to share one.
        </KitEmpty>
      ) : step === "pick" ? (
        <>
          <label className="tk-search">
            <Icon name="search" />
            <span className="sr-only">Search their price list</span>
            <input
              type="search"
              placeholder="Search their price list"
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          <ul className="tk-sheet-list tk-order-list tk-stagger">
            {shown.map((it) => {
              const n = qty[it.item_id] ?? 0;
              return (
                <li
                  key={it.item_id}
                  className={`tk-order-row${n > 0 ? " on" : ""}`}
                  data-testid="order-row"
                >
                  <span className="tk-row-tile" aria-hidden="true">
                    <Icon name="box" />
                  </span>
                  <span className="tk-row-words">
                    <span className="tk-row-name">{it.name}</span>
                    <span className="tk-row-sub">
                      {it.available ? priceLine(it) : "Not available now"}
                    </span>
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
                      onClick={() =>
                        setQty({
                          ...qty,
                          [it.item_id]: stepQty(n, it.min_quantity, -1),
                        })
                      }
                    >
                      −
                    </button>
                    <span aria-live="polite">{n}</span>
                    <button
                      type="button"
                      aria-label={`One more ${it.name}`}
                      disabled={!it.available}
                      onClick={() =>
                        setQty({
                          ...qty,
                          [it.item_id]: stepQty(n, it.min_quantity, 1),
                        })
                      }
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
            {bag.lines.map((l) => (
              <li key={l.item.item_id}>
                <span>
                  {l.quantity} × {l.item.name}
                </span>
                <b>{formatPrice(l.quantity * l.item.price_kobo)}</b>
              </li>
            ))}
          </ul>
          {bag.lines.some((l) => !linkedTo(l.item.item_id)) && (
            <>
              <p className="tk-form-now">
                Which of your items is each? It is remembered for next time.
              </p>
              {bag.lines
                .filter((l) => !linkedTo(l.item.item_id))
                .map((l) => (
                  <KitField key={l.item.item_id} label={l.item.name}>
                    <select
                      value={ownFor(l.item)}
                      onChange={(e) =>
                        setMine({ ...mine, [l.item.item_id]: e.target.value })
                      }
                    >
                      <option value="new">
                        A new item of mine: {l.item.name}
                      </option>
                      {(own.data ?? []).map((x) => (
                        <option key={x.id} value={x.id}>
                          {x.name}
                        </option>
                      ))}
                    </select>
                  </KitField>
                ))}
            </>
          )}
          {suppliers.data && !theirs && (
            <>
              <p className="tk-form-now">
                {business} is not one of your suppliers yet. Orders and bills
                from them are kept with your other suppliers.
              </p>
              {unlinkedSuppliers.length > 0 && (
                <KitField label={`Which of your suppliers is ${business}?`}>
                  <select
                    value={supplierFor}
                    onChange={(e) => setWho(e.target.value)}
                  >
                    <option value="new">A new supplier: {business}</option>
                    {unlinkedSuppliers.map((x) => (
                      <option key={x.id} value={x.id}>
                        {x.name}
                      </option>
                    ))}
                  </select>
                </KitField>
              )}
              {supplierFor === "new" && (
                <KitField
                  label={`${business}'s phone number`}
                  hint="For their record with you; the order reaches them on Taskiem."
                >
                  <input
                    type="tel"
                    inputMode="tel"
                    autoComplete="off"
                    value={phone}
                    onChange={(e) => setPhone(e.target.value)}
                    placeholder="0803 123 4567"
                  />
                </KitField>
              )}
            </>
          )}
          <KitField label="A note for them (optional)">
            <input
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Deliver before Friday"
            />
          </KitField>
          <p className="tk-form-now">
            Delivery and payment as you agree in the chat. The invoice arrives
            here.
          </p>
          <ErrorBox error={error} />
        </div>
      )}
    </Sheet>
  );
}

/** An order sent to a supplier: what it asks for, where it is, and what
 * the buyer can do (confirm their quote, cancel, say all came, reorder). */
export function PurchaseView({
  purchase,
  onSaved,
  onReorder,
  canEdit = true,
}: {
  purchase: Purchase;
  onSaved: (line: string | null) => void;
  onReorder?: (p: Purchase) => void;
  canEdit?: boolean;
}) {
  const { confirm, node } = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const st = purchaseState(purchase);
  const act = async (what: string, words: string, body?: unknown) => {
    setBusy(true);
    setError(undefined);
    try {
      await post(
        `/v1/stock/purchases/${encodeURIComponent(purchase.id)}/${what}`,
        body,
      );
      onSaved(words);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  const open =
    purchase.status !== "cancelled" && purchase.status !== "received";
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        <p className="tk-form-now">
          {purchase.supplier} · <Pill tone={st.tone}>{st.word}</Pill>
        </p>
        {st.step !== undefined && (
          <span className="tk-progress">
            <span
              className="tk-progress-bar"
              role="img"
              aria-label={`${ORDER_STEPS[st.step]}: step ${st.step + 1} of ${ORDER_STEPS.length}`}
            >
              {ORDER_STEPS.map((w, i) => (
                <span key={w} className={i <= (st.step ?? -1) ? "done" : ""} />
              ))}
            </span>
            <span className="tk-progress-words" aria-hidden="true">
              {ORDER_STEPS.map((w, i) => (
                <span key={w} className={i === st.step ? "now" : ""}>
                  {w}
                </span>
              ))}
            </span>
          </span>
        )}
        <ul className="tk-lines">
          {(purchase.seller_status === "changed" && purchase.change
            ? purchase.change
            : purchase.lines
          ).map((l, i) => (
            <li key={i}>
              <span>
                {l.quantity} × {l.item}
              </span>
              <b>
                {l.cost_kobo
                  ? formatPrice(l.quantity * l.cost_kobo)
                  : "No price"}
              </b>
            </li>
          ))}
          <li className="tk-lines-total">
            <span>Total</span>
            <b>{formatPrice(purchase.total_kobo)}</b>
          </li>
        </ul>
        {purchase.seller_note && (
          <p className="tk-form-now">Their note: “{purchase.seller_note}”</p>
        )}
        {purchase.delivered_at && (
          <p className="tk-form-now">
            Delivered{" "}
            {new Date(purchase.delivered_at).toLocaleDateString("en-GB", {
              day: "numeric",
              month: "short",
            })}
          </p>
        )}
        <ErrorBox error={error} />
      </div>
      <div className="tk-sheet-foot tk-foot-two">
        {canEdit && st.quote && (
          <button
            type="button"
            className="tk-cta"
            disabled={busy}
            onClick={() =>
              void act(
                "confirm-proposed",
                `Confirmed ${purchase.reference}`,
                {},
              )
            }
          >
            Confirm their quote
          </button>
        )}
        {canEdit && purchase.seller_status === "changed" && (
          <button
            type="button"
            className="tk-cta"
            disabled={busy}
            onClick={() =>
              void act(
                "accept-change",
                `Accepted the change to ${purchase.reference}`,
              )
            }
          >
            Accept the change
          </button>
        )}
        {canEdit &&
          purchase.seller_status === "delivered" &&
          purchase.status !== "received" && (
            <button
              type="button"
              className="tk-cta"
              disabled={busy}
              onClick={() =>
                void act("all-came", `All of ${purchase.reference} came`)
              }
            >
              All came
            </button>
          )}
        {canEdit &&
          open &&
          !purchase.cancel_asked_at &&
          purchase.seller_status !== "delivered" && (
            <button
              type="button"
              className="tk-btn ghost"
              disabled={busy}
              onClick={async () =>
                (await confirm({
                  title: `Cancel ${purchase.reference}?`,
                  body: `${purchase.supplier} is told.`,
                  ok: "Cancel the order",
                  danger: true,
                })) && void act("cancel", `Cancelled ${purchase.reference}`)
              }
            >
              Cancel
            </button>
          )}
        {onReorder && (
          <button
            type="button"
            className={st.quote || open ? "tk-btn ghost" : "tk-cta"}
            onClick={() => onReorder(purchase)}
          >
            Reorder
          </button>
        )}
      </div>
      {node}
    </>
  );
}

/** A bill from a supplier: what is left to pay, Pay (their checkout, or
 * from Taskiem), a question about it, or what was paid outside Taskiem. */
export function BillView({
  bill,
  onSaved,
  canEdit = true,
}: {
  bill: Bill;
  onSaved: (line: string | null) => void;
  canEdit?: boolean;
}) {
  const settings = useLoad(
    () => get<BillPaySettings>("/v1/bills/pay").catch(() => null),
    [],
  );
  const [mode, setMode] = useState<"" | "query" | "paid">("");
  const [reason, setReason] = useState("");
  const [amount, setAmount] = useState("");
  const [how, setHow] = useState("transfer");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const hero = billHero(bill);
  const st = billStatus(bill.status);
  const due = bill.due_on
    ? new Date(bill.due_on).toLocaleDateString("en-GB", {
        weekday: "long",
        day: "numeric",
        month: "short",
      })
    : "";
  const run = async (path: string, body: unknown, words: string) => {
    setBusy(true);
    setError(undefined);
    try {
      await post(`/v1/bills/${encodeURIComponent(bill.id)}/${path}`, body);
      onSaved(words);
    } catch (e) {
      setError(e);
      setBusy(false);
    }
  };
  const fromTaskiem = canEdit && canPay(bill) && !!settings.data?.workflow_id;
  const amountKobo = useMemo(() => nairaKobo(amount), [amount]);
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        <div className="tk-bill-hero" data-testid="bill-hero">
          <span className="tk-kicker">
            {hero.label}
            {due && bill.due_kobo > 0 ? ` · due ${due}` : ""}
          </span>
          <b>{formatPrice(hero.kobo)}</b>
          <span className="tk-row-sub">{hero.sub}</span>
          {st.label !== hero.label && (
            <Pill
              tone={
                st.tone === "ok"
                  ? "lime"
                  : st.tone === "warn"
                    ? "warn"
                    : "plain"
              }
            >
              {st.label}
            </Pill>
          )}
        </div>
        {sentLine(bill) && <p className="tk-form-now">{sentLine(bill)}</p>}
        <ul className="tk-lines">
          {bill.lines.map((l, i) => (
            <li key={i}>
              <span>
                {l.quantity} × {l.description}
              </span>
              <b>{formatPrice(Math.round(l.quantity * l.unit_kobo))}</b>
            </li>
          ))}
        </ul>
        {bill.pay_account_number && bill.due_kobo > 0 && (
          <p className="tk-form-now">
            Or transfer to <b className="mono">{bill.pay_account_number}</b>
            {bill.pay_bank_name ? ` (${bill.pay_bank_name})` : ""} with{" "}
            <b>{bill.reference}</b> in the note: it is counted by itself.
          </p>
        )}
        {mode === "query" && (
          <KitField
            label={`What is wrong with ${bill.reference}?`}
            hint={`${bill.supplier_name} is told, and it is not paid from Taskiem until it is settled.`}
          >
            <textarea
              rows={3}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              placeholder="Ten bags were torn"
            />
          </KitField>
        )}
        {mode === "paid" && (
          <>
            <KitField
              label="What you paid outside Taskiem (₦)"
              hint={`${bill.supplier_name} is told. It counts as paid when their Taskiem matches the money.`}
            >
              <input
                inputMode="decimal"
                value={amount}
                onChange={(e) => setAmount(e.target.value)}
                placeholder="25,000"
              />
            </KitField>
            <KitField label="How">
              <select value={how} onChange={(e) => setHow(e.target.value)}>
                <option value="transfer">By transfer</option>
                <option value="cash">In cash</option>
                <option value="other">Another way</option>
              </select>
            </KitField>
          </>
        )}
        <ErrorBox error={error ?? settings.error} />
        {canEdit && mode === "" && bill.due_kobo > 0 && (
          <div className="tk-bill-more">
            <button
              type="button"
              className="tk-btn soft"
              onClick={() => setMode("query")}
              disabled={bill.disputed}
            >
              {bill.disputed ? "Questioned" : "Question it"}
            </button>
            <button
              type="button"
              className="tk-btn soft"
              onClick={() => setMode("paid")}
            >
              I paid outside Taskiem
            </button>
          </div>
        )}
      </div>
      <div className="tk-sheet-foot tk-foot-two">
        {mode === "query" ? (
          <button
            type="button"
            className="tk-cta"
            disabled={busy || reason.trim() === ""}
            onClick={() =>
              void run(
                "query",
                { reason: reason.trim() },
                `Questioned ${bill.reference}`,
              )
            }
          >
            Send the question
          </button>
        ) : mode === "paid" ? (
          <button
            type="button"
            className="tk-cta"
            disabled={busy || !(amountKobo > 0)}
            onClick={() =>
              void run(
                "declare",
                { amount_kobo: amountKobo, how },
                `Paid ${formatPrice(amountKobo)} of ${bill.reference} outside Taskiem`,
              )
            }
          >
            Tell {bill.supplier_name}
          </button>
        ) : bill.due_kobo > 0 ? (
          <>
            {bill.pay_link && (
              <a
                className={fromTaskiem ? "tk-btn ghost" : "tk-cta tk-pay"}
                href={bill.pay_link}
                target="_blank"
                rel="noreferrer"
              >
                Pay {formatPrice(bill.due_kobo)}
                <span className="sr-only">
                  {" "}
                  on {bill.supplier_name}'s checkout (opens a new tab)
                </span>
              </a>
            )}
            {fromTaskiem && (
              <button
                type="button"
                className="tk-cta tk-pay"
                disabled={busy}
                onClick={() =>
                  void run("pay", {}, `Paying ${bill.reference} from Taskiem`)
                }
              >
                Pay from Taskiem
              </button>
            )}
            {!bill.pay_link && !fromTaskiem && (
              <Link
                className="tk-cta"
                to={`/bills/${encodeURIComponent(bill.id)}`}
              >
                How to pay
              </Link>
            )}
          </>
        ) : (
          <Link className="tk-cta" to={`/bills/${encodeURIComponent(bill.id)}`}>
            Open the bill
          </Link>
        )}
      </div>
    </>
  );
}
