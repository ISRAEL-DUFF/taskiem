// The shop (docs/spec/mobile-app-look.md, "The shop"; the app's
// mobile/lib/chat/shop.dart): a business's items to browse from a person's
// chat with it, over the chat. The items as photos in a grid, searched and
// filtered by their categories, the ones saved with the heart and the ones
// looked at lately; an item's own view (its photo, sizes, Ask about this,
// Share); and the bag, checked out in the shop's sheet (PersonTools.tsx
// ShopSheet, pickup or delivery). The bag stays while the page is open; the
// hearts and what was looked at are kept in this browser (lib/shopkept.ts).

import { useEffect, useLayoutEffect, useMemo, useRef, useState, useSyncExternalStore } from "react";
import { get } from "../api";
import { Icon } from "../icons";
import { KitEmpty, KitLoading } from "../kit";
import { changeBasket, sizeWords, type BasketLine, type ShopItem, type TheirShop } from "../lib/personchat";
import { categories, fromPrice, keepTo, pricesDiffer, recentItems, savedItems, shownItems, soldOut, toggleSaved, viewedItem } from "../lib/shopkept";
import { formatPrice } from "../lib/storefront";
import { ErrorBox, useFocusTrap, useLoad } from "../ui";
import { ChatAvatar } from "./ChatAvatar";

// --- the bag: one a chat, while the page is open ---

const bags = new Map<string, BasketLine[]>();
const listeners = new Set<() => void>();
function setBag(chat: string, lines: BasketLine[]) {
  bags.set(chat, lines);
  for (const l of listeners) l();
}
/** What is in the bag of [chat]'s shop, and how to change it. */
export function useBag(chat: string): [BasketLine[], (lines: BasketLine[]) => void] {
  const lines = useSyncExternalStore(
    (on) => (listeners.add(on), () => void listeners.delete(on)),
    () => bags.get(chat) ?? EMPTY,
  );
  return [lines, (next) => setBag(chat, next)];
}
const EMPTY: BasketLine[] = [];
/** The order went: the bag empties. */
export function emptyBag(chat: string) {
  setBag(chat, []);
}

const sizePrice = (kobo: number | undefined) => (kobo === undefined ? "" : formatPrice(kobo));
const count = (lines: BasketLine[]) => lines.reduce((t, l) => t + l.quantity, 0);
const total = (lines: BasketLine[]) => lines.reduce((t, l) => t + l.price_kobo * l.quantity, 0);
const itemsSaid = (n: number) => `${n} ${n === 1 ? "item" : "items"}`;

/** A line of the bag for an item, or one of its sizes. */
function lineOf(it: ShopItem, size?: string): Omit<BasketLine, "quantity"> | undefined {
  const z = it.sizes?.find((x) => x.id === size);
  const price = z ? (z.price_kobo ?? it.price_kobo) : it.price_kobo;
  if (price === undefined) return undefined;
  return { item_id: z ? z.id : it.id, name: z ? `${it.name} (${sizeWords(z.variant)})` : it.name, price_kobo: price };
}

type View = { at: "grid" } | { at: "item"; id: string } | { at: "bag" };

