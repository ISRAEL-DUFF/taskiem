import { useEffect, useRef, useState } from "react";
import { ApiError, del, get, post, put } from "../api";
import { Icon } from "../icons";
import { KitField, KitLoading, Pill, Sheet } from "../kit";
import { basketCan, basketCardLine, basketStateWords, basketWithLive, holderWords, myLines, payChoiceWords, shareWords, type Basket } from "../lib/baskets";
import { naira } from "../lib/invoices";
import { changeBasket, type BasketLine, type TheirShop } from "../lib/personchat";
import { toast } from "../toast";
import { ErrorBox, Items, Row, useAction, useLoad } from "../ui";
import { livePoll, usePeopleLive } from "./PeopleLive";
import { ShopRow } from "./PersonTools";

// Ordering together (docs/spec/people-chatting.md §10.3, §11 A and C;
// people-chatting-5b.md C with C1, C2, C3): the basket card in a people
// chat or a group, and the basket's sheet: who wants what, your own items
// from the business's shop (through the basket door), the holder's
// controls (pick-up or delivery and the area, close, reopen, hand on,
// cancel, place with the payment chosen), and once placed the order as it
// moves, each one's share, paying it and the share cards. Taskiem moves
// no money between people.

const base = (id: string) => `/v1/me/baskets/${encodeURIComponent(id)}`;

/** The basket as it is now: read, then kept up by the live connection
 * (polling slowly as the fallback). */
function useBasket(id: string) {
  const live = usePeopleLive();
  const load = useLoad(() => get<Basket>(base(id)), [id]);
  const [b, setB] = useState<Basket>();
  useEffect(() => setB(load.data), [load.data]);
  useEffect(() => live.subscribe((e) => (e.type === "basket" ? setB((cur) => basketWithLive(cur ?? load.data, e.data)) : undefined)), [live, load.data]);
  const reload = useRef(load.reload);
  reload.current = load.reload;
  useEffect(() => {
    const t = window.setInterval(() => reload.current(), livePoll(live.connected, 8_000));
    return () => window.clearInterval(t);
  }, [live.connected]);
  return { b, error: load.error, set: setB, reload: load.reload };
}

/** The basket card in a chat: what it is, who is in it and the total now,
 * and Open. */
export function BasketCardBody({ id, title }: { id: string; title: string }) {
  const { b } = useBasket(id);
  const [open, setOpen] = useState(false);
  const st = b ? basketStateWords(b) : undefined;
  return (
    <>
      <span className="tk-ref-top">
        <span className="tk-ref-tile order" aria-hidden="true">
          <Icon name="orders" />
        </span>
        <span className="tk-ref-words">
          <span className="tk-kicker">Order together</span>
          <span className="tk-ref-name">{title}</span>
          {b && <span className="tk-ref-sub" data-testid="basket-card-line">{basketCardLine(b, naira)}</span>}
        </span>
        {st && (
          <span className="tk-ref-pill" data-testid="basket-card-state">
            <Pill tone={st.tone}>{st.label}</Pill>
          </span>
        )}
      </span>
      <span className="tk-ref-foot">
        <button type="button" className="tk-btn primary" onClick={() => setOpen(true)} data-testid="basket-open">
          Open basket<span className="sr-only"> {title}</span>
        </button>
      </span>
      {open && <BasketSheet id={id} onClose={() => setOpen(false)} />}
    </>
  );
}

/** The basket's sheet. */
export function BasketSheet({ id, onClose }: { id: string; onClose: () => void }) {
  const { b, error, set } = useBasket(id);
  const [step, setStep] = useState<"basket" | "shop" | "place" | "account">("basket");
  const act = useAction();
  const run = (fn: () => Promise<Basket | { basket: Basket }>, ok: string | false) =>
    act.run(async () => {
      const out = await fn();
      set("basket" in out ? out.basket : out);
    }, ok);
  const title = b ? `Order together from ${b.business}` : "Order together";
  return (
    <Sheet title={title} onClose={onClose} onBack={step === "basket" ? undefined : () => setStep("basket")} testid="basket-sheet">
      <ErrorBox error={error ?? act.error} />
      {!b && !error && <KitLoading label="Loading the basket…" />}
      {b && step === "basket" && <BasketView b={b} busy={act.busy} run={run} onShop={() => setStep("shop")} onPlace={() => setStep("place")} onAccount={() => setStep("account")} />}
      {b && step === "shop" && <MineStep b={b} onDone={(nb) => (set(nb), setStep("basket"))} />}
      {b && step === "place" && <PlaceStep b={b} onDone={(nb) => (set(nb), setStep("basket"))} />}
      {b && step === "account" && <AccountStep b={b} onDone={(nb) => (set(nb), setStep("basket"))} />}
    </Sheet>
  );
}

