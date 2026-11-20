import { useState } from "react";
import { ApiError } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { cardSub, KIND_WORDS, type SavedCard } from "../lib/saved";
import { Copy } from "./Forms";
import { AccountSheet, ThingSheet } from "./Saved";

// A saved thing shared in a chat (saved objects S1, part 3; saved-objects
// §6.2): a copy of it as it was sent, folded to a row like orders' cards,
// opening to each detail with Copy beside it, and Save this for whoever
// received it, as a saved thing of their own.

/** saveTo: where Save this keeps it, the person's own (mine) or, read in
 * a business's chat, the business's. */
export function SavedCardView({ card, mine, saveTo = "mine" }: { card: SavedCard; mine: boolean; saveTo?: "mine" | "business" }) {
  const { can } = useAuth();
  // The business's only for whoever may manage its saved things; else
  // one's own.
  const to = saveTo === "business" && can("saved.manage") ? "business" : "mine";
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const icon = card.kind === "custom" ? "box" : (KIND_WORDS[card.kind]?.icon ?? "note");
  const label = `${card.title}, shared`;
  if (!open)
    return (
      <article className="tk-ref tk-ref-saved tk-ref-folded" aria-label={label} data-testid="saved-card">
        <button type="button" className="tk-ref-row" aria-expanded={false} onClick={() => setOpen(true)}>
          <span className="tk-ref-tile" aria-hidden="true">
            <Icon name={icon} />
          </span>
          <span className="tk-ref-words">
            <span className="tk-ref-name">{card.title}</span>
            <span className="tk-ref-sub">{cardSub(card)}</span>
          </span>
          <span className="tk-ref-chevron" aria-hidden="true">
            <Icon name="chevron" />
          </span>
          <span className="sr-only">Show details</span>
        </button>
      </article>
    );
  return (
    <article className="tk-ref tk-ref-saved" aria-label={label} data-testid="saved-card">
      <button type="button" className="tk-ref-top" aria-expanded={true} onClick={() => setOpen(false)}>
        <span className="tk-ref-tile" aria-hidden="true">
          <Icon name={icon} />
        </span>
        <span className="tk-ref-words">
          <span className="tk-kicker">{cardSub(card).split(" · ")[0]}</span>
          <span className="tk-ref-name">{card.title}</span>
          <span className="tk-ref-sub">Shared as @{card.name}</span>
        </span>
        <span className="tk-ref-chevron open" aria-hidden="true">
          <Icon name="chevron" />
        </span>
        <span className="sr-only">Show less</span>
      </button>
      <ul className="tk-ref-lines" aria-label={`${card.title}'s details`}>
        {card.lines.map((l, i) => (
          <li key={i} data-testid="saved-card-line">
            <span className="tk-ref-line-words">
              {l.label !== "Text" && <span className="tk-ref-line-label">{l.label}</span>}
              <b>{l.value}</b>
            </span>
            {/* One's own copy is masked: nothing there to copy. */}
            {!(mine && l.sensitive) && <Copy text={l.value} label={`Copy ${l.label === "Text" ? "the text" : l.label.toLowerCase()}`} />}
          </li>
        ))}
      </ul>
      {!mine && (
        <div className="tk-ref-foot">
          <button type="button" className="tk-btn soft" onClick={() => setSaving(true)} data-testid="saved-card-save">
            Save this
          </button>
        </div>
      )}
      {saving &&
        (card.kind === "bank_account" ? (
          <AccountSheet mine={to === "mine"} checks={false} start={card} onClose={() => setSaving(false)} onSaved={() => setSaving(false)} />
        ) : (
          // A made-up kind's card is kept as a text of its lines: the
          // receiver has no such kind.
          <ThingSheet
            mine={to === "mine"}
            kind={card.kind === "custom" ? "text" : card.kind}
            start={card.kind === "custom" ? { ...card, kind: "text" } : card}
            onClose={() => setSaving(false)}
            onSaved={() => setSaving(false)}
          />
        ))}
    </article>
  );
}

/** Sends a saved thing as a card: once, and when the server asks for a yes
 * to sharing what is sensitive in it (409 confirm_share, its words the
 * question), asks the sender and sends again with it. Undefined when the
 * sender said no. */
export async function sendSaved<T>(send: (confirm: boolean) => Promise<T>, ask: (question: string) => Promise<boolean>): Promise<T | undefined> {
  try {
    return await send(false);
  } catch (e) {
    if (e instanceof ApiError && e.status === 409 && e.body.confirm_share) {
      return (await ask(e.message)) ? send(true) : undefined;
    }
    throw e;
  }
}
