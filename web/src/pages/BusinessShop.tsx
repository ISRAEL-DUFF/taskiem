// A business's shop (docs/spec/mobile-app-look.md, "The business shop";
// docs/spec/business-links.md §5.5; the app's mobile/lib/chat/business_shop.dart):
// a linked supplier's things to window shop from the conversation with them,
// in the person's shop's views (Shop.tsx ShopFrame). What differs comes from
// the Order sheet (ChatBuyer.tsx), which stays the plain list:
//
// - the prices are theirs for this link (GET /v1/supplier-prices?link=), the
//   price list they share with us, else their storefront's; with their
//   quantity prices, a product's sizes counted together (lib/buyer.ts bagOf);
// - + from nothing goes to their minimum; a product sold in sizes is ordered
//   in the Order sheet's sizes grid, on its item's view;
// - the bag checks out in the Order sheet's review (which supplier they are,
//   how each line is treated, a note, Make an offer, Place order or Ask for a
//   quote): a purchase order, its card in the conversation;
// - only those who may order (the sheet's browse otherwise) have a bag;
// - the hearts and what was looked at are kept in this browser by the
//   business we act for and the supplier (lib/shopkept.ts), and forgotten on
//   signing out, as the person's are.

import { useMemo, useRef, useState } from "react";
import { get } from "../api";
import { useAuth } from "../auth";
import { bagOf, flatItems, soldInSizes, stepQty, STOREFRONT_PRICES } from "../lib/buyer";
import { shopItemOf, shortWords, termsOf } from "../lib/businessshop";
import type { BasketLine, TheirShop } from "../lib/personchat";
import type { SupplierPrices } from "../lib/pricelists";
import { useLoad } from "../ui";
import { OrderSheet, SizesQty } from "./ChatBuyer";
import { emptyBag, lineOf, ShopFrame, useBag, type ShopTrade } from "./Shop";

/** The bag's lines as quantities by item. */
const qtyOf = (lines: BasketLine[]) => Object.fromEntries(lines.map((l) => [l.item_id, l.quantity]));

export function BusinessShop({
  link,
  business,
  businessId,
  logo,
  supplies,
  startItem,
  onClose,
  onAsk,
  onShare,
  onPlaced,
}: {
  link: string;
  business: string;
  /** Their business: whose hearts and lately-seen items these are, with ours. */
  businessId: string;
  logo?: string;
  /** We may order from them (drafting needs table.edit; sending and the
   * supplier, storefront.manage): else their shop is to look around. */
  supplies: boolean;
  startItem?: string;
  onClose: () => void;
  /** Ask about this: the question begun in the conversation's box. */
  onAsk: (words: string) => void;
  onShare?: (it: { id: string; name: string }) => void;
  /** Sent (an order, an offer, a quote asked), with its words. */
  onPlaced?: (words: string) => void;
}) {
  const { me } = useAuth();
  const prices = useLoad(() => get<{ suppliers: SupplierPrices[] }>(`/v1/supplier-prices?link=${encodeURIComponent(link)}`), [link]);
  const list = prices.data?.suppliers[0];
  const items = useMemo(() => list?.items ?? [], [list]);
  const flat = useMemo(() => flatItems(items), [items]);
  const storefront = list?.name === STOREFRONT_PRICES;
  const today = !!list?.updated_at && new Date(list.updated_at).toDateString() === new Date().toDateString();
  const bagKey = `link:${link}`;
  const [bag, putBag] = useBag(bagKey);
  // The Order sheet over the shop: the plain list, or the bag's review.
  const [sheet, setSheet] = useState<{ review: boolean; qty: Record<string, number> }>();
  const placed = useRef(false);
  const shop: TheirShop | undefined = prices.data && {
    items: items.map((it) => shopItemOf(it, link)),
    open: supplies,
    closed: supplies ? undefined : `You can look around. Placing orders with ${business} is for those on your team who may.`,
    pickup: false,
    delivery: false,
    zones: [],
    payment: "none",
  };
  const byId = new Map((shop?.items ?? []).map((i) => [i.id, i]));
  // The bag's lines for quantities by item (the plain list changed them).
  const linesOf = (qty: Record<string, number>): BasketLine[] =>
    Object.entries(qty).flatMap(([id, n]) => {
      if (!(n > 0)) return [];
      const it = byId.get(id) ?? [...byId.values()].find((i) => i.sizes?.some((z) => z.id === id));
      const line = it && lineOf(it, it.id === id ? undefined : id);
      return line ? [{ ...line, quantity: n }] : [];
    });
  const trade: ShopTrade = {
    priceIn: (id, lines) => {
      const it = flat.find((x) => x.item_id === id);
      return it ? bagOf(items, qtyOf(lines)).unitPrice(it) : (lines.find((l) => l.item_id === id)?.price_kobo ?? 0);
    },
    step: (id, n, dir) => {
      const it = flat.find((x) => x.item_id === id);
      // A size: one at a time (its product's minimum counts its sizes
      // together, said in the bag).
      if (!it || it.variant_of) return Math.max(0, n + dir);
      return stepQty(n, it.min_quantity, dir);
    },
    terms: (id) => {
      const it = items.find((x) => x.item_id === id);
      return it && termsOf(it);
    },
    sizes: (shown, qty, set) => {
      const it = items.find((x) => x.item_id === shown.id);
      if (!it || !soldInSizes(it) || !it.sizes?.length || !it.available) return undefined;
      return (
        <SizesQty
          product={it}
          qty={Object.fromEntries(it.sizes.map((z) => [z.item_id, qty(z.item_id)]))}
          onQty={(id, n) => {
            const line = lineOf(shown, id);
            if (line) set(line, n);
          }}
        />
      );
    },
    stops: (lines) => shortWords(items, qtyOf(lines)),
    checkoutWords: "Review order",
    onList: () => setSheet({ review: false, qty: qtyOf(bag) }),
  };
  return (
    <>
      <ShopFrame
        bagKey={bagKey}
        keptKey={`business:${me?.tenant_id ?? ""}:${businessId}`}
        shop={{ data: shop, error: prices.error }}
        business={business}
        logo={logo}
        startItem={startItem}
        sub={`${storefront ? "Their storefront prices" : "Your prices"}${today ? " · updated today" : ""}`}
        next="Their prices for these quantities. Next, the review: how each line is treated, a note or an offer, then Place order."
        bags={supplies}
        trade={trade}
        onClose={onClose}
        onAsk={onAsk}
        onShare={storefront ? onShare : undefined}
        onCheckout={(lines) => setSheet({ review: true, qty: qtyOf(lines) })}
      />
      {sheet && (
        <OrderSheet
          link={link}
          business={business}
          start={{ qty: sheet.qty, review: sheet.review }}
          browse={!supplies}
          onQty={sheet.review ? undefined : (qty) => putBag(linesOf(qty))}
          onPlaced={(words) => {
            placed.current = true;
            onPlaced?.(words);
          }}
          onClose={() => {
            setSheet(undefined);
            // Sent: the bag went with it, and its card is in the conversation.
            if (placed.current) {
              placed.current = false;
              emptyBag(bagKey);
              onClose();
            }
          }}
        />
      )}
    </>
  );
}