function BasketView({
  b,
  busy,
  run,
  onShop,
  onPlace,
  onAccount,
}: {
  b: Basket;
  busy: boolean;
  run: (fn: () => Promise<Basket | { basket: Basket }>, ok: string | false) => Promise<boolean>;
  onShop: () => void;
  onPlace: () => void;
  onAccount: () => void;
}) {
  const can = basketCan(b);
  const st = basketStateWords(b);
  const others = b.people.filter((p) => !p.you);
  const [handTo, setHandTo] = useState("");
  const pay = (forID?: string) =>
    void run(async () => {
      const out = await get<{ url: string }>(`${base(b.id)}/pay${forID ? `?for=${encodeURIComponent(forID)}` : ""}`);
      window.open(out.url, "_blank", "noopener");
      return get<Basket>(base(b.id));
    }, false);
  return (
    <div className="basket" data-testid="basket" data-state={b.state}>
      <section className="hero" aria-label="The basket's total" data-testid="basket-hero">
        <div className="hero-top">
          <span className="hero-label">{b.where === "group" ? "In this group" : "Between the two of you"}</span>
          <span className="hero-tag">{st.label}</span>
        </div>
        <div className="hero-amount" data-testid="basket-total">
          {naira(b.order?.total_kobo ?? b.total_kobo)}
        </div>
        {holderWords(b) && (
          <p className="hero-sub" data-testid="basket-holder">
            {holderWords(b)}
          </p>
        )}
      </section>
      {b.problems.length > 0 && b.state !== "placed" && (
        <div className="notice" role="status" data-testid="basket-problems">
          <ul>
            {b.problems.map((p) => (
              <li key={p}>{p}</li>
            ))}
          </ul>
        </div>
      )}
      {b.order && (
        <Items label="The order">
          <Row icon="orders" title={`Order ${b.order.reference}`} sub={<span className="sub" data-testid="basket-order-line">{b.order.line}</span>} side={naira(b.order.total_kobo)} />
          <Row
            icon="money"
            title={b.order.payment_state === "paid" ? "Paid" : b.order.payment_state === "part_paid" ? "Part paid" : "Not paid yet"}
            sub={<span className="sub" data-testid="basket-paid">{`${naira(b.order.paid_kobo)} paid · ${naira(b.order.left_kobo)} left`}</span>}
          />
          {b.order.delivery && <Row icon="package" title="Delivery" sub={<span className="sub">{b.order.delivery.line}</span>} />}
        </Items>
      )}
      {b.people.length === 0 ? (
        <p className="hint">Nothing in the basket yet. Add what you want from {b.business}.</p>
      ) : (
        <Items label="Who wants what">
          {b.people.map((p) => (
            <Row
              key={p.person_id}
              letter={p.name.slice(0, 1)}
              title={p.name}
              side={naira(p.share?.amount_kobo ?? p.items_kobo)}
              testid="basket-person"
              sub={
                <span className="sub">
                  {p.lines.map((l) => (
                    <span key={l.id} className={`basket-line${l.available ? "" : " gone"}`} data-testid="basket-line">
                      {l.quantity} × {l.name} · {naira(l.line_kobo)}
                      {l.price_changed && ` (was ${naira(l.added_price_kobo * l.quantity)})`}
                      {!l.available && " · not available now"}
                      {can.holder && (
                        <button type="button" className="link" disabled={busy} onClick={() => void run(() => del(`${base(b.id)}/lines/${encodeURIComponent(l.id)}`), "Taken out")}>
                          Take out<span className="sr-only"> {l.name}</span>
                        </button>
                      )}
                    </span>
                  ))}
                  {p.share && (
                    <span className="basket-share" data-testid="basket-share">
                      Share: {shareWords(p.share, naira)}
                      {p.share.paid && (
                        <>
                          {" "}
                          <Pill tone="lime">Paid</Pill>
                        </>
                      )}
                      {p.share.card_sent && " · share card sent"}
                    </span>
                  )}
                </span>
              }
            />
          ))}
        </Items>
      )}
      {b.state !== "placed" && b.delivery_kobo > 0 && (
        <p className="hint">
          Delivery to {b.zone}: {naira(b.delivery_kobo)}, shared equally.
        </p>
      )}
      <div className="row-actions basket-actions">
        {can.addMine && (
          <button type="button" className="primary" onClick={onShop} data-testid="basket-add">
            {b.people.some((p) => p.you) ? "Change my items" : "Add my items"}
          </button>
        )}
        {can.close && (
          <button type="button" disabled={busy} onClick={() => void run(() => post(`${base(b.id)}/close`), "Basket closed")} data-testid="basket-close">
            Close the basket
          </button>
        )}
        {can.reopen && (
          <button type="button" disabled={busy} onClick={() => void run(() => post(`${base(b.id)}/reopen`), "Basket open again")} data-testid="basket-reopen">
            Reopen
          </button>
        )}
        {can.place && (
          <button type="button" className="primary" onClick={onPlace} data-testid="basket-place">
            Place the order
          </button>
        )}
        {can.takeOver && (
          <button type="button" disabled={busy} onClick={() => void run(() => put(`${base(b.id)}/holder`, { person_id: b.people.find((p) => p.you)?.person_id ?? "" }), "You hold the basket")} data-testid="basket-take">
            Take it over
          </button>
        )}
        {can.payMine && (
          <button type="button" className="primary" disabled={busy} onClick={() => pay()} data-testid="basket-pay-mine">
            Pay my share
          </button>
        )}
        {can.payOrder && (
          <button type="button" className="primary" disabled={busy} onClick={() => pay()} data-testid="basket-pay-order">
            Pay for the order
          </button>
        )}
        {can.payFor.map((p) => (
          <button key={p.person_id} type="button" disabled={busy} onClick={() => pay(p.person_id)} data-testid="basket-pay-for">
            Pay {p.name}'s share
          </button>
        ))}
        {can.shareCards && (
          <button
            type="button"
            className="primary"
            disabled={busy}
            data-testid="basket-share-cards"
            onClick={() =>
              void (async () => {
                try {
                  const out = await post<{ sent: string[]; basket: Basket }>(`${base(b.id)}/share-cards`, {});
                  toast(out.sent.length === 1 ? "Share card sent" : `${out.sent.length} share cards sent`);
                  void run(async () => out.basket, false);
                } catch (e) {
                  if (e instanceof ApiError && e.status === 400) onAccount();
                  else void run(() => Promise.reject(e), false);
                }
              })()
            }
          >
            Send each their share
          </button>
        )}
      </div>
      {can.holder && (
        <div className="pc-check basket-holder">
          {b.shop && (b.shop.pickup || b.shop.delivery) && (
            <KitField label="Pick-up or delivery">
              <select
                value={b.fulfilment ?? ""}
                disabled={busy}
                data-testid="basket-fulfilment"
                onChange={(e) => void run(() => put(base(b.id), { fulfilment: e.target.value }), false)}
              >
                <option value="">Choose</option>
                {b.shop.pickup && <option value="pickup">Pick-up</option>}
                {b.shop.delivery && <option value="delivery">Delivery</option>}
              </select>
            </KitField>
          )}
          {b.fulfilment === "delivery" && (b.shop?.zones.length ?? 0) > 0 && (
            <KitField label="Area to deliver to">
              <select value={b.zone ?? ""} disabled={busy} data-testid="basket-zone" onChange={(e) => void run(() => put(base(b.id), { zone: e.target.value }), false)}>
                <option value="">Choose an area</option>
                {b.shop?.zones.map((z) => (
                  <option key={z.name} value={z.name}>
                    {z.name} · {naira(z.fee_kobo)}
                  </option>
                ))}
              </select>
            </KitField>
          )}
          {others.length > 0 && (
            <div className="row-actions">
              <KitField label="Hand the basket to">
                <select value={handTo} onChange={(e) => setHandTo(e.target.value)} data-testid="basket-hand-to">
                  <option value="">Choose someone</option>
                  {others.map((p) => (
                    <option key={p.person_id} value={p.person_id}>
                      {p.name}
                    </option>
                  ))}
                </select>
              </KitField>
              <button type="button" disabled={busy || !handTo} onClick={() => void run(() => put(`${base(b.id)}/holder`, { person_id: handTo }), "Handed on")} data-testid="basket-hand">
                Hand on
              </button>
            </div>
          )}
          <button type="button" className="danger" disabled={busy} onClick={() => void run(() => post(`${base(b.id)}/cancel`), "Basket cancelled")} data-testid="basket-cancel">
            Cancel the basket
          </button>
        </div>
      )}
      <p className="hint people-kept">
        <Icon name="info" /> {b.business} sees one order from the one who places it, with a note of whose items are whose. It never sees this {b.where === "group" ? "group" : "chat"}. Taskiem moves no money between you.
      </p>
    </div>
  );
}