export function ShopView({
  chat,
  business,
  businessId,
  logo,
  startItem,
  onClose,
  onAsk,
  onShare,
  onCheckout,
}: {
  chat: string;
  business: string;
  /** Whose hearts and lately-seen items these are. */
  businessId: string;
  logo?: string;
  startItem?: string;
  onClose: () => void;
  /** Ask about this: the question begun in the chat's box. */
  onAsk: (words: string) => void;
  onShare?: (it: { id: string; name: string }) => void;
  /** Check out: the shop's sheet, at its checkout, with the bag. */
  onCheckout: (lines: BasketLine[]) => void;
}) {
  const shop = useLoad(() => get<TheirShop>(`/v1/me/chats/${encodeURIComponent(chat)}/shop`), [chat]);
  const s = shop.data;
  const [view, setView] = useState<View>(() => (startItem ? { at: "item", id: startItem } : { at: "grid" }));
  const [bag, putBag] = useBag(chat);
  const [saved, setSaved] = useState(() => savedItems(businessId));
  const [recent, setRecent] = useState(() => recentItems(businessId));
  const [filter, setFilter] = useState("all");
  const [words, setWords] = useState("");

  // Over the chat as a dialog: Tab stays in the shop, focus starts on it
  // (not the search, which would bring up a phone's keyboard) and goes back
  // after; Esc goes back a view, then closes. A sheet over the shop (the
  // checkout, sharing) holds the keys while it is open.
  const box = useRef<HTMLElement>(null);
  useLayoutEffect(() => box.current?.focus({ preventScroll: true }), []);
  useFocusTrap(box, true, () => (view.at === "grid" ? onClose() : setView({ at: "grid" })), () => !document.documentElement.classList.contains("tk-sheet-open"));

  // The bag kept while the page was open, against the shop read now: what
  // is no longer for sale leaves it, and that is said.
  const [bagNote, setBagNote] = useState("");
  const kept = useRef(false);
  useEffect(() => {
    if (!s || kept.current) return;
    kept.current = true;
    const was = bags.get(chat) ?? [];
    const k = keepTo(was, s);
    if (JSON.stringify(k.lines) !== JSON.stringify(was)) setBag(chat, k.lines);
    if (k.changed) setBagNote("Your bag changed: something in it is no longer for sale, or fewer are left.");
  }, [s, chat]);

  const heart = (id: string) => setSaved(toggleSaved(businessId, id));
  const open = (id: string) => {
    setRecent(viewedItem(businessId, id));
    setView({ at: "item", id });
  };
  const change = (line: Omit<BasketLine, "quantity">, by: number) => putBag(changeBasket(bag, line, by));
  const qty = (id: string) => bag.find((l) => l.item_id === id)?.quantity ?? 0;
  const byId = useMemo(() => new Map((s?.items ?? []).map((i) => [i.id, i])), [s]);
  const item = view.at === "item" ? byId.get(view.id) : undefined;

  // A card in the chat named an item not (or no longer) in the shop: the grid.
  useEffect(() => {
    if (s && view.at === "item" && !byId.has(view.id)) setView({ at: "grid" });
    if (s && view.at === "item" && byId.has(view.id) && recent[0] !== view.id) setRecent(viewedItem(businessId, view.id));
  }, [s, view, byId, businessId, recent]);

  return (
    <section ref={box} tabIndex={-1} className="shop" role="dialog" aria-modal="true" aria-label={`${business}'s shop`} data-testid="shop">
      {bagNote && (
        <p className="notice shop-bag-note" role="status">
          <span className="grow">{bagNote}</span>
          <button type="button" className="small" onClick={() => setBagNote("")}>
            OK
          </button>
        </p>
      )}
      {item && s ? (
        <ShopItemView
          shop={s}
          item={item}
          business={business}
          saved={saved.includes(item.id)}
          onHeart={() => heart(item.id)}
          onBack={() => setView({ at: "grid" })}
          onOpen={open}
          qty={qty}
          onAdd={(line, n) => change(line, n)}
          onAsk={onAsk}
          onShare={onShare}
          onBag={() => setView({ at: "bag" })}
          bagCount={count(bag)}
        />
      ) : view.at === "bag" && s ? (
        <ShopBagView
          shop={s}
          business={business}
          bag={bag}
          byId={byId}
          onBack={() => setView({ at: "grid" })}
          onChange={change}
          onAsk={() => onClose()}
          onCheckout={() => onCheckout(bag)}
        />
      ) : (
        <>
          <header className="chat-head shop-head">
            <button type="button" className="icon-button chat-back" onClick={onClose} aria-label="Back to the chat">
              <Icon name="back" />
            </button>
            <ChatAvatar name={business} logo={logo} />
            <div className="chat-head-main">
              <h2>{business}</h2>
              <span className="chat-sub">{s ? waysWords(s) : "Their shop"}</span>
            </div>
            <button
              type="button"
              className={`icon-button shop-head-heart${filter === "saved" ? " on" : ""}`}
              aria-label={filter === "saved" ? "Show all of the shop" : "Show what you saved"}
              aria-pressed={filter === "saved"}
              onClick={() => setFilter(filter === "saved" ? "all" : "saved")}
            >
              <Icon name="heart" />
            </button>
            <BagButton n={count(bag)} onClick={() => setView({ at: "bag" })} />
          </header>
          <div className="shop-body">
            {!s && !shop.error && <KitLoading label="Loading their shop…" />}
            <ErrorBox error={shop.error} />
            {s && s.items.length === 0 && (
              <KitEmpty icon="store" title={`${business} has nothing in their shop yet`}>
                Ask them in the chat.
              </KitEmpty>
            )}
            {s && s.items.length > 0 && (
              <ShopGrid
                shop={s}
                business={business}
                saved={saved}
                recent={recent.map((id) => byId.get(id)).filter((x): x is ShopItem => !!x)}
                filter={filter}
                setFilter={setFilter}
                words={words}
                setWords={setWords}
                qty={qty}
                onOpen={open}
                onHeart={heart}
                onChange={change}
              />
            )}
          </div>
          {count(bag) > 0 && <BagBar lines={bag} onClick={() => setView({ at: "bag" })} />}
        </>
      )}
    </section>
  );
}

