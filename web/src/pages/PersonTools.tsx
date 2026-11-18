import { useRef, useState } from "react";
import { ApiError, get, post, postOnce } from "../api";
import { Icon } from "../icons";
import { KitEmpty, KitField, KitLoading, Pill, Sheet } from "../kit";
import { dayLabel, lagosTime, whenLabel } from "../lib/bookings";
import type { LinkService, LinkTimes } from "../lib/linkbook";
import {
  basketTotal,
  changeBasket,
  deliveryStep,
  fewLeftWords,
  itemsWords,
  minutesWords,
  payWords,
  plainItem,
  sizeWords,
  stateWords,
  type BasketLine,
  type DeliveryProgress,
  type DoorOrder,
  type ShopItem,
  type TheirShop,
} from "../lib/personchat";
import { formatPrice } from "../lib/storefront";
import { addressLine, addressProblem, chosenAddress, keepProblem, MAX_ADDRESSES, SAVED_LINE_MAX, type SavedAddress } from "../lib/youorders";
import { ErrorBox, useLoad } from "../ui";

// The tools of a person's chat with a business (docs/spec/taskiem-for-
// everyone.md §8.3): Their shop, Book, Your orders with them and Pay,
// behind the tools button, each a sheet. Everything goes through the
// customer door (GET/POST /v1/me/chats/{id}/…).

export type PersonTool = "shop" | "book" | "orders" | "pay";

const base = (chat: string) => `/v1/me/chats/${encodeURIComponent(chat)}`;

