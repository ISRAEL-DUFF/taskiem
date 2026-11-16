import { useState } from "react";
import { get } from "../api";
import { Icon } from "../icons";
import { KitEmpty, KitField, KitLoading, Sheet } from "../kit";
import type { Services } from "../lib/bookings";
import type { CardAsk } from "../lib/customerlinks";
import type { OrdersOfChat } from "../lib/customerorders";
import { minutesWords } from "../lib/personchat";
import { formatPrice } from "../lib/storefront";
import { ErrorBox, useLoad } from "../ui";

// Rich replies to a customer in the app (docs/spec/taskiem-for-everyone.md
// §8.3): an order's card, its pay link, a booking offer (one of the
// business's services) or a product from the catalogue (the "#" of the
// chat), sent as a card the customer acts on. The server makes each card
// from the business's own data.

type Tab = "order" | "pay" | "booking" | "product";

const TABS: { key: Tab; words: string }[] = [
  { key: "order", words: "Order" },
  { key: "pay", words: "Pay link" },
  { key: "booking", words: "Booking" },
  { key: "product", words: "Product" },
];

export function CardSheet({
  chat,
  name,
  references,
  start = "order",
  onSend,
  onClose,
}: {
  chat: string;
  name: string;
  /** Orders this conversation knows of (its cards). */
  references: string[];
  start?: Tab;
  onSend: (card: CardAsk, words: string) => void;
  onClose: () => void;
}) {
  const [tab, setTab] = useState<Tab>(start);
  const [typed, setTyped] = useState("");
  const [q, setQ] = useState("");
  const orders = useLoad(() => get<OrdersOfChat>(`/v1/customer-chats/${encodeURIComponent(chat)}/orders`), [chat]);
  const services = useLoad(() => (tab === "booking" ? get<Services>("/v1/storefront/services") : Promise.resolve(undefined)), [tab === "booking"]);
  const refs = [...new Set([...(orders.data?.orders ?? []).map((d) => d.reference).filter((r): r is string => !!r), ...references])];
  const items = (orders.data?.items ?? []).filter((it) => !q.trim() || it.name.toLowerCase().includes(q.trim().toLowerCase()));
  const pick = (card: CardAsk, words: string) => {
    onSend(card, words);
    onClose();
  };
  return (
    <Sheet title={`Send ${name} a card`} onClose={onClose} testid="card-sheet">
      <div className="tk-chips" role="group" aria-label="What to send">
        {TABS.map((t) => (
          <button key={t.key} type="button" aria-pressed={tab === t.key} onClick={() => setTab(t.key)}>
            {t.words}
          </button>
        ))}
      </div>
      {(tab === "order" || tab === "pay") && (
        <>
          <p className="hint">{tab === "pay" ? "A link to pay what is due on one of their orders." : "One of their orders, with where it is."}</p>
          {!orders.data && !orders.error && <KitLoading rows={2} label="Loading their orders…" />}
          <ErrorBox error={orders.error} />
          {refs.length > 0 && (
            <ul className="pc-tiles" aria-label="Their orders">
              {refs.map((r) => (
                <li key={r}>
                  <button type="button" className="tk-row pc-tile" onClick={() => pick({ type: tab, reference: r }, `${tab === "pay" ? "Pay link for" : "Order"} ${r}`)}>
                    <span className="tk-row-tile" aria-hidden="true">
                      <Icon name={tab === "pay" ? "money" : "orders"} />
                    </span>
                    <span className="tk-row-words">
                      <span className="tk-row-name">{r}</span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
          <form
            className="pc-check"
            onSubmit={(e) => {
              e.preventDefault();
              const r = typed.trim();
              if (r) pick({ type: tab, reference: r }, `${tab === "pay" ? "Pay link for" : "Order"} ${r}`);
            }}
          >
            <KitField label="Or type its reference">
              <input value={typed} onChange={(e) => setTyped(e.target.value)} placeholder="MK-2X4F" />
            </KitField>
            <button type="submit" className="tk-btn soft" disabled={!typed.trim()}>
              Send
            </button>
          </form>
        </>
      )}
      {tab === "booking" && (
        <>
          <p className="hint">One of your services: they choose a free time and book it.</p>
          {!services.data && !services.error && <KitLoading rows={2} label="Loading your services…" />}
          <ErrorBox error={services.error} />
          {services.data && services.data.items.length === 0 && <KitEmpty icon="calendar" title="No services yet">Add services to your shop to offer bookings.</KitEmpty>}
          <ul className="pc-tiles" aria-label="Your services">
            {(services.data?.items ?? []).map((s) => (
              <li key={s.id}>
                <button type="button" className="tk-row pc-tile" onClick={() => pick({ type: "booking", service_id: s.id }, `Book ${s.name}`)}>
                  <span className="tk-row-tile" aria-hidden="true">
                    <Icon name="calendar" />
                  </span>
                  <span className="tk-row-words">
                    <span className="tk-row-name">{s.name}</span>
                    <span className="tk-row-sub">
                      {[s.duration_minutes ? minutesWords(s.duration_minutes) : "", s.price_kobo !== undefined ? formatPrice(s.price_kobo) : ""].filter(Boolean).join(" · ")}
                    </span>
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
      {tab === "product" && (
        <>
          <KitField label="Find a product">
            <input type="search" value={q} onChange={(e) => setQ(e.target.value)} />
          </KitField>
          {!orders.data && !orders.error && <KitLoading rows={2} label="Loading your products…" />}
          {orders.data && items.length === 0 && <KitEmpty icon="tag" title="No products found" />}
          <ul className="pc-tiles" aria-label="Your products">
            {items.map((it) => (
              <li key={it.id}>
                <button type="button" className="tk-row pc-tile" onClick={() => pick({ type: "product", item_id: it.id }, it.name)}>
                  <span className="tk-row-tile" aria-hidden="true">
                    {it.name.slice(0, 1).toUpperCase()}
                  </span>
                  <span className="tk-row-words">
                    <span className="tk-row-name">{it.name}</span>
                    {it.price_kobo !== undefined && <span className="tk-row-sub">{formatPrice(it.price_kobo)}</span>}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        </>
      )}
    </Sheet>
  );
}
