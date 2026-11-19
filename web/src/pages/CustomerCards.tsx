import { useState } from "react";
import { ApiError, get } from "../api";
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

type Tab = "order" | "pay" | "delivery" | "invoice" | "booking" | "product";

const TABS: { key: Tab; words: string }[] = [
  { key: "order", words: "Order" },
  { key: "pay", words: "Pay link" },
  { key: "delivery", words: "Delivery" },
  { key: "invoice", words: "Invoice" },
  { key: "booking", words: "Booking" },
  { key: "product", words: "Product" },
];

/** What a card about an order says in the list before the server's copy. */
const refWords: Record<"order" | "pay" | "delivery", { hint: string; words: string; icon: string }> = {
  order: { hint: "One of their orders, with where it is.", words: "Order", icon: "orders" },
  pay: { hint: "A link to pay what is due on one of their orders.", words: "Pay link for", icon: "money" },
  delivery: { hint: "Where the rider is with one of their orders, once a rider has it.", words: "Delivery of", icon: "package" },
};

/** An invoice of this customer's, by its number (INV-0001): found, then
 * sent as a card with what is due and its pay page. */
function InvoicePick({ onPick }: { onPick: (card: CardAsk, words: string) => void }) {
  const [number, setNumber] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>();
  return (
    <form
      className="pc-check"
      onSubmit={(e) => {
        e.preventDefault();
        const n = number.trim();
        if (!n || busy) return;
        setBusy(true);
        setError(undefined);
        get<{ invoice: { id: string; number: string } }>(`/v1/invoices/${encodeURIComponent(n)}`)
          .then(
            ({ invoice: x }) => onPick({ type: "invoice", invoice_id: x.id }, `Invoice ${x.number}`),
            (err: unknown) => setError(err instanceof ApiError && err.status === 404 ? new Error(`No invoice is numbered ${n}.`) : err),
          )
          .finally(() => setBusy(false));
      }}
    >
      <p className="hint">One of their invoices, sent already: what is due, and its pay page while something is.</p>
      <KitField label="The invoice's number">
        <input value={number} onChange={(e) => setNumber(e.target.value)} placeholder="INV-0001" data-testid="card-invoice-number" />
      </KitField>
      <ErrorBox error={error} />
      <button type="submit" className="tk-btn soft" disabled={!number.trim() || busy}>
        {busy ? "Finding it…" : "Send"}
      </button>
    </form>
  );
}

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
      {(tab === "order" || tab === "pay" || tab === "delivery") && (
        <>
          <p className="hint">{refWords[tab].hint}</p>
          {!orders.data && !orders.error && <KitLoading rows={2} label="Loading their orders…" />}
          <ErrorBox error={orders.error} />
          {refs.length > 0 && (
            <ul className="pc-tiles" aria-label="Their orders">
              {refs.map((r) => (
                <li key={r}>
                  <button type="button" className="tk-row pc-tile" onClick={() => pick({ type: tab, reference: r }, `${refWords[tab].words} ${r}`)}>
                    <span className="tk-row-tile" aria-hidden="true">
                      <Icon name={refWords[tab].icon} />
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
              if (r) pick({ type: tab, reference: r }, `${refWords[tab].words} ${r}`);
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
      {tab === "invoice" && <InvoicePick onPick={pick} />}
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