/** The tools button's sheet: a tile for each tool. */
export function ToolsMenu({ business, onPick, onClose }: { business: string; onPick: (t: PersonTool, from: DOMRect) => void; onClose: () => void }) {
  const tiles: { key: PersonTool; icon: string; title: string; line: string }[] = [
    { key: "shop", icon: "store", title: "Their shop", line: `Order from ${business}` },
    { key: "book", icon: "calendar", title: "Book", line: "Choose a service and a time" },
    { key: "orders", icon: "orders", title: "Your orders", line: `Your orders with ${business}` },
    { key: "pay", icon: "money", title: "Pay", line: "What you owe them" },
  ];
  return (
    <Sheet title="Tools" onClose={onClose} testid="person-tools">
      <ul className="pc-tiles" aria-label="Tools">
        {tiles.map((t) => (
          <li key={t.key}>
            <button type="button" className="tk-row pc-tile" onClick={(e) => onPick(t.key, e.currentTarget.getBoundingClientRect())} data-testid={`person-tool-${t.key}`}>
              <span className="tk-row-tile" aria-hidden="true">
                <Icon name={t.icon} />
              </span>
              <span className="tk-row-words">
                <span className="tk-row-name">{t.title}</span>
                <span className="tk-row-sub">{t.line}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
    </Sheet>
  );
}

/** Their shop: items in sizes and colours, few left where they say, a
 * basket, pickup or delivery, and the order placed once. */
export function ShopSheet({
  chat,
  business,
  from,
  startItem,
  onShare,
  onClose,
  onPlaced,
}: {
  chat: string;
  business: string;
  from?: DOMRect | null;
  /** A product card's item, put in the basket. */
  startItem?: string;
  /** Share a product with someone (people chats, §10.1). */
  onShare?: (it: { id: string; name: string }) => void;
  onClose: () => void;
  onPlaced: (o: DoorOrder) => void;
}) {
  const shop = useLoad(() => get<TheirShop>(`${base(chat)}/shop`), [chat]);
  const [lines, setLines] = useState<BasketLine[]>([]);
  const [sizeOf, setSizeOf] = useState<Record<string, string>>({});
  const [step, setStep] = useState<"items" | "check" | "done">("items");
  const [fulfilment, setFulfilment] = useState<"pickup" | "delivery" | "">("");
  const [zone, setZone] = useState("");
  const [address, setAddress] = useState("");
  const [landmark, setLandmark] = useState("");
  // The person's saved addresses (§13): one is picked, or one is typed
  // and, when they ask, saved. Without them (or when they cannot be read)
  // the address is typed as before.
  const saved = useLoad(() => get<{ addresses: SavedAddress[] }>("/v1/me/addresses").catch(() => ({ addresses: [] as SavedAddress[] })), []);
  const addresses = saved.data?.addresses ?? [];
  const [picked, setPicked] = useState<string>();
  // An address typed: it stays chosen over a default that loads after.
  const [typed, setTyped] = useState(false);
  const [keep, setKeep] = useState(false);
  const [keepAs, setKeepAs] = useState("Home");
  const keptID = useRef("");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [placed, setPlaced] = useState<DoorOrder>();
  const [shut, setShut] = useState(false);
  // One order per basket as checked: a retry after a dropped connection
  // is the same order (Idempotency-Key).
  const key = useRef("");
  const s = shop.data;
  const started = useRef(false);
  if (s && startItem && !started.current) {
    started.current = true;
    const it = s.items.find((x) => x.id === startItem);
    if (it && it.available && plainItem(it) && it.price_kobo !== undefined)
      setLines([{ item_id: it.id, name: it.name, price_kobo: it.price_kobo, quantity: 1 }]);
  }
  const qty = (id: string) => lines.find((l) => l.item_id === id)?.quantity ?? 0;
  const fee = fulfilment === "delivery" ? (s?.zones.find((z) => z.name === zone)?.fee_kobo ?? 0) : 0;
  const total = basketTotal(lines, fee);
  const ways = s ? [...(s.pickup ? (["pickup"] as const) : []), ...(s.delivery ? (["delivery"] as const) : [])] : [];
  const how = fulfilment || (ways.length === 1 ? ways[0] : "");
  // The saved address chosen: the one picked, else the default; once an
  // address is typed it stays chosen, even when the saved ones load late.
  const chosenID = chosenAddress(picked, addresses, typed);
  const chosen = addresses.find((a) => a.id === chosenID);
  const hasAddress = !!chosen || address.trim().length > 0;
  // "Save this address": offered under the most a person keeps, and the
  // typed address short enough to be kept.
  const canKeep = !!saved.data && addresses.length < MAX_ADDRESSES;
  const keeping = keep && canKeep;
  const keepSaid = keeping ? keepProblem(address, addresses.length) : null;
  const ready = lines.length > 0 && !keepSaid && (ways.length === 0 || (!!how && (how !== "delivery" || ((s?.zones.length ?? 0) === 0 || !!zone) && hasAddress)));

  const place = async () => {
    if (!ready || busy) return;
    setBusy(true);
    setError(undefined);
    key.current ||= `order-${crypto.randomUUID()}`;
    try {
      const delivering = how === "delivery";
      let addressID = delivering && chosen ? chosen.id : "";
      // "Save this address": saved once, then the order is placed with it.
      if (delivering && !chosen && keeping) {
        if (!keptID.current) {
          try {
            const made = await post<SavedAddress>("/v1/me/addresses", { label: keepAs.trim() || "Home", line: address.trim(), area: zone, landmark: landmark.trim() });
            keptID.current = made.id;
          } catch (e) {
            const said = addressProblem(e);
            throw said ? new ApiError(400, `Your address was not saved. ${said}`) : e;
          }
        }
        addressID = keptID.current;
      }
      const out = await postOnce<{ order: DoorOrder }>(`${base(chat)}/orders`, key.current, {
        lines: lines.map((l) => ({ item_id: l.item_id, quantity: l.quantity })),
        fulfilment: how,
        zone: delivering ? zone : "",
        address: delivering && !addressID ? address.trim() : "",
        ...(addressID ? { address_id: addressID } : {}),
        ...(delivering && !addressID && landmark.trim() ? { landmark: landmark.trim() } : {}),
        note: note.trim(),
      });
      setPlaced(out.order);
      setStep("done");
      onPlaced(out.order);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };

  const footer =
    step === "items" && lines.length > 0 ? (
      <button type="button" className="tk-cta" onClick={() => setStep("check")} data-testid="shop-check">
        Check order · {itemsWords(lines)} · {formatPrice(total)}
      </button>
    ) : step === "check" ? (
      <button type="button" className="tk-cta" disabled={!ready || busy} onClick={() => void place()} data-testid="shop-place">
        {busy ? "Sending…" : `Place order · ${formatPrice(total)}`}
      </button>
    ) : undefined;

  return (
    <Sheet
      title={step === "check" ? "Check your order" : step === "done" ? "Order sent" : `${business}'s shop`}
      from={from}
      onClose={onClose}
      onBack={step === "check" ? () => ((key.current = ""), setStep("items")) : undefined}
      footer={footer}
      shut={shut}
      testid="shop-sheet"
    >
      {!s && !shop.error && <KitLoading label="Loading their shop…" />}
      <ErrorBox error={shop.error} />
      {s && step === "items" && (
        <>
          {s.closed && (
            <p className="notice" role="status">
              {s.closed}
            </p>
          )}
          {s.items.length === 0 ? (
            <KitEmpty icon="store" title="Nothing in their shop yet">
              Ask them here in the chat.
            </KitEmpty>
          ) : (
            <ul className="pc-items" aria-label={`${business}'s items`}>
              {s.items.map((it) => (
                <ShopRow
                  key={it.id}
                  it={it}
                  open={s.open}
                  qty={qty}
                  size={sizeOf[it.id] ?? ""}
                  setSize={(v) => setSizeOf((m) => ({ ...m, [it.id]: v }))}
                  onShare={onShare}
                  change={(line, by) => {
                    key.current = "";
                    setLines((cur) => changeBasket(cur, line, by));
                  }}
                />
              ))}
            </ul>
          )}
        </>
      )}
      {s && step === "check" && (
        <div className="pc-check">
          <ul className="pc-lines" aria-label="Your order">
            {lines.map((l) => (
              <li key={l.item_id}>
                <span className="grow">
                  {l.quantity} × {l.name}
                </span>
                <span>{formatPrice(l.price_kobo * l.quantity)}</span>
              </li>
            ))}
            {fee > 0 && (
              <li>
                <span className="grow">Delivery</span>
                <span>{formatPrice(fee)}</span>
              </li>
            )}
            <li className="pc-total">
              <span className="grow">Total</span>
              <span>{formatPrice(total)}</span>
            </li>
          </ul>
          {ways.length > 1 && (
            <fieldset className="pc-choice">
              <legend>How do you want it?</legend>
              <label className="switch-line">
                <input type="radio" name="fulfilment" checked={how === "pickup"} onChange={() => (setFulfilment("pickup"), (key.current = ""))} /> <span>I'll pick it up</span>
              </label>
              <label className="switch-line">
                <input type="radio" name="fulfilment" checked={how === "delivery"} onChange={() => (setFulfilment("delivery"), (key.current = ""))} /> <span>Deliver it to me</span>
              </label>
            </fieldset>
          )}
          {ways.length === 1 && <p className="hint">{ways[0] === "pickup" ? "You pick it up from them." : "They deliver it to you."}</p>}
          {how === "delivery" && s.zones.length > 0 && (
            <KitField label="Where to">
              {/* The zone is the saved address's area: another zone, another address to save. */}
              <select value={zone} onChange={(e) => (setZone(e.target.value), (key.current = ""), (keptID.current = ""))} required>
                <option value="">Choose your area</option>
                {s.zones.map((z) => (
                  <option key={z.name} value={z.name}>
                    {z.name} · {z.fee_kobo > 0 ? formatPrice(z.fee_kobo) : "free"}
                  </option>
                ))}
              </select>
            </KitField>
          )}
          {how === "delivery" && addresses.length > 0 && (
            <fieldset className="pc-choice" data-testid="shop-saved-addresses">
              <legend>Deliver to</legend>
              {addresses.map((a) => (
                <label key={a.id} className="switch-line">
                  <input type="radio" name="saved-address" checked={chosenID === a.id} onChange={() => (setPicked(a.id), (key.current = ""))} />{" "}
                  <span>
                    <strong>{a.label}</strong> · {addressLine(a)}
                  </span>
                </label>
              ))}
              <label className="switch-line">
                <input type="radio" name="saved-address" checked={!chosen} onChange={() => (setPicked(""), (key.current = ""))} /> <span>Another address</span>
              </label>
            </fieldset>
          )}
          {how === "delivery" && !chosen && (
            <>
              <KitField label="Your address">
                <textarea
                  rows={2}
                  maxLength={keeping ? SAVED_LINE_MAX : 300}
                  value={address}
                  onChange={(e) => (setAddress(e.target.value), setTyped(true), (key.current = ""), (keptID.current = ""))}
                  required
                />
              </KitField>
              <KitField label="Landmark" hint="Optional. Something a rider can find.">
                <input type="text" maxLength={200} value={landmark} onChange={(e) => (setLandmark(e.target.value), (key.current = ""), (keptID.current = ""))} />
              </KitField>
              {canKeep && (
                <label className="switch-line">
                  <input type="checkbox" checked={keep} onChange={(e) => (setKeep(e.target.checked), (key.current = ""))} data-testid="shop-keep-address" /> <span>Save this address</span>
                </label>
              )}
              {keeping && (
                <KitField label="Name it" hint="Such as Home or Work.">
                  {/* A new name after it was saved keeps the one saved: a retry does not save it twice. */}
                  <input type="text" maxLength={40} value={keepAs} onChange={(e) => setKeepAs(e.target.value)} />
                </KitField>
              )}
              {keepSaid && (
                <p className="error" role="alert" data-testid="shop-keep-problem">
                  {keepSaid}
                </p>
              )}
            </>
          )}
          <KitField label="A note for them" hint="Optional">
            <textarea rows={2} maxLength={500} value={note} onChange={(e) => (setNote(e.target.value), (key.current = ""))} />
          </KitField>
          <p className="hint">{payWords(s.payment, business)}</p>
          <p className="hint">{business} sees your name and phone number with your order.</p>
          <ErrorBox error={error} />
        </div>
      )}
      {step === "done" && placed && (
        <div className="pc-done" data-testid="shop-done">
          <Done words="Order sent" />
          <p>
            Your order {placed.reference} is with {business}. It is in this chat, and they will answer here.
          </p>
          {s?.payment === "at_order" && <PayButton chat={chat} order={{ ...placed, due_kobo: placed.total_kobo }} />}
          <button type="button" className="tk-btn soft" onClick={() => setShut(true)}>
            Back to the chat
          </button>
        </div>
      )}
    </Sheet>
  );
}

export function ShopRow({
  it,
  open,
  qty,
  size,
  setSize,
  change,
  onShare,
}: {
  it: ShopItem;
  onShare?: (it: { id: string; name: string }) => void;
  open: boolean;
  qty: (id: string) => number;
  size: string;
  setSize: (v: string) => void;
  change: (line: Omit<BasketLine, "quantity">, by: number) => void;
}) {
  const few = fewLeftWords(it.few_left);
  const sizes = it.sizes ?? [];
  const chosen = sizes.find((x) => x.id === size);
  const price = chosen?.price_kobo ?? it.price_kobo;
  const lineID = plainItem(it) ? it.id : chosen?.id;
  const n = lineID ? qty(lineID) : 0;
  const can = open && it.available && price !== undefined && (plainItem(it) || (!!chosen && chosen.available));
  const name = chosen ? `${it.name} (${sizeWords(chosen.variant)})` : it.name;
  return (
    <li className="pc-item" data-testid="shop-item">
      {it.photo ? <img className="pc-item-photo" src={it.photo} alt="" loading="lazy" /> : <span className="tk-row-tile" aria-hidden="true">{it.name.slice(0, 1).toUpperCase()}</span>}
      <span className="tk-row-words">
        <span className="tk-row-name">{it.name}</span>
        <span className="tk-row-sub">
          {price !== undefined ? formatPrice(price) : "Price on asking"}
          {!it.available && " · Sold out"}
        </span>
        {few && <span className="tk-row-warn">{few}</span>}
        {chosen && fewLeftWords(chosen.few_left) && <span className="tk-row-warn">{fewLeftWords(chosen.few_left)}</span>}
        {onShare && (
          <button type="button" className="link pc-item-share" onClick={() => onShare({ id: it.id, name: it.name })} data-testid="shop-item-share">
            Share with someone<span className="sr-only"> {it.name}</span>
          </button>
        )}
        {sizes.length > 0 && (
          <select className="pc-size" aria-label={`Size and colour of ${it.name}`} value={size} onChange={(e) => setSize(e.target.value)}>
            <option value="">Choose size and colour</option>
            {sizes.map((z) => (
              <option key={z.id} value={z.id} disabled={!z.available}>
                {sizeWords(z.variant)}
                {z.price_kobo !== undefined && z.price_kobo !== it.price_kobo ? ` · ${formatPrice(z.price_kobo)}` : ""}
                {z.available ? "" : " · sold out"}
              </option>
            ))}
          </select>
        )}
      </span>
      {can && lineID && price !== undefined && (
        <span className="tk-stepper" role="group" aria-label={`How many ${name}`}>
          <button type="button" aria-label={`One fewer ${name}`} disabled={n === 0} onClick={() => change({ item_id: lineID, name, price_kobo: price }, -1)}>
            −
          </button>
          <span className="tk-stepper-n" aria-live="polite">
            {n}
          </span>
          <button type="button" aria-label={`One more ${name}`} onClick={() => change({ item_id: lineID, name, price_kobo: price }, 1)}>
            +
          </button>
        </span>
      )}
    </li>
  );
}

/** Book: their services, a day and a free time, then the booking. */
export function BookSheet({
  chat,
  business,
  from,
  startService,
  onClose,
  onBooked,
}: {
  chat: string;
  business: string;
  from?: DOMRect | null;
  startService?: string;
  onClose: () => void;
  onBooked: () => void;
}) {
  const list = useLoad(() => get<{ services: LinkService[]; open: boolean; closed?: string }>(`${base(chat)}/services`), [chat]);
  const [service, setService] = useState<LinkService>();
  const [day, setDay] = useState<string>();
  const [start, setStart] = useState<string>();
  const [note, setNote] = useState("");
  const [step, setStep] = useState<"service" | "time" | "confirm" | "done">("service");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  const [taken, setTaken] = useState(false);
  const [shut, setShut] = useState(false);
  const keys = useRef<Record<string, string>>({});
  const started = useRef(false);
  if (list.data && startService && !started.current) {
    started.current = true;
    const s = list.data.services.find((x) => x.id === startService);
    if (s && list.data.open) {
      setService(s);
      setStep("time");
    }
  }
  const times = useLoad(
    () =>
      service ? get<LinkTimes>(`${base(chat)}/services/${encodeURIComponent(service.id)}/times${day ? `?day=${day}` : ""}`) : Promise.resolve(undefined),
    [chat, service?.id, day],
  );
  const book = async () => {
    if (!service || !start) return;
    setBusy(true);
    setError(undefined);
    setTaken(false);
    const what = `${service.id} ${start}`;
    keys.current[what] ??= `book-${crypto.randomUUID()}`;
    try {
      await postOnce(`${base(chat)}/bookings`, keys.current[what], { service_id: service.id, start, note: note.trim() });
      setStep("done");
      onBooked();
    } catch (e) {
      if (e instanceof ApiError && e.body.code === "slot_taken") {
        setTaken(true);
        setStart(undefined);
        setStep("time");
        times.reload();
      } else setError(e);
    } finally {
      setBusy(false);
    }
  };
  const shownDay = times.data?.day ?? day;
  return (
    <Sheet
      title={step === "confirm" ? "Check your booking" : step === "done" ? "Booked" : step === "time" && service ? service.name : `Book with ${business}`}
      from={from}
      onClose={onClose}
      onBack={step === "confirm" ? () => setStep("time") : step === "time" && !startService ? () => setStep("service") : undefined}
      shut={shut}
      testid="book-sheet"
      footer={
        step === "time" && start ? (
          <button type="button" className="tk-cta" onClick={() => setStep("confirm")}>
            Continue
          </button>
        ) : step === "confirm" ? (
          <button type="button" className="tk-cta" disabled={busy} onClick={() => void book()} data-testid="book-confirm">
            {busy ? "Booking…" : "Book"}
          </button>
        ) : undefined
      }
    >
      {!list.data && !list.error && <KitLoading label="Loading their services…" />}
      <ErrorBox error={list.error} />
      {list.data && step === "service" && (
        <>
          {!list.data.open && (
            <p className="notice" role="status">
              {list.data.closed ?? `${business} does not take bookings in the app right now.`}
            </p>
          )}
          {list.data.services.length === 0 ? (
            <KitEmpty icon="calendar" title="No services to book">
              Ask them here in the chat.
            </KitEmpty>
          ) : (
            <ul className="pc-tiles" aria-label="Services">
              {list.data.services.map((s) => (
                <li key={s.id}>
                  <button type="button" className="tk-row pc-tile" disabled={!list.data?.open} onClick={() => (setService(s), setDay(undefined), setStart(undefined), setStep("time"))}>
                    <span className="tk-row-tile" aria-hidden="true">
                      <Icon name="calendar" />
                    </span>
                    <span className="tk-row-words">
                      <span className="tk-row-name">{s.name}</span>
                      <span className="tk-row-sub">{[minutesWords(s.minutes), s.price_kobo !== undefined ? formatPrice(s.price_kobo) : ""].filter(Boolean).join(" · ")}</span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {step === "time" && service && (
        <>
          {taken && (
            <p className="warning" role="alert">
              That time was just taken. Please choose another.
            </p>
          )}
          {!times.data && !times.error && <KitLoading rows={2} label="Loading free times…" />}
          <ErrorBox error={times.error} />
          {times.data && times.data.days.length === 0 && (
            <KitEmpty icon="calendar" title="No free times soon">
              Ask them here in the chat for another time.
            </KitEmpty>
          )}
          {times.data && times.data.days.length > 0 && (
            <>
              <div className="tk-chips pc-days" role="group" aria-label="Days">
                {times.data.days.map((d) => (
                  <button key={d} type="button" aria-pressed={d === shownDay} onClick={() => (setDay(d), setStart(undefined))}>
                    {dayLabel(d)}
                  </button>
                ))}
              </div>
              <div className="tk-chips pc-times" role="group" aria-label="Free times">
                {times.data.times.map((t) => (
                  <button key={t} type="button" aria-pressed={t === start} onClick={() => setStart(t)}>
                    {lagosTime(t)}
                  </button>
                ))}
              </div>
              <p className="hint">Times are Lagos time.</p>
            </>
          )}
        </>
      )}
      {step === "confirm" && service && start && (
        <div className="pc-check">
          <p>
            <strong>{service.name}</strong>
            <br />
            {whenLabel(start)} (Lagos time)
            {service.price_kobo !== undefined && (
              <>
                <br />
                {formatPrice(service.price_kobo)}
              </>
            )}
          </p>
          <KitField label="A note for them" hint="Optional">
            <textarea rows={2} maxLength={500} value={note} onChange={(e) => setNote(e.target.value)} />
          </KitField>
          <p className="hint">{business} sees your name and phone number with your booking.</p>
          <ErrorBox error={error} />
        </div>
      )}
      {step === "done" && (
        <div className="pc-done" data-testid="book-done">
          <Done words="Booked" />
          <p>Your booking is in this chat. {business} will answer here.</p>
          <button type="button" className="tk-btn soft" onClick={() => setShut(true)}>
            Back to the chat
          </button>
        </div>
      )}
    </Sheet>
  );
}

/** Pay one order: its pay link asked for, then opened by the person. */
export function PayButton({ chat, order }: { chat: string; order: Pick<DoorOrder, "reference" | "due_kobo"> }) {
  const [url, setUrl] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  if (url)
    return (
      <a className="tk-btn primary" href={url} target="_blank" rel="noopener noreferrer" data-testid="pay-open">
        Open the pay page<span className="sr-only"> for {order.reference}</span>
      </a>
    );
  return (
    <>
      <button
        type="button"
        className="tk-btn primary"
        disabled={busy}
        data-testid="pay-button"
        onClick={() => {
          setBusy(true);
          setError(undefined);
          get<{ url: string }>(`${base(chat)}/orders/${encodeURIComponent(order.reference)}/pay`)
            .then((r) => setUrl(r.url), setError)
            .finally(() => setBusy(false));
        }}
      >
        {busy ? "One moment…" : `Pay ${order.due_kobo > 0 ? formatPrice(order.due_kobo) : ""}`.trim()}
        <span className="sr-only"> for {order.reference}</span>
      </button>
      <ErrorBox error={error} />
    </>
  );
}

/** Your orders with them, and those with something to pay (Pay). */
export function OrdersSheet({ chat, business, from, only, onClose }: { chat: string; business: string; from?: DOMRect | null; only?: "pay"; onClose: () => void }) {
  const list = useLoad(() => get<{ orders: DoorOrder[] }>(`${base(chat)}/orders`), [chat]);
  const orders = (list.data?.orders ?? []).filter((o) => !only || o.pay);
  return (
    <Sheet title={only ? `Pay ${business}` : `Your orders with ${business}`} from={from} onClose={onClose} testid="orders-sheet">
      {!list.data && !list.error && <KitLoading label="Loading your orders…" />}
      <ErrorBox error={list.error} />
      {list.data && orders.length === 0 && (
        <KitEmpty icon={only ? "money" : "orders"} title={only ? "Nothing to pay" : "No orders yet"}>
          {only ? `You owe ${business} nothing in the app.` : `Orders and bookings you make with ${business} here show in this list.`}
        </KitEmpty>
      )}
      {orders.length > 0 && (
        <ul className="pc-orders" aria-label="Your orders">
          {orders.map((o) => {
            const st = stateWords(o.status);
            return (
              <li key={o.reference} className="pc-order" data-testid="person-order">
                <span className="pc-order-top">
                  <span className="tk-row-words">
                    <span className="tk-row-name">{o.kind === "booking" ? (o.service_name ?? "Booking") : o.summary || "Order"}</span>
                    <span className="tk-row-sub">
                      {o.reference} · {formatPrice(o.total_kobo)}
                      {o.starts_at ? ` · ${whenLabel(o.starts_at)}` : ""}
                    </span>
                  </span>
                  {st.label && <Pill tone={st.tone}>{st.label}</Pill>}
                </span>
                {o.delivery && <DeliveryLine d={o.delivery} />}
                {o.pay && <PayButton chat={chat} order={o} />}
              </li>
            );
          })}
        </ul>
      )}
    </Sheet>
  );
}

/** Where the rider is with an order: the line and four steps. */
function DeliveryLine({ d }: { d: DeliveryProgress }) {
  const step = deliveryStep(d);
  return (
    <span className="pc-order-delivery" data-testid="order-delivery">
      <span className="tk-row-sub">{d.line}</span>
      <span className="app-card-steps" role="img" aria-label={`${d.line}: step ${step} of 4`}>
        {[1, 2, 3, 4].map((n) => (
          <span key={n} className={n <= step ? "done" : undefined} />
        ))}
      </span>
    </span>
  );
}

/** A sheet's work done, said with a tick (the sheet stays, with what next). */
function Done({ words }: { words: string }) {
  return (
    <p className="pc-done-title" role="status">
      <span className="pc-done-tick" aria-hidden="true">
        <Icon name="tick" />
      </span>
      {words}
    </p>
  );
}
