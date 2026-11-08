import { useState } from "react";
import { del, get, patch, post, postOnce, put, sendWith } from "../api";
import { FormFrame, formKey as key, type Saver } from "./FormFrame";
import { GridStockIn, SizesEditor } from "./SizesForms";
import { hasSizes, sizesLine, sizesWords } from "../lib/variants";
import {
  PendingPhotos,
  PhotoFailures,
  ProductPhotos,
  sendAll,
  SendingLine,
  type Sending,
} from "./ProductPhotos";
import { photoCount, type PhotoPick } from "../lib/photos";
import { useConfirm } from "./ConfirmSheet";
import { KitEmpty, KitField, KitLoading, Pill } from "../kit";
import { serviceRow } from "../lib/bookings";
import type { Service, ServiceColumns } from "../lib/bookings";
import { ORDER_STEPS } from "../lib/cardframe";
import {
  itemsWithPrice,
  listFor,
  listLabel,
  sharedWords,
  type PriceList,
} from "../lib/pricelists";
import type { BusinessLink } from "../lib/links";
import { useAuth } from "../auth";
import { quantity, type StockItem, type StockView } from "../lib/stock";
import { categoriesOf } from "../lib/mentions";
import { formatPrice, type Storefront } from "../lib/storefront";
import { cellText, cellValue, type Row, type Table } from "../lib/tables";
import type { TradeOrder } from "../lib/tradeorders";
import {
  nairaKobo,
  nairaText,
  orderLine,
  productLine,
  serviceLine,
  stockInLine,
} from "../lib/toolforms";
import { ErrorBox, useLoad } from "../ui";
import type { Prefill } from "../lib/intents";

// The chat tools' forms (docs/spec/chat-tools.md §4): each does the common
// task in the sheet and saves through the same API as the page; the
// server tells the change as a diary line (chat_changes.go). What a form
// gives back is the words for the toast. Someone who may not change it
// sees it, with no Save.

/** What a form does on Save: words for what changed, or null for nothing. */

/** A chat tool's write, marked so a table row added is told as a change. */
const toolPost = <T,>(path: string, body: unknown) =>
  sendWith<T>("POST", path, body, { "X-Taskiem-Diary": "1" });
const toolPatch = <T,>(path: string, body: unknown) =>
  sendWith<T>("PATCH", path, body, { "X-Taskiem-Diary": "1" });

/** − n + for a count. */
function Stepper({
  label,
  value,
  onChange,
}: {
  label: string;
  value: number;
  onChange: (n: number) => void;
}) {
  return (
    <div className="tk-stepper" role="group" aria-label={label}>
      <button
        type="button"
        aria-label="One fewer"
        onClick={() => onChange(Math.max(0, value - 1))}
      >
        −
      </button>
      <input
        inputMode="numeric"
        aria-label={label}
        value={String(value)}
        onChange={(e) =>
          onChange(Math.max(0, Number(e.target.value.replace(/\D/g, "")) || 0))
        }
      />
      <button
        type="button"
        aria-label="One more"
        onClick={() => onChange(value + 1)}
      >
        +
      </button>
    </div>
  );
}

function Money({
  label,
  value,
  onChange,
  hint,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  hint?: string;
}) {
  return (
    <KitField label={label} hint={hint}>
      <span className="tk-money">
        <span aria-hidden="true">₦</span>
        <input
          inputMode="decimal"
          value={value}
          aria-label={`${label} in naira`}
          onChange={(e) => onChange(e.target.value)}
        />
      </span>
    </KitField>
  );
}

/** An on/off switch with its words, as the designs draw it. */
function Switch({
  label,
  hint,
  on,
  onChange,
}: {
  label: string;
  hint?: string;
  on: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="tk-form-row">
      <span>
        <span className="tk-form-row-title">{label}</span>
        {hint && <span className="tk-form-row-sub">{hint}</span>}
      </span>
      <button
        type="button"
        role="switch"
        aria-checked={on}
        aria-label={label}
        className="tk-switch"
        onClick={() => onChange(!on)}
      >
        <span />
      </button>
    </div>
  );
}

/** A product's category (docs/spec/chat-categories-and-variants.md): any
 * word the owner types, with the categories already used offered as chips
 * ("Sneakers · 48"), so spellings don't drift. */