/** Your own items, from the shop through the basket door. */
function MineStep({ b, onDone }: { b: Basket; onDone: (b: Basket) => void }) {
  const shop = useLoad(() => get<TheirShop>(`${base(b.id)}/shop`), [b.id]);
  const [lines, setLines] = useState<BasketLine[]>(() => myLines(b));
  const [sizeOf, setSizeOf] = useState<Record<string, string>>({});
  const act = useAction();
  const s = shop.data;
  const qty = (id: string) => lines.find((l) => l.item_id === id)?.quantity ?? 0;
  return (
    <div data-testid="basket-shop">
      <ErrorBox error={shop.error ?? act.error} />
      {!s && !shop.error && <KitLoading label="Loading their shop…" />}
      {s && (
        <ul className="pc-items" aria-label={`${b.business}'s items`}>
          {s.items.map((it) => (
            <ShopRow
              key={it.id}
              it={it}
              open={s.open}
              qty={qty}
              size={sizeOf[it.id] ?? ""}
              setSize={(v) => setSizeOf((m) => ({ ...m, [it.id]: v }))}
              change={(line, by) => setLines((cur) => changeBasket(cur, line, by))}
            />
          ))}
        </ul>
      )}
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={act.busy}
          data-testid="basket-save-mine"
          onClick={() =>
            void act.run(async () => onDone(await put<Basket>(`${base(b.id)}/mine`, { lines: lines.map((l) => ({ item_id: l.item_id, quantity: l.quantity })) })), "Your items are in the basket")
          }
        >
          {lines.length === 0 ? "Take my items out" : `Put my ${lines.reduce((n, l) => n + l.quantity, 0)} in the basket`}
        </button>
      </div>
    </div>
  );
}

