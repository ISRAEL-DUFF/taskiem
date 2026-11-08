import { useMemo, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, get, post, postOnce, put } from "../api";
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
  orderLine,
  purchaseState,
  quoteText,
  reorderQty,
  stepQty,
} from "../lib/buyer";
import { ORDER_STEPS } from "../lib/cardframe";
import { categoryChips, categoryTag } from "../lib/tools";
import { breakWords, dealKobo, offWords, priceAt } from "../lib/deals";
import { answersDelivery } from "../lib/purchases";
import { buyerSteps, sameName } from "../lib/tradeorders";
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
  /** A category picked in "@": the list opens showing it. */
  category?: string;
}

/** An order from a supplier's price list: pick, review, place (or ask them
 * to quote). Placing drafts the order at their prices and sends it. */
/** The items of their list a chip and a search show: by name first, then
 * those whose category holds the words. */
export function orderShown(
  items: PriceItem[],
  chip: string | undefined,
  search: string,
): PriceItem[] {
  const q = search.trim().toLowerCase();
  const picked = items.filter(
    (it) => !chip || (it.category && categoryTag(it.category) === chip),
  );
  if (q === "") return picked;
  return [
    ...picked.filter((it) => it.name.toLowerCase().includes(q)),
    ...picked.filter(
      (it) =>
        !it.name.toLowerCase().includes(q) &&
        (it.category ?? "").toLowerCase().includes(q),
    ),
  ];
}

/** Their list under its categories' headings, in the order the list has
 * them, "Other" last; one section with no heading when it has none. */