function waysWords(s: TheirShop): string {
  if (!s.open) return "Not taking orders now";
  const how = s.pickup && s.delivery ? "Delivery and pickup" : s.delivery ? "Delivery" : s.pickup ? "Pickup" : "";
  return how ? `Open · ${how}` : "Open";
}

function BagButton({ n, onClick }: { n: number; onClick: () => void }) {
  return (
    <button type="button" className="icon-button shop-bag-button" aria-label={n ? `Your bag, ${itemsSaid(n)}` : "Your bag, empty"} onClick={onClick} data-testid="shop-bag-button">
      <Icon name="bag" />
      {n > 0 && (
        <span className="shop-bag-count" aria-hidden="true">
          {n}
        </span>
      )}
    </button>
  );
}

function BagBar({ lines, onClick }: { lines: BasketLine[]; onClick: () => void }) {
  const said = `${itemsSaid(count(lines))} · ${formatPrice(total(lines))}`;
  return (
    <button type="button" className="shop-bag-bar" onClick={onClick} data-testid="shop-bag-bar">
      <Icon name="bag" />
      <span className="grow">{said}</span>
      <strong>View bag</strong>
      <Icon name="chevron" />
    </button>
  );
}

/** An item's photo, square; its first letter on a soft tint without one. */
function Photo({ it, className }: { it: ShopItem; className?: string }) {
  const [failed, setFailed] = useState(false);
  const tone = (it.name.split("").reduce((a, c) => a + c.charCodeAt(0), 0) % 5) + 1;
  return (
    <span className={`shop-photo tone-${tone}${className ? ` ${className}` : ""}`} aria-hidden="true">
      {/* The letter drawn by CSS: no text in the buttons it sits in. */}
      {it.photo && !failed ? <img src={it.photo} alt="" loading="lazy" onError={() => setFailed(true)} /> : <span className="shop-letter" data-letter={(it.name[0] ?? "?").toUpperCase()} />}
    </span>
  );
}

