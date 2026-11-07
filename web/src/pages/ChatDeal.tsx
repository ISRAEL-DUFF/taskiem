import { useState } from "react";
import { post } from "../api";
import { useAuth } from "../auth";
import { KitField } from "../kit";
import { KEEP_CHOICES, dealKobo, offWords } from "../lib/deals";
import type { DocCard } from "../lib/linkchat";
import { formatPrice } from "../lib/storefront";
import { toast } from "../toast";
import { ErrorBox, useAction } from "../ui";
import { useConfirm } from "./ConfirmSheet";

// A deal's buttons on its card in the conversation (docs/spec/chat-tools.md
// §9, phase 7). The seller, on the buyer's offer or counter: Accept (for
// those who set prices, keeping the price for this buyer a while), Counter
// with a total, Decline. The buyer, on the seller's counter: Accept it, or
// Counter back. Staff accepting below the lowest price wait for the owner.

export function DealActions({
  card,
  side,
  onDone,
}: {
  card: DocCard;
  side: "seller" | "buyer";
  onDone: () => void;
}) {
  const { can } = useAuth();
  const act = useAction();
  const { confirm, node: confirmNode } = useConfirm();
  const [countering, setCountering] = useState(false);
  const [text, setText] = useState("");
  const [note, setNote] = useState("");
  const [keep, setKeep] = useState(0);
  const id = encodeURIComponent(card.id ?? card.reference);
  const ref = <span className="sr-only"> {card.reference}</span>;
  const seller = side === "seller";
  const mayAct = seller
    ? can("storefront.manage|order.handle")
    : can("storefront.manage");
  const mayCounter = seller
    ? can("storefront.manage|order.handle")
    : can("table.edit");
  const setsPrices = seller && can("storefront.manage");
  const amount = card.amount_kobo ?? 0;
  const kobo = dealKobo(text);
  // A counter sits between the two: above what they said, under the price.
  const problem =
    text.trim() === ""
      ? ""
      : !(kobo > 0)
        ? "A price in naira, like 770,000 or 770k."
        : seller && kobo <= amount
          ? `A counter is more than their ${formatPrice(amount)}.`
          : !seller && kobo >= amount
            ? `A counter is under their ${formatPrice(amount)}.`
            : "";

  const accept = () =>
    void act.run(async () => {
      if (seller) {
        const out = await post<{ waiting?: string; why?: string[] }>(
          `/v1/trade-orders/${id}/accept`,
          keep ? { keep_days: keep } : undefined,
        );
        if (out?.waiting === "owner") {
          toast(
            `Under your lowest price: ${card.reference} waits for the owner's OK`,
            "info",
          );
          onDone();
          return;
        }
        toast(
          keep
            ? `Agreed ${formatPrice(amount)}, kept for them ${keep} days`
            : `Agreed ${formatPrice(amount)} on ${card.reference}`,
        );
      } else {
        await post(`/v1/stock/purchases/${id}/accept-change`);
        toast(`Agreed ${formatPrice(amount)} on ${card.reference}`);
      }
      onDone();
    }, false);

  const counter = () =>
    void act.run(
      async () => {
        await post(
          seller
            ? `/v1/trade-orders/${id}/change`
            : `/v1/stock/purchases/${id}/counter`,
          { total_kobo: kobo, note: note.trim() || undefined },
        );
        setCountering(false);
        setText("");
        setNote("");
        onDone();
      },
      `Counter sent: ${formatPrice(kobo)}`,
    );

  const decline = async () => {
    if (
      !(await confirm({
        title: seller
          ? `Decline ${card.reference}?`
          : `Cancel ${card.reference}?`,
        ok: seller ? "Decline" : "Cancel the order",
        danger: true,
      }))
    )
      return;
    void act.run(
      async () => (
        await post(
          seller
            ? `/v1/trade-orders/${id}/decline`
            : `/v1/stock/purchases/${id}/cancel`,
        ),
        onDone()
      ),
      seller ? `${card.reference} declined` : `${card.reference} cancelled`,
    );
  };

  if (!mayAct && !mayCounter) return null;
  return (
    <span className="tk-deal-form" data-testid="deal-actions">
      {!countering && setsPrices && (
        <KitField label="Keep this price for them">
          <select
            value={keep}
            onChange={(e) => setKeep(Number(e.target.value))}
          >
            {KEEP_CHOICES.map((c) => (
              <option key={c.days} value={c.days}>
                {c.label}
              </option>
            ))}
          </select>
        </KitField>
      )}
      {countering && (
        <>
          <KitField
            label={seller ? "Your price for the whole order" : "Your counter"}
            error={problem || undefined}
            hint={
              kobo > 0 && !problem && card.was_kobo
                ? `${offWords(kobo, card.was_kobo) || "Their full price"}${seller ? " your price" : " their price"}`
                : undefined
            }
          >
            <input
              inputMode="decimal"
              autoComplete="off"
              value={text}
              onChange={(e) => setText(e.target.value)}
              placeholder={seller ? "770,000" : "760,000"}
            />
          </KitField>
          <KitField label="A word with it (optional)">
            <input
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="Best I can do"
            />
          </KitField>
        </>
      )}
      <span className="tk-deal-row">
        {countering ? (
          <>
            <button
              type="button"
              className="small primary"
              disabled={act.busy || !(kobo > 0) || !!problem}
              onClick={counter}
            >
              Send counter{ref}
            </button>
            <button
              type="button"
              className="small"
              disabled={act.busy}
              onClick={() => setCountering(false)}
            >
              Back
            </button>
          </>
        ) : (
          <>
            {mayAct && (
              <button
                type="button"
                className="small primary"
                disabled={act.busy}
                onClick={accept}
              >
                Accept {formatPrice(amount)}
                {ref}
              </button>
            )}
            {mayCounter && (
              <button
                type="button"
                className="small"
                disabled={act.busy}
                onClick={() => setCountering(true)}
              >
                Counter{ref}
              </button>
            )}
            {mayAct && (
              <button
                type="button"
                className="small"
                disabled={act.busy}
                onClick={() => void decline()}
              >
                {seller ? "Decline" : "Cancel"}
                {ref}
              </button>
            )}
          </>
        )}
      </span>
      {confirmNode}
      {!!act.error && <ErrorBox error={act.error} />}
    </span>
  );
}
