import { Link } from "react-router-dom";
import { Icon } from "../icons";
import { Pill } from "../kit";
import type { Mention } from "../lib/mentions";
import type { PriceItem } from "../lib/pricelists";
import { quantity, type StockItem } from "../lib/stock";
import { formatPrice } from "../lib/storefront";

// A live card (docs/spec/chat-tools.md §3, phase 2): a product or a service
// tagged in a message, shown as a card with what is true now: the price on
// the conversation's price list today (and what it was when tagged), your
// own stock of it, and Order when you buy from them.

/** What the cards read live, from the conversation. */
export interface Live {
  /** The conversation's price list, and whose it is. */
  items: PriceItem[];
  whose: "ours" | "theirs" | "";
  /** Our own stock (when we may see it). */
  stock: StockItem[];
  /** The conversation (for Order). */
  link: string;
}

const same = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase();

/** The things a message tags that have a card: its first product or service. */
export function liveMention(mentions: Mention[] | undefined): Mention | undefined {
  return mentions?.find((m) => m.kind === "item" || m.kind === "service");
}

export function LiveCard({ m, live }: { m: Mention; live: Live }) {
  if (m.kind === "service")
    return (
      <article className="tk-live" aria-label={`Service: ${m.name}`}>
        <span className="tk-live-top">
          <span className="tk-live-tile blue" aria-hidden="true">
            <Icon name="clock" />
          </span>
          <span className="tk-live-words">
            <span className="tk-kicker">Service</span>
            <span className="tk-live-name">{m.name}</span>
            <span className="tk-live-sub">
              {m.price_kobo ? formatPrice(m.price_kobo) : "Price on the day"}
              {m.minutes ? ` · ${m.minutes} min` : ""}
            </span>
          </span>
        </span>
      </article>
    );
  const now = live.items.find((it) => it.item_id === m.item_id);
  const price = now?.price_kobo ?? m.price_kobo;
  const changed = now && m.price_kobo !== undefined && now.price_kobo !== m.price_kobo;
  const unit = now?.unit ?? m.unit;
  const mine = live.stock.find((it) => same(it.name, m.name));
  return (
    <article className="tk-live" aria-label={`Product: ${m.name}`}>
      <span className="tk-live-top">
        <span className="tk-live-tile" aria-hidden="true">
          <Icon name="box" />
        </span>
        <span className="tk-live-words">
          <span className="tk-kicker">Product</span>
          <span className="tk-live-name">{m.name}</span>
          <span className="tk-live-sub">
            {price !== undefined ? `${formatPrice(price)}${unit ? ` a ${unit}` : ""}` : "No price"}
            {now && now.min_quantity > 1 ? ` · min ${now.min_quantity}` : ""}
            {changed && m.price_kobo !== undefined ? ` (was ${formatPrice(m.price_kobo)})` : ""}
            {now && !now.available ? " · not available now" : ""}
          </span>
        </span>
      </span>
      {(mine?.stock != null || live.whose === "theirs") && (
        <span className="tk-live-foot">
          {mine && mine.stock !== null ? (
            <Pill tone={mine.low || mine.stock <= 0 ? "warn" : "plain"}>
              Your stock: {mine.stock <= 0 ? "none" : `${quantity(mine.stock, mine.unit)}${mine.low ? " left" : ""}`}
            </Pill>
          ) : (
            <span />
          )}
          {live.whose === "theirs" && now?.available !== false && (
            <Link className="tk-btn primary" to={`/storefront/suppliers/${encodeURIComponent(live.link)}/prices`}>
              Order<span className="sr-only"> {m.name}</span>
            </Link>
          )}
        </span>
      )}
    </article>
  );
}