/** Placing: who pays, where it goes, a note. */
function PlaceStep({ b, onDone }: { b: Basket; onDone: (b: Basket) => void }) {
  const each = !!b.shop?.each_offered;
  const words = payChoiceWords(each, b.business);
  const [payment, setPayment] = useState<"all" | "each">("all");
  const [address, setAddress] = useState("");
  const [landmark, setLandmark] = useState("");
  const [note, setNote] = useState("");
  const act = useAction();
  return (
    <form
      className="pc-check"
      data-testid="basket-place-form"
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          const out = await post<{ basket: Basket }>(`${base(b.id)}/place`, { payment, address, landmark, note, total_kobo: b.total_kobo });
          onDone(out.basket);
        }, "Order placed");
      }}
    >
      <p>
        You place this as your own order of {naira(b.total_kobo)} with {b.business}.
      </p>
      <fieldset>
        <legend>Who pays</legend>
        <label>
          <input type="radio" name="pay" checked={payment === "all"} onChange={() => setPayment("all")} data-testid="basket-pay-all" /> {words.all}
        </label>
        {each && (
          <label>
            <input type="radio" name="pay" checked={payment === "each"} onChange={() => setPayment("each")} data-testid="basket-pay-each" /> {words.each}
          </label>
        )}
        <p className="hint">{words.note}</p>
      </fieldset>
      {b.fulfilment === "delivery" && (
        <>
          <KitField label="Address to deliver to">
            <input value={address} maxLength={300} required onChange={(e) => setAddress(e.target.value)} data-testid="basket-address" />
          </KitField>
          <KitField label="Landmark" hint="Optional">
            <input value={landmark} maxLength={300} onChange={(e) => setLandmark(e.target.value)} />
          </KitField>
        </>
      )}
      <KitField label="A note for the business" hint="Optional">
        <input value={note} maxLength={500} onChange={(e) => setNote(e.target.value)} />
      </KitField>
      <ErrorBox error={act.error} />
      <button type="submit" className="primary" disabled={act.busy} data-testid="basket-place-send">
        Place the order
      </button>
    </form>
  );
}

/** The account the share cards ask to be paid to, when none is saved. */
function AccountStep({ b, onDone }: { b: Basket; onDone: (b: Basket) => void }) {
  const [bank, setBank] = useState("");
  const [number, setNumber] = useState("");
  const [name, setName] = useState("");
  const act = useAction();
  return (
    <form
      className="pc-check"
      onSubmit={(e) => {
        e.preventDefault();
        void act.run(async () => {
          const out = await post<{ basket: Basket }>(`${base(b.id)}/share-cards`, { pay_to: { bank, account_number: number, account_name: name } });
          onDone(out.basket);
        }, "Share cards sent");
      }}
    >
      <p>Where should they pay you back? Taskiem moves no money: they pay with their bank app.</p>
      <KitField label="Bank">
        <input value={bank} required onChange={(e) => setBank(e.target.value)} data-testid="basket-acct-bank" />
      </KitField>
      <KitField label="Account number">
        <input inputMode="numeric" value={number} required onChange={(e) => setNumber(e.target.value)} data-testid="basket-acct-number" />
      </KitField>
      <KitField label="Name on the account">
        <input value={name} required onChange={(e) => setName(e.target.value)} data-testid="basket-acct-name" />
      </KitField>
      <ErrorBox error={act.error} />
      <button type="submit" className="primary" disabled={act.busy} data-testid="basket-acct-send">
        Send each their share
      </button>
    </form>
  );
}