export function orderSections(
  items: PriceItem[],
): { label: string; items: PriceItem[] }[] {
  if (!items.some((it) => it.category))
    return items.length ? [{ label: "", items }] : [];
  const by = new Map<string, { label: string; items: PriceItem[] }>();
  const other: PriceItem[] = [];
  for (const it of items) {
    const c = it.category?.trim();
    if (!c) {
      other.push(it);
      continue;
    }
    const k = c.toLowerCase();
    const sec = by.get(k) ?? { label: c, items: [] };
    sec.items.push(it);
    by.set(k, sec);
  }
  const out = [...by.values()];
  if (other.length) out.push({ label: "Other", items: other });
  return out;
}

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
      ),
    [],
  );
  // Our last order with them, for "Same as last time".
  const last = useLoad(
    () =>
      get<{ purchases: Purchase[] }>("/v1/stock/purchases").then(
        (r) =>
          r.purchases
            .filter(
              (p) =>
                p.link_id === link &&
                p.status !== "cancelled" &&
                p.seller_status !== "proposed",
            )
            .sort((a, b) => b.created_at.localeCompare(a.created_at))[0],
        () => undefined,
      ),
    [link],
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
  const [chip, setChip] = useState<string | undefined>(
    start?.category ? categoryTag(start.category) : undefined,
  );
  const [note, setNote] = useState("");
  // Make an offer (phase 7): our own price for the whole order.
  const [offering, setOffering] = useState(false);
  const [offerText, setOfferText] = useState("");
  const [mine, setMine] = useState<Record<string, string>>({});
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [done, setDone] = useState<string>();
  const [shut, setShut] = useState(false);
  const bag = bagOf(list?.items ?? [], qty);
  const offerKobo = offering ? dealKobo(offerText) : NaN;
  const offerProblem = !offering
    ? ""
    : offerText.trim() === ""
      ? "Say your price for the whole order."
      : !(offerKobo > 0)
        ? "A price in naira, like 740,000 or 740k."
        : offerKobo >= bag.total
          ? `An offer is under their price, ${formatPrice(bag.total)}.`
          : "";
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
  // Their categories as chips; the list under their headings.
  const cats = categoryChips(list?.items ?? []);
  const shown = orderShown(list?.items ?? [], chip, search);
  const sections = orderSections(shown);
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
  // What placing has made so far, kept for the sheet's life: a retry after
  // a failure part way does each step once (the same keys, the same ids).
  const made = useRef<{
    supplier?: string;
    linked?: boolean;
    items: Record<string, string>;
    orders: Record<string, Purchase>;
    keys: Record<string, string>;
  }>({ items: {}, orders: {}, keys: {} });
  const keyOf = (what: string) => (made.current.keys[what] ??= key());
  const place = async () => {
    setBusy(true);
    setError(undefined);
    const m = made.current;
    try {
      if (!theirs && !m.linked) {
        let id = supplierFor === "new" ? m.supplier : supplierFor;
        if (!id) {
          const out = await postOnce<{ supplier: { id: string } }>(
            "/v1/stock/suppliers",
            keyOf("supplier"),
            { name: business, phone: phone.trim() },
          );
          id = m.supplier = out.supplier.id;
        }
        await put(`/v1/stock/suppliers/${encodeURIComponent(id)}/link`, {
          link_id: link,
        });
        m.linked = true;
      }
      const lines = [];
      for (const l of bag.lines) {
        const seller = l.item.item_id;
        let itemId = m.items[seller] ?? ownFor(l.item);
        if (itemId === "new") {
          const out = await postOnce<{ item: { id: string } }>(
            "/v1/stock/items",
            keyOf(`item-${seller}`),
            { name: l.item.name, unit: l.item.unit ?? "", quantity: 0 },
          );
          itemId = m.items[seller] = out.item.id;
        }
        lines.push({
          seller_item_id: seller,
          quantity: l.quantity,
          item_id: linkedTo(seller) ? undefined : itemId,
        });
      }
      // The same bag, note and offer is the same order: drafted once.
      const deal = offering ? { offer: true, total_kobo: offerKobo } : {};
      const what = JSON.stringify([lines, note.trim(), deal]);
      let order = m.orders[what];
      if (!order) {
        const out = await postOnce<{ purchase: Purchase }>(
          `/v1/supplier-prices/${encodeURIComponent(link)}/order`,
          keyOf(`order-${what}`),
          { lines, note: note.trim(), ...deal },
        );
        order = m.orders[what] = out.purchase;
      }
      try {
        await post(`/v1/stock/purchases/${encodeURIComponent(order.id)}/send`);
      } catch (e) {
        // Above the business's limit: drafted, for someone else to approve.
        if (e instanceof ApiError && e.status === 409) {
          finish(
            `${order.reference} is drafted: it is above your limit, so someone else sends it`,
          );
          return;
        }
        throw e;
      }
      finish(
        offering
          ? `Offer sent to ${business}: ${formatPrice(offerKobo)}`
          : `Order sent to ${business}: ${formatPrice(bag.total)}`,
      );
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
  // Everything placing reads is here; what still stops it, said.
  const loaded = !!suppliers.data && !!own.data && !!links.data;
  const needPhone =
    !!suppliers.data && !theirs && supplierFor === "new" && phone.trim() === "";
  const stops = !loaded
    ? undefined
    : needPhone
      ? `Add ${business}'s phone number to place the order.`
      : undefined;
  return (
    <Sheet
      title={step === "review" ? "Review your order" : `Order from ${business}`}
      from={from}
      onClose={onClose}
      onBack={step === "review" ? () => setStep("pick") : undefined}
      shut={shut}
      testid="order-sheet"
      lead={
        step === "pick" ? (
          <span
            className="chat-avatar tk-sheet-lead"
            aria-hidden="true"
            data-initials={initialsOf(business)}
            data-tone="pine"
          />
        ) : undefined
      }
      sub={step === "pick" ? `Your prices · ${updated}` : undefined}
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
              Review order <Icon name="arrow-right" />
            </button>
          </div>
        ) : (
          <div className="tk-review-foot">
            <div className="tk-review-total">
              <span>{offering ? "Your offer" : "Total"}</span>
              <b>
                {offering && offerKobo > 0 && !offerProblem ? (
                  <>
                    {formatPrice(offerKobo)}{" "}
                    <s className="tk-was">{formatPrice(bag.total)}</s>
                  </>
                ) : (
                  formatPrice(bag.total)
                )}
              </b>
            </div>
            {stops && <p className="tk-review-stops">{stops}</p>}
            <div className="tk-foot-two">
              {can("chat.trade") && (
                <button
                  type="button"
                  className="tk-btn ghost"
                  disabled={busy}
                  onClick={() => void askQuote()}
                >
                  Ask for a quote
                </button>
              )}
              <button
                type="button"
                className="tk-cta"
                disabled={busy || !loaded || needPhone || !!offerProblem}
                onClick={() => void place()}
              >
                {busy ? "Sending…" : offering ? "Send offer" : "Place order"}
              </button>
            </div>
          </div>
        )
      }
    >
      {done && <SavedTick words={done} />}
      {step === "pick" && last.data && bag.lines.length === 0 && list && (
        <div className="tk-order-who">
          <button
            type="button"
            className="tk-chip-btn"
            onClick={() =>
              setQty(
                reorderQty(
                  last.data as Purchase,
                  links.data?.item_links ?? [],
                  list.items,
                ),
              )
            }
          >
            Same as last time
            <span className="sr-only">: {last.data.reference}</span>
          </button>
        </div>
      )}
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
          {cats.length > 0 && (
            <div className="tk-chips" role="group" aria-label="Show">
              <button
                type="button"
                aria-pressed={!chip}
                onClick={() => setChip(undefined)}
              >
                All
              </button>
              {cats.map((c) => (
                <button
                  key={c.tag}
                  type="button"
                  aria-pressed={chip === c.tag}
                  onClick={() => setChip(chip === c.tag ? undefined : c.tag)}
                >
                  {c.label}
                </button>
              ))}
            </div>
          )}
          <div className="tk-sheet-list tk-order-sections">
            {shown.length === 0 && (
              <KitEmpty icon="search" title="Nothing matches">
                Try another word, or show all.
              </KitEmpty>
            )}
            {sections.map((sec) => (
              <section
                key={sec.label || "all"}
                aria-label={sec.label || undefined}
              >
                {sec.label && <h3 className="tk-order-cat">{sec.label}</h3>}
                <ul className="tk-order-list tk-stagger">
                  {sec.items.map((it) => {
                    const n = qty[it.item_id] ?? 0;
                    return (
                      <li
                        key={it.item_id}
                        className={`tk-order-row${n > 0 ? " on" : ""}`}
                        data-testid="order-row"
                      >
                        <span
                          className="tk-row-tile"
                          aria-hidden="true"
                          data-tone="sand"
                        >
                          <Icon name="package" />
                        </span>
                        <span className="tk-row-words">
                          <span className="tk-row-name">{it.name}</span>
                          <span className="tk-row-sub">
                            {it.available ? orderLine(it) : "Not available now"}
                          </span>
                          {it.available && !!it.breaks?.length && (
                            <span className="tk-row-sub tk-breaks">
                              {breakWords(it)}
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
              </section>
            ))}
          </div>
        </>
      ) : (
        <div className="tk-sheet-list tk-form-body">
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
          <ul className="tk-lines">
            {bag.lines.map((l) => (
              <li key={l.item.item_id}>
                <span>
                  {l.quantity} × {l.item.name}
                  {priceAt(l.item, l.quantity) < l.item.price_kobo && (
                    <span className="tk-row-sub">
                      {" "}
                      at {formatPrice(priceAt(l.item, l.quantity))} for{" "}
                      {l.quantity}
                    </span>
                  )}
                </span>
                <b>{formatPrice(l.quantity * priceAt(l.item, l.quantity))}</b>
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
          <div className="tk-offer" data-testid="make-offer">
            <button
              type="button"
              className="tk-chip-btn"
              aria-pressed={offering}
              onClick={() => setOffering(!offering)}
            >
              {offering ? "Pay their price instead" : "Make an offer"}
            </button>
            {offering && (
              <KitField
                label="Your price for the whole order"
                hint={`Their price is ${formatPrice(bag.total)}. They can accept, counter or decline; it lapses in 48 hours.`}
                error={offerText.trim() ? offerProblem : undefined}
              >
                <input
                  inputMode="decimal"
                  autoComplete="off"
                  value={offerText}
                  onChange={(e) => setOfferText(e.target.value)}
                  placeholder="740,000"
                />
              </KitField>
            )}
            {offering && offerKobo > 0 && !offerProblem && (
              <p className="tk-form-now" aria-live="polite">
                {offWords(offerKobo, bag.total)} their price
              </p>
            )}
          </div>
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
 * the buyer can do now (confirm their quote, saying which of our items each
 * line is; accept their change; say all came, or how many did; cancel;
 * reorder), each as its permission allows. */
export function PurchaseView({
  purchase,
  onSaved,
  onReorder,
}: {
  purchase: Purchase;
  onSaved: (line: string | null) => void;
  onReorder?: (p: Purchase) => void;
}) {
  const { can } = useAuth();
  const manage = can("storefront.manage");
  const receive = can("table.edit");
  const { confirm, node } = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [mode, setMode] = useState<"" | "confirm" | "short">("");
  const st = purchaseState(purchase);
  const steps = buyerSteps(purchase);
  // Delivered and not yet said what came (a received order is done).
  const delivery = answersDelivery(purchase) && purchase.status !== "received";
  const own = useLoad(
    () =>
      mode === "confirm"
        ? get<StockView>("/v1/stock").then((r) => r.items)
        : Promise.resolve(undefined),
    [mode],
  );
  const [chosen, setChosen] = useState<Record<number, string>>({});
  const pick = (i: number) =>
    chosen[i] ?? sameName(own.data ?? [], purchase.lines[i]?.item ?? "");
  const picks = purchase.lines.map((_, i) => pick(i));
  const [counts, setCounts] = useState<Record<number, string>>({});
  const countOf = (i: number) =>
    counts[i] ??
    String(purchase.lines[i]?.delivered ?? purchase.lines[i]?.quantity ?? 0);
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
  // Their change, while it waits on us: its lines and what it comes to.
  const lines =
    steps.acceptChange && purchase.change ? purchase.change : purchase.lines;
  const total =
    steps.acceptChange && purchase.change
      ? purchase.change.reduce((n, l) => n + l.quantity * l.cost_kobo, 0)
      : purchase.total_kobo;
  const back = (
    <button
      type="button"
      className="tk-btn ghost"
      disabled={busy}
      onClick={() => (setMode(""), setError(undefined))}
    >
      Not now
    </button>
  );
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        <p className="tk-form-now tk-who-state">
          <span>{purchase.supplier}</span>
          <Pill tone={st.tone}>{st.word}</Pill>
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
        {steps.acceptChange && (
          <p className="tk-form-now">
            {purchase.supplier} changed it
            {purchase.counter ? `: “${purchase.counter}”` : ""}. It comes to{" "}
            {formatPrice(total)} now, from {formatPrice(purchase.total_kobo)}.
          </p>
        )}
        {mode === "" && (
          <ul className="tk-lines">
            {lines.map((l, i) => (
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
              <b>{formatPrice(total)}</b>
            </li>
          </ul>
        )}
        {mode === "confirm" &&
          (!own.data ? (
            own.error ? (
              <p className="tk-form-now">
                Keep your stock on Taskiem to confirm it, so what comes is
                received into your items.
              </p>
            ) : (
              <KitLoading rows={2} label="Loading your items…" />
            )
          ) : (
            <>
              <p className="tk-form-now">
                Which of your items is each? What comes is received into it.
              </p>
              {purchase.lines.map((l, i) => (
                <KitField key={i} label={`Their ${l.item}`}>
                  <select
                    value={pick(i)}
                    onChange={(e) =>
                      setChosen({ ...chosen, [i]: e.target.value })
                    }
                  >
                    <option value="">Choose your item</option>
                    {(own.data ?? []).map((it) => (
                      <option key={it.id} value={it.id}>
                        {it.name}
                      </option>
                    ))}
                  </select>
                </KitField>
              ))}
            </>
          ))}
        {mode === "short" && (
          <>
            <p className="tk-form-now">
              How many of each came? {purchase.supplier} is told, and agrees the
              count or talks to you.
            </p>
            {purchase.lines.map((l, i) => (
              <KitField key={i} label={`${l.item}: of ${l.quantity}`}>
                <input
                  inputMode="numeric"
                  value={countOf(i)}
                  onChange={(e) =>
                    setCounts({
                      ...counts,
                      [i]: e.target.value.replace(/\D/g, ""),
                    })
                  }
                />
              </KitField>
            ))}
          </>
        )}
        {purchase.seller_note && mode === "" && (
          <p className="tk-form-now">Their note: “{purchase.seller_note}”</p>
        )}
        {purchase.delivered_at && mode === "" && (
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
        {mode === "confirm" ? (
          <>
            {back}
            <button
              type="button"
              className="tk-cta"
              disabled={busy || !own.data || picks.some((x) => !x)}
              onClick={() =>
                void act(
                  "confirm-proposed",
                  `Confirmed ${purchase.reference}`,
                  { items: picks },
                )
              }
            >
              Confirm the order
            </button>
          </>
        ) : mode === "short" ? (
          <>
            {back}
            <button
              type="button"
              className="tk-cta"
              disabled={
                busy || purchase.lines.some((_, i) => countOf(i) === "")
              }
              onClick={() =>
                void act(
                  "not-all-came",
                  `Told ${purchase.supplier} what came of ${purchase.reference}`,
                  { counts: purchase.lines.map((_, i) => Number(countOf(i))) },
                )
              }
            >
              Tell {purchase.supplier}
            </button>
          </>
        ) : (
          <>
            {manage && steps.confirmProposed && (
              <button
                type="button"
                className="tk-cta"
                disabled={busy}
                onClick={() => setMode("confirm")}
              >
                Confirm their quote
              </button>
            )}
            {manage && steps.acceptChange && (
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
            {receive && delivery && (
              <>
                <button
                  type="button"
                  className="tk-btn ghost"
                  disabled={busy}
                  onClick={() => setMode("short")}
                >
                  Not all came
                </button>
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
              </>
            )}
            {manage && steps.cancel && !delivery && (
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
                {steps.confirmProposed ? "Decline" : "Cancel"}
              </button>
            )}
            {onReorder && !delivery && (
              <button
                type="button"
                className={
                  steps.confirmProposed || steps.acceptChange
                    ? "tk-btn ghost"
                    : "tk-cta"
                }
                onClick={() => onReorder(purchase)}
              >
                Reorder
              </button>
            )}
          </>
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
        {mode !== "" && (
          <button
            type="button"
            className="tk-btn ghost"
            disabled={busy}
            onClick={() => (setMode(""), setError(undefined))}
          >
            Not now
          </button>
        )}
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