function ShopGrid({
  shop,
  business,
  saved,
  recent,
  filter,
  setFilter,
  words,
  setWords,
  qty,
  onOpen,
  onHeart,
  onChange,
}: {
  shop: TheirShop;
  business: string;
  saved: string[];
  recent: ShopItem[];
  filter: string;
  setFilter: (f: string) => void;
  words: string;
  setWords: (w: string) => void;
  qty: (id: string) => number;
  onOpen: (id: string) => void;
  onHeart: (id: string) => void;
  onChange: (line: Omit<BasketLine, "quantity">, by: number) => void;
}) {
  const cats = categories(shop.items);
  const shown = shownItems(shop.items, filter, words, saved);
  const chips: [string, string][] = [["all", "All"], ["saved", saved.length ? `Saved · ${saved.length}` : "Saved"], ...cats.map((c): [string, string] => [`cat:${c}`, c])];
  return (
    <>
      <label className="shop-search">
        <Icon name="search" />
        <span className="sr-only">Search their shop</span>
        <input type="search" placeholder={`Search ${business}`} value={words} onChange={(e) => setWords(e.target.value)} data-testid="shop-search" />
      </label>
      <div className="tk-chips shop-chips" role="group" aria-label="Show">
        {chips.map(([k, label]) => (
          <button key={k} type="button" aria-pressed={filter === k} onClick={() => setFilter(k)}>
            {label}
          </button>
        ))}
      </div>
      {shop.closed && !shop.open && (
        <p className="hint shop-closed" role="status">
          {shop.closed}
        </p>
      )}
      {recent.length > 0 && filter === "all" && !words.trim() && (
        <>
          <h3 className="shop-h">Recently viewed</h3>
          <ul className="shop-strip" aria-label="Recently viewed">
            {recent.map((it) => (
              <li key={it.id}>
                <button type="button" onClick={() => onOpen(it.id)} aria-label={it.name}>
                  <Photo it={it} />
                  <span className="shop-strip-name">{it.name}</span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      {shown.length === 0 ? (
        filter === "saved" && !words.trim() ? (
          <KitEmpty icon="star" title="Nothing saved yet">
            Press the heart on what you like, to find it here.
          </KitEmpty>
        ) : (
          <KitEmpty icon="search" title="Nothing matches">
            Try another word, or show all.
          </KitEmpty>
        )
      ) : (
        <ul className="shop-grid" aria-label={`${business}'s items`}>
          {shown.map((it) => {
            const sized = (it.sizes?.length ?? 0) > 0;
            const from = fromPrice(it);
            const price = from === undefined ? "" : sized && pricesDiffer(it) ? `From ${formatPrice(from)}` : formatPrice(from);
            const sold = soldOut(it);
            const few = sized ? undefined : it.few_left;
            const n = sized ? (it.sizes ?? []).reduce((t, z) => t + qty(z.id), 0) : qty(it.id);
            const line = sized ? undefined : lineOf(it);
            const isSaved = saved.includes(it.id);
            return (
              <li key={it.id} className={`shop-card${sold ? " sold" : ""}`} data-testid={`shop-item-${it.id}`}>
                <button type="button" className="shop-card-open" onClick={() => onOpen(it.id)}>
                  <Photo it={it} />
                  <span className="shop-card-name">{it.name}</span>
                  <span className="sr-only">{[price, sold ? "Sold out" : few ? `Only ${few} left` : ""].filter(Boolean).map((x) => `, ${x}`).join("")}</span>
                </button>
                {sold ? (
                  <span className="shop-mark danger" aria-hidden="true">
                    Sold out
                  </span>
                ) : few ? (
                  <span className="shop-mark" aria-hidden="true">
                    Only {few} left
                  </span>
                ) : null}
                <button type="button" className={`shop-heart${isSaved ? " on" : ""}`} aria-pressed={isSaved} aria-label={isSaved ? `Saved: ${it.name}` : `Save ${it.name}`} onClick={() => onHeart(it.id)}>
                  <Icon name="heart" />
                </button>
                <div className="shop-card-foot">
                  <span className="shop-price">{price}</span>
                  {!sold && shop.open && (sized ? (
                    <button type="button" className="shop-add" aria-label={`Choose a size of ${it.name}${n ? ` (${n} in the bag)` : ""}`} onClick={() => onOpen(it.id)}>
                      {n > 0 && <span>{n}</span>}
                      <Icon name="plus" />
                    </button>
                  ) : line && n > 0 ? (
                    <span className="shop-stepper on">
                      <button type="button" aria-label={`One fewer ${it.name}`} onClick={() => onChange(line, -1)}>
                        <Icon name="minus" />
                      </button>
                      <span>{n}</span>
                      <button type="button" aria-label={`One more ${it.name}`} disabled={!!few && n >= few} onClick={() => onChange(line, 1)}>
                        <Icon name="plus" />
                      </button>
                    </span>
                  ) : line ? (
                    <button type="button" className="shop-add" aria-label={`Add ${it.name} to the bag`} onClick={() => onChange(line, 1)}>
                      <Icon name="plus" />
                    </button>
                  ) : null)}
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </>
  );
}

function ShopItemView({
  shop,
  item: it,
  business,
  saved,
  onHeart,
  onBack,
  onOpen,
  qty,
  onAdd,
  onAsk,
  onShare,
  onBag,
  bagCount,
}: {
  shop: TheirShop;
  item: ShopItem;
  business: string;
  saved: boolean;
  onHeart: () => void;
  onBack: () => void;
  onOpen: (id: string) => void;
  qty: (id: string) => number;
  onAdd: (line: Omit<BasketLine, "quantity">, n: number) => void;
  onAsk: (words: string) => void;
  onShare?: (it: { id: string; name: string }) => void;
  onBag: () => void;
  bagCount: number;
}) {
  const sizes = it.sizes ?? [];
  const [size, setSize] = useState(() => sizes.find((z) => z.available)?.id ?? "");
  const [n, setN] = useState(1);
  const [added, setAdded] = useState("");
  useEffect(() => {
    setSize((it.sizes ?? []).find((z) => z.available)?.id ?? "");
    setN(1);
    setAdded("");
  }, [it]);
  const z = sizes.find((x) => x.id === size);
  const line = sizes.length ? (z ? lineOf(it, z.id) : undefined) : lineOf(it);
  const sold = sizes.length ? !z || !z.available || !it.available : !it.available;
  const left = sizes.length ? z?.few_left : it.few_left;
  const price = line?.price_kobo ?? it.price_kobo;
  const inBag = line ? qty(line.item_id) : 0;
  const more = [
    ...shop.items.filter((x) => x.id !== it.id && it.category && x.category === it.category),
    ...shop.items.filter((x) => x.id !== it.id && (!it.category || x.category !== it.category)),
  ].slice(0, 8);
  const add = () => {
    if (!line || sold) return;
    const by = left ? Math.max(0, Math.min(n, left - inBag)) : n;
    if (by > 0) onAdd(line, by);
    setAdded(`${n === 1 ? "" : `${n} × `}${line.name} is in your bag`);
    setN(1);
  };
  const ask = () => onAsk(`About ${[it.name, z ? sizeWords(z.variant) : "", price !== undefined ? formatPrice(price) : ""].filter(Boolean).join(", ")}: is it still available?`);
  return (
    <>
      <div className="shop-body shop-item">
        <div className="shop-hero">
          <Photo it={it} className="big" />
          <div className="shop-hero-buttons">
            <button type="button" className="shop-over" aria-label="Back to the shop" onClick={onBack}>
              <Icon name="back" />
            </button>
            <span className="grow" />
            {onShare && (
              <button type="button" className="shop-over" aria-label={`Share ${it.name} with someone`} onClick={() => onShare({ id: it.id, name: it.name })}>
                <Icon name="share" />
              </button>
            )}
            <button type="button" className={`shop-over shop-heart-big${saved ? " on" : ""}`} aria-pressed={saved} aria-label={saved ? `Saved: ${it.name}` : `Save ${it.name}`} onClick={onHeart}>
              <Icon name="heart" />
            </button>
            <BagButton n={bagCount} onClick={onBag} />
          </div>
        </div>
        <div className="shop-item-sheet">
          <p className="shop-eyebrow">{[it.category, business].filter(Boolean).join(" · ")}</p>
          <h2 className="shop-item-name">{it.name}</h2>
          <p className="shop-item-price">
            {price !== undefined && <strong>{formatPrice(price)}</strong>}
            {sold ? <span className="tk-pill shop-pill danger">Sold out</span> : left ? <span className="tk-pill shop-pill warn">Only {left} left</span> : null}
          </p>
          {it.description && <p className="shop-item-about">{it.description}</p>}
          {sizes.length > 0 && (
            <div className="tk-chips shop-sizes" role="group" aria-label="Size">
              {sizes.map((x) => (
                <button key={x.id} type="button" aria-pressed={size === x.id} disabled={!x.available} onClick={() => (setSize(x.id), setN(1))}>
                  {[sizeWords(x.variant), sizePrice(x.price_kobo ?? it.price_kobo)].filter(Boolean).join(" · ")}
                </button>
              ))}
            </div>
          )}
          <div className="shop-item-actions">
            <button type="button" className="shop-card-button" onClick={ask}>
              <Icon name="chat" />
              Ask about this<span className="sr-only">: {it.name}, in the chat with {business}</span>
            </button>
            {onShare && (
              <button type="button" className="shop-card-button" onClick={() => onShare({ id: it.id, name: it.name })}>
                <Icon name="share" />
                Share with a friend<span className="sr-only">: {it.name}</span>
              </button>
            )}
          </div>
          {more.length > 0 && (
            <>
              <h3 className="shop-h">More from {business}</h3>
              <ul className="shop-strip big" aria-label={`More from ${business}`}>
                {more.map((x) => (
                  <li key={x.id}>
                    <button type="button" onClick={() => onOpen(x.id)} aria-label={x.name}>
                      <Photo it={x} />
                      <span className="shop-strip-name">{x.name}</span>
                    </button>
                  </li>
                ))}
              </ul>
            </>
          )}
          {added && (
            <p className="shop-added" role="status">
              {added}.{" "}
              <button type="button" className="link-button" onClick={onBag}>
                View bag
              </button>
            </p>
          )}
        </div>
      </div>
      <footer className="shop-foot">
        {!shop.open ? (
          <p className="hint">{shop.closed || `${business} is not taking orders right now. Ask them in the chat.`}</p>
        ) : (
          <>
            <span className="shop-stepper">
              <button type="button" aria-label={`One fewer ${it.name}`} disabled={n <= 1} onClick={() => setN(n - 1)}>
                <Icon name="minus" />
              </button>
              <span>{n}</span>
              <button type="button" aria-label={`One more ${it.name}`} disabled={sold || (!!left && inBag + n >= left)} onClick={() => setN(n + 1)}>
                <Icon name="plus" />
              </button>
            </span>
            <button type="button" className="tk-cta shop-add-bag" disabled={sold || !line} onClick={add} data-testid="shop-add">
              {sold ? "Sold out" : !line ? (sizes.length ? "Choose a size" : "Ask them its price") : `Add to bag${price !== undefined ? ` · ${formatPrice(price * n)}` : ""}`}
            </button>
          </>
        )}
      </footer>
    </>
  );
}

function ShopBagView({
  shop,
  business,
  bag,
  byId,
  onBack,
  onChange,
  onAsk,
  onCheckout,
}: {
  shop: TheirShop;
  business: string;
  bag: BasketLine[];
  byId: Map<string, ShopItem>;
  onBack: () => void;
  onChange: (line: Omit<BasketLine, "quantity">, by: number) => void;
  onAsk: () => void;
  onCheckout: () => void;
}) {
  // A line's item: itself, or the item one of whose sizes it is.
  const itemOf = (id: string) => byId.get(id) ?? [...byId.values()].find((i) => i.sizes?.some((z) => z.id === id));
  const leftOf = (id: string) => {
    const it = itemOf(id);
    return it?.sizes?.find((z) => z.id === id)?.few_left ?? (it?.id === id ? it.few_left : undefined);
  };
  return (
    <>
      <header className="chat-head shop-head">
        <button type="button" className="icon-button chat-back" onClick={onBack} aria-label="Back to the shop">
          <Icon name="back" />
        </button>
        <div className="chat-head-main">
          <h2>Your bag</h2>
        </div>
        <span className="hint shop-bag-who">{business}</span>
      </header>
      <div className="shop-body">
        {bag.length === 0 ? (
          <KitEmpty icon="box" title="Your bag is empty">
            Press + on what you want in the shop.
          </KitEmpty>
        ) : (
          <>
            <ul className="shop-bag-lines" aria-label="Your bag">
              {bag.map((l) => {
                const it = itemOf(l.item_id);
                const left = leftOf(l.item_id);
                const size = it && it.id !== l.item_id ? sizeWords(it.sizes?.find((z) => z.id === l.item_id)?.variant ?? {}) : "";
                return (
                  <li key={l.item_id}>
                    {it ? <Photo it={it} /> : <span className="shop-photo" aria-hidden="true" />}
                    <span className="grow">
                      <strong>{it?.name ?? l.name}</strong>
                      <span className="hint">{[size, `${formatPrice(l.price_kobo)} each`].filter(Boolean).join(" · ")}</span>
                      <span className="shop-price">{formatPrice(l.price_kobo * l.quantity)}</span>
                    </span>
                    <span className="shop-stepper">
                      <button type="button" aria-label={`One fewer ${it?.name ?? l.name}`} onClick={() => onChange(l, -1)}>
                        <Icon name="minus" />
                      </button>
                      <span>{l.quantity}</span>
                      <button type="button" aria-label={`One more ${it?.name ?? l.name}`} disabled={!!left && l.quantity >= left} onClick={() => onChange(l, 1)}>
                        <Icon name="plus" />
                      </button>
                    </span>
                  </li>
                );
              })}
            </ul>
            <p className="shop-bag-total">
              <span className="grow">Items</span>
              <strong>{formatPrice(total(bag))}</strong>
            </p>
            <p className="hint">{shop.delivery ? "How you get it, and delivery if you want it, come next." : "How you get it comes next."}</p>
            <button type="button" className="shop-card-button" onClick={onAsk}>
              <Icon name="chat" />
              Have a question? Ask {business} in the chat
            </button>
          </>
        )}
      </div>
      {bag.length > 0 && shop.open && (
        <footer className="shop-foot">
          <button type="button" className="tk-cta" onClick={onCheckout} data-testid="shop-checkout">
            Check out · {formatPrice(total(bag))}
          </button>
        </footer>
      )}
    </>
  );
}
