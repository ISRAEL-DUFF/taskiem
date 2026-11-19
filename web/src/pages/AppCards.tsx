import type { ReactNode } from "react";
import { Icon } from "../icons";
import { Pill } from "../kit";
import { fileSize } from "../lib/linkchat";
import { cardFigure, cardKicker, cardState, deliveryStep, type AppAttachment, type AppCard } from "../lib/personchat";

// What an app conversation carries besides words (docs/spec/taskiem-for-
// everyone.md §8.3, §8.5): a card (an order, a pay link, a booking or a
// booking offer, a product, an invoice, a delivery with the rider's
// progress) and a photo, voice note or file. The same on the person's side
// and the business's; what can be done with a card is given by the side
// that shows it.

const ICON: Record<AppCard["type"], string> = {
  order: "orders",
  pay: "money",
  booking: "calendar",
  product: "tag",
  invoice: "invoices",
  delivery: "package",
};

/** A card in a conversation; `actions` are the side's own buttons. */
export function AppCardView({ card, actions }: { card: AppCard; actions?: ReactNode }) {
  const st = cardState(card);
  const step = card.type === "delivery" && card.status ? deliveryStep({ status: card.status as "accepted" }) : 0;
  const figure = cardFigure(card);
  const label = `${cardKicker(card)}: ${card.title}`;
  return (
    <article className="tk-ref app-card" aria-label={label} data-testid="app-card" data-type={card.type}>
      <span className="tk-ref-top">
        <span className="tk-ref-tile order" aria-hidden="true">
          <Icon name={ICON[card.type]} />
        </span>
        <span className="tk-ref-words">
          <span className="tk-kicker">{cardKicker(card)}</span>
          <span className="tk-ref-name">{card.title}</span>
          {card.line && <span className="tk-ref-sub">{card.line}</span>}
          {figure && <span className="tk-ref-sub app-card-figure">{figure}</span>}
        </span>
        {card.type !== "pay" && st.label && (
          <span className="tk-ref-pill">
            <Pill tone={st.tone}>{st.label}</Pill>
          </span>
        )}
      </span>
      {step > 0 && (
        <span className="app-card-steps" role="img" aria-label={`${st.label || "Delivery"}: step ${step} of 4`} data-testid="delivery-steps">
          {[1, 2, 3, 4].map((n) => (
            <span key={n} className={n <= step ? "done" : undefined} />
          ))}
        </span>
      )}
      {actions && <span className="tk-ref-foot">{actions}</span>}
    </article>
  );
}

/** A message's photo, voice note or file, from `src` (the side's own copy). */
export function AppAttachmentView({ a, src, who, mine, preview }: { a: AppAttachment; src?: string; who: string; mine: boolean; preview?: string }) {
  const name = a.kind === "photo" ? "Photo" : a.kind === "voice" ? "Voice note" : a.name || "File";
  if (!src) {
    // Still being sent: this device's own copy (a preview, or the name).
    if (a.kind === "photo" && preview)
      return (
        <span className="chat-photo">
          <img src={preview} alt="Your photo" />
        </span>
      );
    return (
      <span className="chat-file">
        <Icon name="file" />
        <span className="name">{name}</span>
        <span className="hint">{fileSize(a.size)}</span>
      </span>
    );
  }
  if (a.kind === "photo")
    return (
      <a href={src} target="_blank" rel="noopener" className="chat-photo">
        <img src={src} alt={mine ? "Your photo" : `Photo from ${who}`} loading="lazy" />
      </a>
    );
  if (a.kind === "voice") return <audio className="chat-voice" controls preload="none" src={src} aria-label={mine ? "Your voice note" : `Voice note from ${who}`} />;
  return (
    <a href={src} download={a.name || true} className="chat-file">
      <Icon name="file" />
      <span className="name">{name}</span>
      <span className="hint">{fileSize(a.size)}</span>
    </a>
  );
}
