import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError, del, get, post, postOnce, put } from "../api";
import type { LinksView } from "../lib/links";
import { feeInput, feeKobo } from "../lib/orders";
import {
  lineProblem,
  priceWords,
  type ItemLink,
  type PriceItem,
  type PriceList,
  type SupplierPrices,
} from "../lib/pricelists";
import type { StockItem, StockView } from "../lib/stock";
import { formatPrice } from "../lib/storefront";
import {
  EmptyState,
  ErrorBox,
  Field,
  Items,
  Modal,
  PageHeader,
  Row,
  Skeleton,
  useAction,
  useLoad,
} from "../ui";
import { StorefrontTabs } from "./Storefront";

// Price lists between linked businesses (business links L4, §9). The
// seller's: lists built on its own stock items, shared with linked
// buyers, a buyer's own prices. The buyer's: a linked supplier's prices,
// ordered from at those prices, its own item said once for each.

/** The seller's price lists. */
export function StorefrontPriceLists() {
  const view = useLoad(
    () => get<{ lists: PriceList[] }>("/v1/price-lists"),
    [],
  );
  const links = useLoad(() => get<LinksView>("/v1/links"), []);
  const act = useAction();
  const [name, setName] = useState("");
  const [editing, setEditing] = useState<PriceList>();
  const [sharing, setSharing] = useState<PriceList>();
  const [forLink, setForLink] = useState<string>();
  const active = (links.data?.links ?? []).filter((l) => l.state === "active");
  const nameOf = (id: string) =>
    active.find((l) => l.id === id)?.name ?? "A business";
  const create = () =>
    void act
      .run(async () => {
        await post("/v1/price-lists", { name: name.trim() });
        setName("");
        view.reload();
      }, "Price list made")
      .then(() => undefined);
  return (
    <>
      <PageHeader
        title="Price lists"
        description="The prices linked businesses that buy from you see, from your own stock items. Share a list with them, and give one their own prices."
      />
      <StorefrontTabs />
      <ErrorBox error={view.error ?? act.error} />
      {!view.data && !view.error && (
        <Skeleton rows={3} label="Loading your price lists…" />
      )}
      {view.data && (
        <section className="card" aria-labelledby="price-lists">
          <h2 id="price-lists">Your price lists</h2>
          {view.data.lists.length === 0 ? (
            <EmptyState icon="money">
              No price lists yet. Make one, add items from your stock, and share
              it with the businesses that buy from you.
            </EmptyState>
          ) : (
            <Items label="Your price lists">
              {view.data.lists.map((l) => (
                <Row
                  key={l.id}
                  icon="money"
                  title={l.name}
                  sub={
                    <span className="sub">
                      {l.items.length} item{l.items.length === 1 ? "" : "s"} ·{" "}
                      {l.links.length === 0
                        ? "not shared"
                        : `shared with ${l.links.map(nameOf).join(", ")}`}
                    </span>
                  }
                  testid="price-list"
                >
                  <button
                    type="button"
                    className="small"
                    onClick={() => setEditing(l)}
                  >
                    Items<span className="sr-only"> of {l.name}</span>
                  </button>
                  <button
                    type="button"
                    className="small"
                    onClick={() => setSharing(l)}
                  >
                    Share<span className="sr-only"> {l.name}</span>
                  </button>
                  <button
                    type="button"
                    className="small danger"
                    disabled={act.busy}
                    onClick={() =>
                      confirm(
                        `Delete ${l.name}? The businesses it is shared with no longer see it. Open orders keep their prices.`,
                      ) &&
                      void act.run(
                        async () => (
                          await del(`/v1/price-lists/${l.id}`),
                          view.reload()
                        ),
                        "Deleted",
                      )
                    }
                  >
                    Delete<span className="sr-only"> {l.name}</span>
                  </button>
                </Row>
              ))}
            </Items>
          )}
          <form
            className="toolbar"
            style={{ marginTop: 12 }}
            onSubmit={(e) => {
              e.preventDefault();
              if (name.trim()) create();
            }}
          >
            <label className="grow">
              <span className="sr-only">New price list's name</span>
              <input
                value={name}
                maxLength={80}
                placeholder="Trade prices"
                onChange={(e) => setName(e.target.value)}
              />
            </label>
            <button
              type="submit"
              className="primary"
              disabled={act.busy || !name.trim()}
            >
              Make a price list
            </button>
          </form>
        </section>
      )}
      {view.data && view.data.lists.some((l) => l.links.length > 0) && (
        <section className="card" aria-labelledby="own-prices">
          <h2 id="own-prices">A business's own prices</h2>
          <p className="hint">
            Give one business its own price for an item on its list.
          </p>
          <Items label="Businesses with a price list">
            {view.data.lists.flatMap((l) =>
              l.links.map((id) => (
                <Row
                  key={id}
                  letter={nameOf(id)}
                  title={nameOf(id)}
                  sub={<span className="sub">{l.name}</span>}
                >
                  <button
                    type="button"
                    className="small"
                    onClick={() => setForLink(id)}
                  >
                    Their prices
                    <span className="sr-only"> for {nameOf(id)}</span>
                  </button>
                </Row>
              )),
            )}
          </Items>
        </section>
      )}
      {editing && (
        <ListItemsModal
          list={editing}
          onClose={() => setEditing(undefined)}
          onSaved={view.reload}
        />
      )}
      {sharing && (
        <ShareModal
          list={sharing}
          links={active}
          onClose={() => setSharing(undefined)}
          onSaved={view.reload}
        />
      )}
      {forLink && (
        <LinkPricesModal
          link={forLink}
          name={nameOf(forLink)}
          onClose={() => setForLink(undefined)}
        />
      )}
    </>
  );
}