function CategoryField({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const used = useLoad(
    () =>
      get<StockView>("/v1/stock").then(
        (r) => categoriesOf(r.items),
        () => [],
      ),
    [],
  );
  const q = value.trim().toLowerCase();
  const offer = (used.data ?? [])
    .filter(
      (c) =>
        c.name.toLowerCase() !== q &&
        (q === "" || c.name.toLowerCase().includes(q)),
    )
    .slice(0, 6);
  return (
    <div className="tk-category">
      <KitField
        label="Category"
        hint="Sneakers, Soft drinks… Search and the chips use it."
      >
        <input
          value={value}
          maxLength={60}
          placeholder="None"
          onChange={(e) => onChange(e.target.value)}
        />
      </KitField>
      {offer.length > 0 && (
        <div
          className="tk-chips tk-field-chips"
          role="group"
          aria-label="Categories you use"
        >
          {offer.map((c) => (
            <button key={c.name} type="button" onClick={() => onChange(c.name)}>
              {c.name} · {c.count}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** A product: its photo, price, count on the shelf, and whether it shows. */
export function ProductForm({
  item,
  onSaved,
  prefill,
  canEdit = true,
  onCancel,
  link,
}: {
  item: StockItem;
  onSaved: (line: string | null) => void;
  prefill?: Prefill;
  canEdit?: boolean;
  onCancel?: () => void;
  /** The business whose conversation it was opened from (its list first). */
  link?: string;
}) {
  // Its sizes and colours, set up in place of the form (chat categories
  // Part B).
  const [sizesOpen, setSizesOpen] = useState(false);
  const [name, setName] = useState(item.name);
  // Its price on a price list, and who the list is shared with (the
  // DeskSeller board): for those who keep the storefront's prices.
  const { can } = useAuth();
  // A product sold in sizes and colours is priced on the lists through them.
  const trade = can("storefront.manage") && !hasSizes(item);
  const lists = useLoad(
    () =>
      trade
        ? get<{ lists: PriceList[] }>("/v1/price-lists").then((r) => r.lists)
        : Promise.resolve([] as PriceList[]),
    [],
  );
  const linked = useLoad(
    () =>
      trade
        ? get<{ links: BusinessLink[] }>("/v1/links").then((r) =>
            r.links.filter((l) => l.state === "active"),
          )
        : Promise.resolve([] as BusinessLink[]),
    [],
  );
  // The lowest price staff may agree to (phase 7); under it, the owner's OK.
  const floors = useLoad(
    () =>
      trade
        ? get<{ floors: Record<string, number> }>("/v1/trade-floors").then(
            (r) => r.floors,
          )
        : Promise.resolve({} as Record<string, number>),
    [],
  );
  const floorWas = floors.data?.[item.id];
  const [floorText, setFloorText] = useState<string | null>(null);
  const floorShown =
    floorText ?? (floorWas !== undefined ? nairaText(floorWas) : "");
  const floorKobo = floorShown.trim() === "" ? 0 : nairaKobo(floorShown);
  const [listId, setListId] = useState<string>();
  const chosen =
    lists.data &&
    (lists.data.find((l) => l.id === listId) ??
      listFor(lists.data, item.id, link));
  const onList = chosen?.items.find((x) => x.item_id === item.id);
  const [tradePrice, setTradePrice] = useState<string | null>(null);
  const tradeShown = tradePrice ?? (onList ? nairaText(onList.price_kobo) : "");
  const tradeKobo = nairaKobo(tradeShown);
  const [share, setShare] = useState<string[] | null>(null);
  const sharing = share ?? chosen?.links ?? [];
  const [picking, setPicking] = useState(false);
  const names = new Map((linked.data ?? []).map((l) => [l.id, l.name]));
  const [price, setPrice] = useState(
    prefill?.priceKobo !== undefined
      ? nairaText(prefill.priceKobo)
      : item.price_kobo !== undefined
        ? nairaText(item.price_kobo)
        : "",
  );
  // The count is sent only when it was changed (an item may have none).
  const [count, setCount] = useState<number | null>(item.stock);
  const [shown, setShown] = useState(item.available);
  const [category, setCategory] = useState(item.category ?? "");
  const kobo = nairaKobo(price);
  const save: Saver = async () => {
    const said: string[] = [];
    const change: Record<string, unknown> = {};
    if (!Number.isNaN(kobo) && kobo !== item.price_kobo)
      change.price_kobo = kobo;
    if (name.trim() && name.trim() !== item.name) change.name = name.trim();
    if (shown !== item.available) change.available = shown;
    if (category.trim() !== (item.category ?? ""))
      change.category = category.trim();
    if (Object.keys(change).length) {
      await patch(`/v1/stock/items/${encodeURIComponent(item.id)}`, change);
      said.push("Saved");
    }
    if (
      chosen &&
      tradePrice !== null &&
      tradeShown.trim() !== "" &&
      !Number.isNaN(tradeKobo) &&
      tradeKobo !== onList?.price_kobo
    ) {
      await put(`/v1/price-lists/${chosen.id}/items`, {
        items: itemsWithPrice(chosen, item.id, tradeKobo),
      });
      said.push("Priced");
    }
    if (
      chosen &&
      share !== null &&
      [...share].sort().join() !== [...chosen.links].sort().join()
    ) {
      await put(`/v1/price-lists/${chosen.id}/links`, { links: share });
      said.push("Shared");
    }
    if (
      floors.data &&
      floorText !== null &&
      !Number.isNaN(floorKobo) &&
      floorKobo !== (floorWas ?? 0)
    ) {
      await put("/v1/trade-floors", {
        item_id: item.id,
        floor_kobo: floorKobo,
      });
      said.push("Lowest price set");
    }
    if (count !== null && count !== item.stock) {
      await postOnce("/v1/stock/movements", key(), {
        item_id: item.id,
        reason: "count",
        count,
        note: "Counted in chat",
      });
      said.push("Counted");
    }
    return said.length ? `${name.trim() || item.name}: saved` : null;
  };
  if (sizesOpen)
    return (
      <SizesEditor
        item={item}
        onSaved={onSaved}
        onCancel={() => setSizesOpen(false)}
      />
    );
  return (
    <FormFrame
      save={save}
      onSaved={onSaved}
      canEdit={canEdit}
      onCancel={onCancel}
      ready={
        name.trim() !== "" &&
        (price.trim() === "" || !Number.isNaN(kobo)) &&
        (tradeShown.trim() === "" || !Number.isNaN(tradeKobo)) &&
        !Number.isNaN(floorKobo)
      }
    >
      <ProductPhotos row={item.id} name={item.name} canEdit={canEdit} />
      <KitField label="Name">
        <input
          value={name}
          maxLength={120}
          onChange={(e) => setName(e.target.value)}
        />
      </KitField>
      <CategoryField value={category} onChange={setCategory} />
      {chosen ? (
        <>
          <div className="tk-form-two">
            <Money label="Storefront price" value={price} onChange={setPrice} />
            <Money
              label={listLabel(chosen.name)}
              value={tradeShown}
              onChange={setTradePrice}
            />
          </div>
          {(lists.data?.length ?? 0) > 1 && (
            <label className="tk-list-pick">
              Price list
              <select
                value={chosen.id}
                onChange={(e) => {
                  setListId(e.target.value);
                  setTradePrice(null);
                  setShare(null);
                  setPicking(false);
                }}
              >
                {lists.data?.map((l) => (
                  <option key={l.id} value={l.id}>
                    {l.name}
                  </option>
                ))}
              </select>
            </label>
          )}
          <div className="tk-share-box">
            <span>
              <span className="tk-form-row-title">Shared with</span>
              <span className="tk-form-row-sub" data-testid="shared-with">
                {sharedWords(sharing, names)}
              </span>
            </span>
            <button
              type="button"
              className="tk-share-change"
              aria-expanded={picking}
              onClick={() => setPicking(!picking)}
            >
              {picking ? "Done" : "Change"}
              <span className="sr-only"> who sees the {chosen.name} list</span>
            </button>
          </div>
          {picking && (
            <fieldset className="tk-share-pick">
              <legend className="sr-only">
                Who sees the {chosen.name} list
              </legend>
              {(linked.data ?? []).length === 0 ? (
                <p className="tk-form-row-sub">
                  No linked businesses yet. Link with a business to share a
                  list.
                </p>
              ) : (
                (linked.data ?? []).map((l) => (
                  <label key={l.id} className="tk-share-row">
                    <input
                      type="checkbox"
                      checked={sharing.includes(l.id)}
                      onChange={(e) =>
                        setShare(
                          e.target.checked
                            ? [...sharing, l.id]
                            : sharing.filter((x) => x !== l.id),
                        )
                      }
                    />
                    {l.name}
                  </label>
                ))
              )}
            </fieldset>
          )}
          {floors.data && (
            <Money
              label="Lowest price"
              value={floorShown}
              onChange={setFloorText}
              hint={
                floorKobo > 0 && tradeKobo > 0 && floorKobo > tradeKobo
                  ? "Higher than the trade price: every offer will wait for you."
                  : "Staff can agree offers down to this. Under it, an offer waits for your OK. Buyers never see it."
              }
            />
          )}
        </>
      ) : (
        <Money label="Price" value={price} onChange={setPrice} />
      )}
      {hasSizes(item) ? null : (
        <div className="tk-form-row">
          <span>
            <span className="tk-form-row-title">In stock</span>
            <span className="tk-form-row-sub">
              {count === null
                ? "No count yet: set one"
                : "Counts down as orders are accepted"}
            </span>
          </span>
          <Stepper label="In stock" value={count ?? 0} onChange={setCount} />
        </div>
      )}
      <SizesRow
        item={item}
        canEdit={canEdit}
        onOpen={() => setSizesOpen(true)}
      />
      <Switch
        label="Show on your storefront"
        hint="And on price lists you share"
        on={shown}
        onChange={setShown}
      />
      {chosen && (
        <p className="tk-share-note">
          <b>Saving posts a line in this chat</b> and updates the price for
          every business on the {listLabel(chosen.name)}.
        </p>
      )}
    </FormFrame>
  );
}

/** A product's sizes and colours, in its form: how many, and the way to
 * set them up or change them. */
function SizesRow({
  item,
  canEdit,
  onOpen,
}: {
  item: StockItem;
  canEdit: boolean;
  onOpen: () => void;
}) {
  const all = useLoad(
    () =>
      hasSizes(item)
        ? get<StockView>("/v1/stock").then((r) => r.items)
        : Promise.resolve([] as StockItem[]),
    [item.id],
  );
  const line =
    hasSizes(item) && all.data ? sizesLine(all.data, item.id) : undefined;
  return (
    <div className="tk-form-row">
      <span>
        <span className="tk-form-row-title">Sizes and colours</span>
        <span className="tk-form-row-sub">
          {!hasSizes(item)
            ? "A count for each size and colour"
            : line
              ? `${sizesWords(line.count)}${line.stock !== null ? ` · ${line.stock} in stock` : ""}${line.low ? ` · ${line.low} low` : ""}`
              : "Loading…"}
        </span>
      </span>
      {canEdit && (
        <button type="button" className="tk-btn soft" onClick={onOpen}>
          {hasSizes(item) ? "Change" : "Set up"}
          <span className="sr-only"> sizes and colours</span>
        </button>
      )}
    </div>
  );
}

/** A new product: its name, price, unit and how many there are. */
export function ProductAdd({
  onSaved,
}: {
  onSaved: (line: string | null) => void;
}) {
  const [name, setName] = useState("");
  const [price, setPrice] = useState("");
  const [unit, setUnit] = useState("");
  const [category, setCategory] = useState("");
  const [count, setCount] = useState(0);
  // Its photos (chat categories Part C): held until the product exists,
  // then sent to it. One that does not go leaves the product added and
  // the form open, to send it again.
  const { can } = useAuth();
  const photosOK = can("storefront.manage");
  const [picks, setPicks] = useState<PhotoPick[]>([]);
  const [added, setAdded] = useState<string>();
  const [failed, setFailed] = useState<{ pick: PhotoPick; why: string }[]>([]);
  const [sending, setSending] = useState<Sending>();
  const kobo = nairaKobo(price);
  const save: Saver = async () => {
    let id = added;
    if (!id) {
      const out = await postOnce<{ item: { id: string } }>(
        "/v1/stock/items",
        key(),
        {
          name: name.trim(),
          price_kobo: kobo,
          unit: unit.trim(),
          quantity: count,
          ...(category.trim() ? { category: category.trim() } : {}),
        },
      );
      id = out.item.id;
    }
    const todo = added ? failed.map((f) => f.pick) : photosOK ? picks : [];
    if (todo.length) {
      setSending({ done: 0, of: todo.length });
      const bad = await sendAll(
        id,
        todo,
        added ? picks.length - failed.length : 0,
        () => setSending((x) => x && { ...x, done: x.done + 1 }),
      ).finally(() => setSending(undefined));
      await post("/v1/storefront/catalogue/refresh").catch(() => undefined);
      if (bad.length) {
        setAdded(id);
        setFailed(bad);
        throw new Error(
          `${name.trim()} is added, but ${bad.length === 1 ? "a photo" : `${bad.length} photos`} did not go. Press Send the photos again, or close: the product stays.`,
        );
      }
    }
    return productLine(name.trim(), kobo, unit.trim() || undefined);
  };
  return (
    <FormFrame
      save={save}
      onSaved={onSaved}
      saveWords={added ? "Send the photos again" : "Add product"}
      ready={name.trim() !== "" && kobo > 0}
    >
      <SendingLine sending={sending} />
      {photosOK &&
        (added ? (
          <PhotoFailures failed={failed} onRetry={() => undefined} hideRetry />
        ) : (
          <PendingPhotos picks={picks} onChange={setPicks} />
        ))}
      {added ? (
        // Added: what is left is its photos, said above; nothing else to change here.
        <p className="tk-form-now">{name.trim()} is in your products.</p>
      ) : (
        <>
          <KitField label="Name">
            <input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Rice, 50kg bag"
            />
          </KitField>
          <CategoryField value={category} onChange={setCategory} />
          <Money label="Price" value={price} onChange={setPrice} />
          <KitField label="Sold by the" hint="bag, carton, piece…">
            <input value={unit} onChange={(e) => setUnit(e.target.value)} />
          </KitField>
          <div className="tk-form-row">
            <span className="tk-form-row-title">In stock now</span>
            <Stepper label="In stock now" value={count} onChange={setCount} />
          </div>
        </>
      )}
    </FormFrame>
  );
}

/** Goods that came in. */
export function StockInForm({
  item,
  onSaved,
  prefill,
  canEdit = true,
}: {
  item: StockItem;
  onSaved: (line: string | null) => void;
  prefill?: Prefill;
  canEdit?: boolean;
}) {
  const [n, setN] = useState(prefill?.count ?? 1);
  const [note, setNote] = useState("");
  // A product with sizes and colours: goods received by its grid.
  if (hasSizes(item))
    return <GridStockIn item={item} onSaved={onSaved} canEdit={canEdit} />;
  const save: Saver = async () => {
    await postOnce("/v1/stock/movements", key(), {
      item_id: item.id,
      reason: "received",
      change: n,
      note: note.trim() || "Recorded in chat",
    });
    return stockInLine(item.name, n, item.stock, item.unit);
  };
  return (
    <FormFrame
      save={save}
      onSaved={onSaved}
      saveWords="Record stock in"
      ready={n > 0}
      canEdit={canEdit}
    >
      <p className="tk-form-now">
        Now:{" "}
        {item.stock === null ? "no count yet" : quantity(item.stock, item.unit)}
      </p>
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

type SF = { services_table?: string | null; services_columns?: ServiceColumns };

/** A service, new or as it is: its name, how long it takes and its price. */
export function ServiceForm({
  service,
  onSaved,
  prefill,
  canEdit = true,
}: {
  service?: Service;
  onSaved: (line: string | null) => void;
  prefill?: Prefill;
  canEdit?: boolean;
}) {
  const sf = useLoad(() => get<SF>("/v1/storefront"), []);
  const { confirm, node } = useConfirm();
  const [name, setName] = useState(service?.name ?? prefill?.name ?? "");
  const [minutes, setMinutes] = useState(
    service?.duration_minutes ?? prefill?.minutes ?? 30,
  );
  const [price, setPrice] = useState(
    service?.price_kobo != null
      ? nairaText(service.price_kobo)
      : prefill?.priceKobo !== undefined
        ? nairaText(prefill.priceKobo)
        : "",
  );
  if (!sf.data)
    return sf.error ? <ErrorBox error={sf.error} /> : <KitLoading />;
  const table = sf.data.services_table;
  if (!table)
    return (
      <KitEmpty icon="clock" title="Services are not set up yet">
        Open services to choose where your services are kept.
      </KitEmpty>
    );
  const cols = sf.data.services_columns ?? {};
  const save: Saver = async () => {
    const made = serviceRow(cols, name, String(minutes), price);
    if ("error" in made) throw new Error(made.error);
    if (service)
      await patch(
        `/v1/tables/${table}/rows/${encodeURIComponent(service.id)}`,
        { set: made.row },
      );
    else await post(`/v1/tables/${table}/rows`, { row: made.row });
    await post("/v1/storefront/catalogue/refresh");
    const kobo = nairaKobo(price);
    return service
      ? `${name.trim()}: saved`
      : serviceLine(name.trim(), minutes, Number.isNaN(kobo) ? null : kobo);
  };
  const remove = async () => {
    if (
      !service ||
      !(await confirm({
        title: `Remove ${service.name}?`,
        body: "Customers can no longer book it.",
        ok: "Remove",
        danger: true,
      }))
    )
      return;
    await del(`/v1/tables/${table}/rows/${encodeURIComponent(service.id)}`);
    await post("/v1/storefront/catalogue/refresh");
    onSaved(`${service.name}: removed`);
  };
  return (
    <>
      <FormFrame
        save={save}
        onSaved={onSaved}
        saveWords={service ? "Save" : "Add service"}
        ready={name.trim() !== "" && minutes > 0}
        canEdit={canEdit}
      >
        <KitField label="Name">
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Rice milling, per bag"
          />
        </KitField>
        <div className="tk-form-row">
          <span>
            <span className="tk-form-row-title">Takes</span>
            <span className="tk-form-row-sub">Minutes</span>
          </span>
          <Stepper
            label="Minutes it takes"
            value={minutes}
            onChange={setMinutes}
          />
        </div>
        <Money label="Price" value={price} onChange={setPrice} />
        {service && canEdit && (
          <button
            type="button"
            className="tk-btn ghost tk-danger"
            onClick={() => void remove()}
          >
            Remove this service
          </button>
        )}
      </FormFrame>
      {node}
    </>
  );
}

/** A price list's prices. */
export function PriceListForm({
  list,
  onSaved,
  canEdit = true,
}: {
  list: PriceList;
  onSaved: (line: string | null) => void;
  canEdit?: boolean;
}) {
  const [prices, setPrices] = useState(() =>
    list.items.map((it) => nairaText(it.price_kobo)),
  );
  const bad = prices.some((p) => !(nairaKobo(p) > 0));
  const save: Saver = async () => {
    let changed = 0;
    const items = list.items.map((it, i) => {
      const to = nairaKobo(prices[i] ?? "");
      if (to !== it.price_kobo) changed++;
      return {
        item_id: it.item_id,
        price_kobo: to,
        min_quantity: it.min_quantity,
        available: it.available,
      };
    });
    if (changed === 0) return null;
    await put(`/v1/price-lists/${list.id}/items`, { items });
    return `${list.name}: saved`;
  };
  if (list.items.length === 0)
    return (
      <KitEmpty icon="tag" title="Nothing on this list yet">
        Open price lists to add items to it.
      </KitEmpty>
    );
  return (
    <FormFrame save={save} onSaved={onSaved} ready={!bad} canEdit={canEdit}>
      {list.items.map((it, i) => (
        <Money
          key={it.item_id}
          label={`${it.name}${it.unit ? `, a ${it.unit}` : ""}`}
          value={prices[i] ?? ""}
          onChange={(v) =>
            setPrices((cur) => cur.map((p, j) => (j === i ? v : p)))
          }
        />
      ))}
    </FormFrame>
  );
}

const STATUS: Record<string, string> = {
  received: "New",
  changed: "Changed",
  proposed: "Draft",
  accepted: "Accepted",
  ready: "Ready",
  dispatched: "On the way",
  delivered: "Delivered",
  declined: "Declined",
  cancelled: "Cancelled",
};
/** Where an order is on Placed, Accepted, On the way, Delivered. */
const STEP: Record<string, number> = {
  received: 0,
  changed: 0,
  proposed: 0,
  accepted: 1,
  ready: 1,
  dispatched: 2,
  delivered: 3,
};

/** An order from a linked business: what it asks for, where it is, Accept or Decline. */
export function OrderView({
  order,
  onSaved,
  canEdit = true,
}: {
  order: TradeOrder;
  onSaved: (line: string | null) => void;
  canEdit?: boolean;
}) {
  const { confirm, node } = useConfirm();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const open =
    canEdit && (order.status === "received" || order.status === "changed");
  const step = STEP[order.status];
  const act = async (s: "accept" | "decline") => {
    if (
      s === "decline" &&
      !(await confirm({
        title: `Decline ${order.order_reference}?`,
        ok: "Decline",
        danger: true,
      }))
    )
      return;
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
          {order.buyer_name} ·{" "}
          <Pill
            tone={
              order.status === "received"
                ? "lime"
                : order.status === "declined" || order.status === "cancelled"
                  ? "danger"
                  : "plain"
            }
          >
            {STATUS[order.status] ?? order.status}
          </Pill>
        </p>
        {step !== undefined && (
          <span className="tk-progress">
            <span
              className="tk-progress-bar"
              role="img"
              aria-label={`${ORDER_STEPS[step]}: step ${step + 1} of ${ORDER_STEPS.length}`}
            >
              {ORDER_STEPS.map((w, i) => (
                <span key={w} className={i <= step ? "done" : ""} />
              ))}
            </span>
            <span className="tk-progress-words" aria-hidden="true">
              {ORDER_STEPS.map((w, i) => (
                <span key={w} className={i === step ? "now" : ""}>
                  {w}
                </span>
              ))}
            </span>
          </span>
        )}
        <ul className="tk-lines">
          {order.lines.map((l, i) => (
            <li key={i}>
              <span>
                {l.quantity} × {l.item}
              </span>
              <b>
                {l.price_kobo
                  ? formatPrice(l.quantity * l.price_kobo)
                  : "No price"}
              </b>
            </li>
          ))}
          <li className="tk-lines-total">
            <span>Total</span>
            <b>{formatPrice(order.total_kobo)}</b>
          </li>
        </ul>
        {order.note && (
          <p className="tk-form-now">Their note: “{order.note}”</p>
        )}
        <ErrorBox error={error} />
      </div>
      {open && (
        <div className="tk-sheet-foot tk-foot-two">
          <button
            type="button"
            className="tk-btn ghost"
            disabled={busy}
            onClick={() => void act("decline")}
          >
            Decline
          </button>
          <button
            type="button"
            className="tk-cta"
            disabled={busy}
            onClick={() => void act("accept")}
          >
            Accept
          </button>
        </div>
      )}
      {node}
    </>
  );
}

/** Any data table: its latest rows, a row changed, a row added. */
export function TableTool({
  table,
  onSaved,
  canEdit = true,
}: {
  table: Table;
  onSaved: (line: string | null) => void;
  canEdit?: boolean;
}) {
  const rows = useLoad(
    () =>
      get<{ rows: Row[] }>(
        `/v1/tables/${encodeURIComponent(table.id)}/rows?limit=20`,
      ),
    [table.id],
  );
  const [editing, setEditing] = useState<Row | "new">();
  const [values, setValues] = useState<Record<string, string>>({});
  // The shop's products table: its photo column takes photos (the camera,
  // the device, a link), not a web address typed as text (chat categories
  // Part C). It is the storefront's to write, so it is left out of the row.
  const { can } = useAuth();
  const shop = useLoad(
    () => get<Storefront>("/v1/storefront").catch(() => null),
    [],
  );
  const photoCol =
    shop.data?.catalogue_table === table.id
      ? shop.data.catalogue_columns.photo
      : undefined;
  const nameCol =
    shop.data?.catalogue_table === table.id
      ? shop.data.catalogue_columns.name
      : undefined;
  const [picks, setPicks] = useState<PhotoPick[]>([]);
  const [addedRow, setAddedRow] = useState<string>();
  const [failed, setFailed] = useState<{ pick: PhotoPick; why: string }[]>([]);
  const [sending, setSending] = useState<Sending>();
  const cols = table.columns.slice(0, 3);
  const title = table.title || table.name;
  if (editing) {
    const row = editing === "new" ? undefined : editing;
    const photosOK = !!photoCol && can("storefront.manage");
    const save: Saver = async () => {
      const set = Object.fromEntries(
        table.columns
          .filter((c) => c.name !== photoCol)
          .filter(
            (c) =>
              (values[c.name] ?? "") !== (row ? cellText(row[c.name]) : ""),
          )
          .map((c) => [c.name, cellValue(c.type, values[c.name] ?? "")]),
      );
      if (row) {
        if (Object.keys(set).length === 0) return null;
        await toolPatch(
          `/v1/tables/${encodeURIComponent(table.id)}/rows/${encodeURIComponent(String(row.id))}`,
          { set },
        );
        return `${title}: a row changed`;
      }
      let id = addedRow;
      if (!id) {
        const out = await toolPost<{ rows?: Row[] }>(
          `/v1/tables/${encodeURIComponent(table.id)}/rows`,
          { row: set },
        );
        id =
          out.rows?.[0]?.id !== undefined ? String(out.rows[0].id) : undefined;
      }
      const todo = addedRow ? failed.map((f) => f.pick) : photosOK ? picks : [];
      if (id && todo.length) {
        setSending({ done: 0, of: todo.length });
        const bad = await sendAll(
          id,
          todo,
          addedRow ? picks.length - failed.length : 0,
          () => setSending((x) => x && { ...x, done: x.done + 1 }),
        ).finally(() => setSending(undefined));
        await post("/v1/storefront/catalogue/refresh").catch(() => undefined);
        if (bad.length) {
          setAddedRow(id);
          setFailed(bad);
          throw new Error(
            `The row is added, but ${bad.length === 1 ? "a photo" : `${bad.length} photos`} did not go. Press Send the photos again, or close: the row stays.`,
          );
        }
      }
      return `${title}: a row added`;
    };
    const missing =
      !row &&
      table.columns.some(
        (c) => c.required && (values[c.name] ?? "").trim() === "",
      );
    return (
      <FormFrame
        save={save}
        onSaved={onSaved}
        saveWords={
          row ? "Save" : addedRow ? "Send the photos again" : "Add the row"
        }
        ready={!missing}
        canEdit={canEdit}
      >
        {table.columns.map((c) =>
          c.name === photoCol ? (
            <div key={c.name} className="tk-field">
              <span className="tk-field-label">{c.title || c.name}</span>
              {!photosOK ? (
                <p className="hint">
                  Photos are added by whoever looks after the shop&apos;s page.
                </p>
              ) : row ? (
                <ProductPhotos
                  row={String(row.id)}
                  name={(nameCol && values[nameCol]) || "this product"}
                  canEdit={canEdit}
                />
              ) : addedRow ? (
                <PhotoFailures
                  failed={failed}
                  onRetry={() => undefined}
                  hideRetry
                />
              ) : (
                <>
                  <SendingLine sending={sending} />
                  <PendingPhotos picks={picks} onChange={setPicks} />
                </>
              )}
            </div>
          ) : (
            <KitField
              key={c.name}
              label={`${c.title || c.name}${c.required ? "" : " (optional)"}`}
            >
              <input
                value={values[c.name] ?? ""}
                onChange={(e) =>
                  setValues({ ...values, [c.name]: e.target.value })
                }
              />
            </KitField>
          ),
        )}
      </FormFrame>
    );
  }
  const open = (r: Row | "new") => {
    setValues(
      r === "new"
        ? {}
        : Object.fromEntries(
            table.columns.map((c) => [c.name, cellText(r[c.name])]),
          ),
    );
    setPicks([]);
    setAddedRow(undefined);
    setFailed([]);
    setEditing(r);
  };
  return (
    <>
      <div className="tk-sheet-list tk-form-body">
        {!rows.data ? (
          rows.error ? (
            <ErrorBox error={rows.error} />
          ) : (
            <KitLoading />
          )
        ) : rows.data.rows.length === 0 ? (
          <KitEmpty icon="tables" title="No rows yet" />
        ) : (
          <ul className="tk-table-rows">
            {rows.data.rows.map((r, i) => (
              <li key={String(r.id ?? i)}>
                <button
                  type="button"
                  className="tk-table-row"
                  disabled={!canEdit}
                  onClick={() => open(r)}
                >
                  {cols.map((c) => (
                    <span key={c.name}>
                      <span className="tk-cell-name">{c.title || c.name}</span>{" "}
                      {c.name === photoCol
                        ? photoCount(r[c.name])
                        : cellText(r[c.name])}
                    </span>
                  ))}
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {canEdit && (
        <div className="tk-sheet-foot">
          <button type="button" className="tk-cta" onClick={() => open("new")}>
            Add a row
          </button>
        </div>
      )}
    </>
  );
}