interface ItemRow {
  item: StockItem;
  on: boolean;
  price: string;
  min: string;
  available: boolean;
}

/** A list's items: which of the business's own, at what price, at least
 * how many, and whether available. */
function ListItemsModal({
  list,
  onClose,
  onSaved,
}: {
  list: PriceList;
  onClose: () => void;
  onSaved: () => void;
}) {
  const stock = useLoad(() => get<StockView>("/v1/stock"), []);
  const [rows, setRows] = useState<ItemRow[]>();
  const act = useAction();
  useEffect(() => {
    if (!stock.data) return;
    setRows(
      stock.data.items.map((it) => {
        const on = list.items.find((x) => x.item_id === it.id);
        return {
          item: it,
          on: !!on,
          price: feeInput(on?.price_kobo ?? it.price_kobo ?? 0),
          min: String(on?.min_quantity ?? 1),
          available: on?.available ?? true,
        };
      }),
    );
  }, [stock.data, list]);
  const set = (i: number, patch: Partial<ItemRow>) =>
    setRows((cur) => cur?.map((r, j) => (j === i ? { ...r, ...patch } : r)));
  const bad = (rows ?? []).find(
    (r) =>
      r.on &&
      (!(feeKobo(r.price) > 0) || !/^\d+$/.test(r.min) || Number(r.min) < 1),
  );
  const save = () =>
    void act
      .run(async () => {
        await put(`/v1/price-lists/${list.id}/items`, {
          items: (rows ?? [])
            .filter((r) => r.on)
            .map((r) => ({
              item_id: r.item.id,
              price_kobo: feeKobo(r.price),
              min_quantity: Number(r.min),
              available: r.available,
            })),
        });
        onSaved();
      }, "Saved. The businesses it is shared with see it now.")
      .then((ok) => ok && onClose());
  return (
    <Modal title={`Items of ${list.name}`} onClose={onClose}>
      <ErrorBox
        error={
          stock.error instanceof ApiError && stock.error.status === 409
            ? new Error(
                "Keep your stock on Taskiem first: its items are what a price list sells.",
              )
            : (stock.error ?? act.error)
        }
      />
      {!rows ? (
        <Skeleton rows={3} label="Loading your items…" />
      ) : rows.length === 0 ? (
        <EmptyState icon="storefront">
          No stock items yet. Add them on the Stock page.
        </EmptyState>
      ) : (
        <div className="price-rows" data-testid="price-items">
          {rows.map((r, i) => (
            <fieldset key={r.item.id} className="price-row">
              <legend className="sr-only">{r.item.name}</legend>
              <label className="inline">
                <input
                  type="checkbox"
                  checked={r.on}
                  onChange={(e) => set(i, { on: e.target.checked })}
                />
                {r.item.name}
                {r.item.unit ? ` (${r.item.unit})` : ""}
              </label>
              {r.on && (
                <div className="price-fields">
                  <Field label="Price (₦)">
                    <input
                      inputMode="decimal"
                      value={r.price}
                      onChange={(e) => set(i, { price: e.target.value })}
                    />
                  </Field>
                  <Field label="At least">
                    <input
                      inputMode="numeric"
                      value={r.min}
                      onChange={(e) => set(i, { min: e.target.value })}
                    />
                  </Field>
                  <label className="inline">
                    <input
                      type="checkbox"
                      checked={r.available}
                      onChange={(e) => set(i, { available: e.target.checked })}
                    />
                    Available
                  </label>
                </div>
              )}
            </fieldset>
          ))}
        </div>
      )}
      {bad && (
        <p className="hint">
          {bad.item.name} needs a price and a whole number for “At least”.
        </p>
      )}
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={act.busy || !rows || !!bad}
          onClick={save}
        >
          Save
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** Which linked businesses see a list. */
function ShareModal({
  list,
  links,
  onClose,
  onSaved,
}: {
  list: PriceList;
  links: LinksView["links"];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [picked, setPicked] = useState<string[]>(list.links);
  const act = useAction();
  const save = () =>
    void act
      .run(async () => {
        await put(`/v1/price-lists/${list.id}/links`, { links: picked });
        onSaved();
      }, "Shared")
      .then((ok) => ok && onClose());
  return (
    <Modal title={`Share ${list.name}`} onClose={onClose}>
      <p className="hint">
        A business sees one price list: sharing this one with a business
        replaces the one it had.
      </p>
      {links.length === 0 ? (
        <EmptyState icon="chat">
          No linked businesses yet. Link with them on the Chat page.
        </EmptyState>
      ) : (
        <fieldset className="choice">
          <legend>Linked businesses</legend>
          {links.map((l) => (
            <label key={l.id} className="inline">
              <input
                type="checkbox"
                checked={picked.includes(l.id)}
                onChange={() =>
                  setPicked((cur) =>
                    cur.includes(l.id)
                      ? cur.filter((x) => x !== l.id)
                      : [...cur, l.id],
                  )
                }
              />
              {l.name}
            </label>
          ))}
        </fieldset>
      )}
      <ErrorBox error={act.error} />
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={act.busy}
          onClick={save}
        >
          Save
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** One business's own prices over its list's. */
function LinkPricesModal({
  link,
  name,
  onClose,
}: {
  link: string;
  name: string;
  onClose: () => void;
}) {
  const view = useLoad(
    () => get<{ list?: string; items: PriceItem[] }>(`/v1/link-prices/${link}`),
    [link],
  );
  const [prices, setPrices] = useState<Record<string, string>>({});
  const act = useAction();
  useEffect(() => {
    if (!view.data) return;
    setPrices(
      Object.fromEntries(
        view.data.items
          .filter((it) => it.overridden)
          .map((it) => [it.item_id, feeInput(it.price_kobo)]),
      ),
    );
  }, [view.data]);
  const bad = Object.values(prices).some(
    (p) => p.trim() !== "" && !(feeKobo(p) > 0),
  );
  const save = () =>
    void act
      .run(async () => {
        const out: Record<string, number> = {};
        for (const [id, p] of Object.entries(prices))
          if (p.trim() !== "") out[id] = feeKobo(p);
        await put(`/v1/link-prices/${link}`, { prices: out });
      }, `${name}'s prices saved`)
      .then((ok) => ok && onClose());
  return (
    <Modal title={`${name}'s prices`} onClose={onClose}>
      <p className="hint">Leave a price empty for the list's.</p>
      <ErrorBox error={view.error ?? act.error} />
      {!view.data ? (
        <Skeleton rows={2} label="Loading their prices…" />
      ) : (
        view.data.items.map((it) => (
          <Field
            key={it.item_id}
            label={`${it.name} (list: ${formatPrice(view.data?.items.find((x) => x.item_id === it.item_id && !x.overridden)?.price_kobo ?? it.price_kobo)})`}
          >
            <input
              inputMode="decimal"
              value={prices[it.item_id] ?? ""}
              placeholder="The list's"
              onChange={(e) =>
                setPrices((cur) => ({ ...cur, [it.item_id]: e.target.value }))
              }
            />
          </Field>
        ))
      )}
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={act.busy || bad || !view.data}
          onClick={save}
        >
          Save
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** A linked supplier's prices, ordered from. */
export function SupplierPricesPage() {
  const { link = "" } = useParams();
  const navigate = useNavigate();
  const view = useLoad(
    () =>
      get<{ suppliers: SupplierPrices[] }>(
        `/v1/supplier-prices?link=${encodeURIComponent(link)}`,
      ),
    [link],
  );
  const stock = useLoad(() => get<StockView>("/v1/stock"), []);
  const linked = useLoad(
    () => get<{ item_links: ItemLink[] }>(`/v1/item-links/${link}`),
    [link],
  );
  const [qty, setQty] = useState<Record<string, string>>({});
  const [mine, setMine] = useState<Record<string, string>>({});
  const act = useAction();
  const p = view.data?.suppliers[0];
  const own = stock.data?.items ?? [];
  const linkOf = (seller: string) =>
    linked.data?.item_links.find((l) => l.seller_item_id === seller)?.item_id;
  const lines = (p?.items ?? []).filter(
    (it) => (qty[it.item_id] ?? "").trim() !== "",
  );
  const problems = lines
    .map((it) => lineProblem(it, qty[it.item_id] ?? ""))
    .filter(Boolean);
  const unlinked = lines.filter(
    (it) => !linkOf(it.item_id) && !mine[it.item_id],
  );
  const total = lines.reduce(
    (sum, it) => sum + it.price_kobo * (Number(qty[it.item_id]) || 0),
    0,
  );
  const order = () =>
    void act
      .run(async () => {
        const out = await postOnce<{ purchase: { id: string } }>(
          `/v1/supplier-prices/${link}/order`,
          `prices-${link}-${lines.map((it) => `${it.item_id}:${qty[it.item_id]}`).join(",")}-${Date.now()}`,
          {
            lines: lines.map((it) => ({
              seller_item_id: it.item_id,
              quantity: Number(qty[it.item_id]),
              item_id: mine[it.item_id] || undefined,
            })),
          },
        );
        navigate(`/purchases/${out.purchase.id}`);
      }, false)
      .then(() => undefined);
  return (
    <>
      <PageHeader
        title={p ? `${p.business}'s prices` : "Prices"}
        description="What this linked supplier charges you. Order from it at these prices; the order goes to them when you send it."
        actions={
          <Link className="button" to="/storefront/suppliers">
            Suppliers
          </Link>
        }
      />
      <ErrorBox error={view.error ?? stock.error ?? act.error} />
      {!view.data && !view.error && (
        <Skeleton rows={3} label="Loading their prices…" />
      )}
      {view.data && !p && (
        <EmptyState icon="money">
          This business shares no price list with you now.
        </EmptyState>
      )}
      {p && (
        <section className="card" aria-labelledby="their-prices">
          <h2 id="their-prices">{p.name}</h2>
          <div>
            <table>
              <thead>
                <tr>
                  <th scope="col">Item</th>
                  <th scope="col">Price</th>
                  <th scope="col">How many</th>
                </tr>
              </thead>
              <tbody>
                {p.items.map((it) => {
                  const problem = lineProblem(it, qty[it.item_id] ?? "");
                  const mineID = linkOf(it.item_id);
                  const asking =
                    (qty[it.item_id] ?? "").trim() !== "" && !mineID;
                  return (
                    <tr key={it.item_id} data-testid="supplier-price">
                      <td>
                        {it.name}
                        {mineID && (
                          <span className="block hint">
                            Your{" "}
                            {own.find((o) => o.id === mineID)?.name ?? "item"}
                          </span>
                        )}
                        {asking && (
                          <label className="block">
                            <span className="hint">
                              Which of your items is it?
                            </span>
                            <select
                              value={mine[it.item_id] ?? ""}
                              onChange={(e) =>
                                setMine((cur) => ({
                                  ...cur,
                                  [it.item_id]: e.target.value,
                                }))
                              }
                            >
                              <option value="">Choose…</option>
                              {own.map((o) => (
                                <option key={o.id} value={o.id}>
                                  {o.name}
                                </option>
                              ))}
                            </select>
                          </label>
                        )}
                      </td>
                      <td>{priceWords(it)}</td>
                      <td>
                        <input
                          aria-label={`How many ${it.name}`}
                          inputMode="numeric"
                          size={5}
                          disabled={!it.available}
                          value={qty[it.item_id] ?? ""}
                          onChange={(e) =>
                            setQty((cur) => ({
                              ...cur,
                              [it.item_id]: e.target.value,
                            }))
                          }
                        />
                        {problem && (
                          <span className="block hint">{problem}</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          <div className="toolbar" style={{ marginTop: 12 }}>
            <strong className="grow">Total {formatPrice(total)}</strong>
            <button
              type="button"
              className="primary"
              disabled={
                act.busy ||
                lines.length === 0 ||
                problems.length > 0 ||
                unlinked.length > 0
              }
              onClick={order}
            >
              Draft the order
            </button>
          </div>
          {unlinked.length > 0 && (
            <p className="hint">
              Say which of your items{" "}
              {unlinked.map((it) => it.name).join(" and ")} is, once: it is
              remembered.
            </p>
          )}
        </section>
      )}
    </>
  );
}
