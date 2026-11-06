import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { del, get, post, put } from "../api";
import { useAuth } from "../auth";
import {
  attachmentLabel,
  cardHref,
  cardLine,
  cardSub,
  clock,
  dayLabel,
  fileSize,
  fileURL,
  merge,
  previewLine,
  REACTIONS,
  reactionWords,
  toggleReaction,
  type Attachment,
  type Conversation,
  type DocCard,
  type LinkMessage,
  type LinkPreview,
  type Reaction,
  logoSrc,
} from "../lib/linkchat";
import { Icon } from "../icons";
import { MentionText } from "./Mentions";
import { ChatAvatar } from "./ChatAvatar";
import { ContactInfo } from "./ContactInfo";
import { useConfirm } from "./ConfirmSheet";
import { useChatTools } from "../lib/useChatTools";
import { ORDER_STEPS, cardKicker, cardStatus, isDiary } from "../lib/cardframe";
import { DiaryLine } from "../kit";
import {
  BUSINESS_FILES,
  ComposerView,
  useComposer,
  type Sending,
} from "./ChatComposer";
import { priceWords, type PriceItem } from "../lib/pricelists";
import { firstUnread, startsRun } from "../lib/thread";
import { useThread } from "../lib/useThread";
import {
  actionError,
  asMessage,
  newClientId,
  type Outgoing,
} from "../lib/outbox";
import {
  discardOutgoing,
  onSent,
  retryOutgoing,
  sendOutgoing,
  updateOutgoing,
  useOutbox,
} from "../lib/outboxStore";
import { ThreadJump } from "./ThreadParts";
import { ChatStream } from "./ChatStream";
import { ChatHead } from "./ChatHead";
import { ChatSearchPanel } from "./ChatSearch";
import { BubbleShell, CopyItem, PopItem, Ticks } from "./BubbleShell";
import { ReadMore, useFold } from "./ReadMore";
import { useChatLive } from "./ChatLive";
import {
  linkedLine,
  roleLine,
  type BusinessLink,
  type LinkCard,
  type LinkRole,
} from "../lib/links";
import { ErrorBox, fmtTime, Modal, useAction, useLoad } from "../ui";

// The conversation with a linked business (business links L3; §5.5): the
// stream of bubbles, theirs on the left and ours on the right, grouped by
// day, with the time and the ticks in the corner, quoted replies, and the
// documents between the two businesses as cards; the composer at the
// bottom. As WhatsApp works, deliberately (§5.5): a bubble's corner shows
// the clock, as WhatsApp's does, with the time in words on hover.

const EVERY_MS = 3000;
/** The six reactions, in words (for the screen reader and the tooltip). */
const REACTION_NAMES: Record<string, string> = {
  "👍": "thumbs up",
  "❤️": "heart",
  "😂": "laughing",
  "😮": "surprise",
  "😢": "sad",
  "🙏": "thanks",
};
/** A page of the conversation, as the API gives it. */
const PAGE = 50;

/** What can be attached: a photo or a PDF (a voice note is recorded). */

/** What can be done from a card, while it is its document's newest:
 * accept or decline a new order, accept a change, say all came, settle a
 * query, pay a bill (§5.5). */
function CardActions({ card, onDone }: { card: DocCard; onDone: () => void }) {
  const { can } = useAuth();
  const act = useAction();
  const { confirm, node: confirmNode } = useConfirm();
  const id = encodeURIComponent(card.id ?? card.reference);
  const go = (path: string, done: string) =>
    void act.run(async () => (await post(path), onDone()), done);
  const who = <span className="sr-only"> {card.reference}</span>;
  let buttons: ReactNode = null;
  switch (`${card.doc}:${card.event}`) {
    case "order:ordered":
      if (can("storefront.manage"))
        buttons = (
          <>
            <button
              type="button"
              className="small primary"
              disabled={act.busy}
              onClick={() =>
                go(
                  `/v1/trade-orders/${id}/accept`,
                  `${card.reference} accepted`,
                )
              }
            >
              Accept{who}
            </button>
            <button
              type="button"
              className="small"
              disabled={act.busy}
              onClick={async () =>
                (await confirm({
                  title: `Decline ${card.reference}?`,
                  ok: "Decline",
                  danger: true,
                })) &&
                go(
                  `/v1/trade-orders/${id}/decline`,
                  `${card.reference} declined`,
                )
              }
            >
              Decline{who}
            </button>
          </>
        );
      break;
    case "purchase:change":
      if (can("storefront.manage"))
        buttons = (
          <button
            type="button"
            className="small primary"
            disabled={act.busy}
            onClick={() =>
              go(`/v1/stock/purchases/${id}/accept-change`, "Change accepted")
            }
          >
            Accept the change{who}
          </button>
        );
      break;
    case "purchase:delivered":
      if (can("table.edit"))
        buttons = (
          <button
            type="button"
            className="small primary"
            disabled={act.busy}
            onClick={() =>
              go(
                `/v1/stock/purchases/${id}/all-came`,
                "Received, and your stock is up",
              )
            }
          >
            All came{who}
          </button>
        );
      break;
    case "invoice:queried":
      if (can("invoice.manage"))
        buttons = (
          <Link className="button small primary" to={cardHref(card)}>
            Settle the query{who}
          </Link>
        );
      break;
    case "bill:bill":
      if (can("invoice.manage"))
        buttons = (
          <Link className="button small primary" to={cardHref(card)}>
            Pay{who}
          </Link>
        );
      break;
  }
  if (!buttons) return null;
  return (
    <span
      className="row-actions"
      style={{ marginTop: 4 }}
      data-testid="card-actions"
    >
      {buttons}
      {confirmNode}
      {!!act.error && <ErrorBox error={act.error} />}
    </span>
  );
}

/** A message's photo, voice note or PDF: this business's own copy, from
 * the API (the console shows files from its own origin only). */
function AttachmentView({
  m,
  a,
  other,
}: {
  m: LinkMessage;
  a: Attachment;
  other: string;
}) {
  if (m.local) {
    // Still being sent: this device's own copy (a preview, or the name).
    if (a.kind === "photo" && m.localPreview)
      return (
        <span className="chat-photo">
          <img src={m.localPreview} alt="Your photo" />
        </span>
      );
    return (
      <span className="chat-file">
        <Icon name="file" />
        <span className="name">{attachmentLabel(a)}</span>
        <span className="hint">{fileSize(a.size)}</span>
      </span>
    );
  }
  const src = fileURL(m);
  if (a.kind === "photo")
    return (
      <a href={src} target="_blank" rel="noopener" className="chat-photo">
        <img
          src={src}
          alt={m.mine ? "Your photo" : `Photo from ${other}`}
          loading="lazy"
        />
      </a>
    );
  if (a.kind === "voice")
    return (
      <audio
        className="chat-voice"
        controls
        preload="none"
        src={src}
        aria-label={m.mine ? "Your voice note" : `Voice note from ${other}`}
      />
    );
  return (
    <a href={src} download={a.name || true} className="chat-file">
      <Icon name="file" />
      <span className="name">{attachmentLabel(a)}</span>
      <span className="hint">{fileSize(a.size)}</span>
    </a>
  );
}

/** The photos, files and voice notes in the conversation (those loaded),
 * newest first: WhatsApp's "Media, links and docs". */
function MediaAndFiles({
  messages,
  other,
}: {
  messages: LinkMessage[];
  other: string;
}) {
  const [open, setOpen] = useState(false);
  const kept = messages.filter((m) => m.attachment).reverse();
  if (kept.length === 0) return null;
  // Closed: a strip of the latest pictures; open: every picture, voice note and file.
  const strip = kept.filter((m) => m.attachment?.kind === "photo").slice(0, 4);
  return (
    <section className="info-media" aria-label="Media and files">
      <button
        type="button"
        className="info-row"
        aria-expanded={open}
        onClick={() => setOpen(!open)}
        data-testid="info-media"
      >
        <Icon name="image" />
        <span className="info-row-main">
          <span className="info-row-label">Media and files</span>
        </span>
        <span className="info-count">{kept.length}</span>
        <Icon name="chevron" className={open ? "turned" : undefined} />
      </button>
      {!open && strip.length > 0 && (
        <ul className="info-thumbs">
          {strip.map((m) => (
            <li key={m.id}>
              {m.attachment && (
                <AttachmentView m={m} a={m.attachment} other={other} />
              )}
            </li>
          ))}
        </ul>
      )}
      {open && (
        <ul className="info-media-all">
          {kept.map((m) => (
            <li key={m.id}>
              {m.attachment && (
                <AttachmentView m={m} a={m.attachment} other={other} />
              )}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/** Forward a message to other linked businesses (up to five), marked
 * Forwarded; a file goes as a copy into each one's own storage. */
function ForwardSheet({
  from,
  m,
  to,
  onClose,
  onSent,
}: {
  from: Conversation;
  m: LinkMessage;
  to: Conversation[];
  onClose: () => void;
  onSent: () => void;
}) {
  const [picked, setPicked] = useState<string[]>([]);
  const act = useAction();
  const toggle = (link: string) =>
    setPicked((cur) =>
      cur.includes(link)
        ? cur.filter((x) => x !== link)
        : cur.length < 5
          ? [...cur, link]
          : cur,
    );
  const send = () =>
    void act
      .run(
        async () => {
          await post(`/v1/link-chats/${from.link}/forward`, {
            message: m.id,
            to: picked,
          });
          onSent();
        },
        `Forwarded to ${picked.length === 1 ? (to.find((c) => c.link === picked[0])?.name ?? "1 chat") : `${picked.length} chats`}`,
      )
      .then((ok) => ok !== false && onClose());
  return (
    <Modal title="Forward to" onClose={onClose}>
      <p className="hint">“{previewLine(m, from.name)}”</p>
      <fieldset className="choice">
        <legend>Linked businesses (up to five)</legend>
        {to.map((c) => (
          <label key={c.link} className="inline">
            <input
              type="checkbox"
              checked={picked.includes(c.link)}
              onChange={() => toggle(c.link)}
            />
            {c.name}
          </label>
        ))}
      </fieldset>
      <ErrorBox error={act.error} />
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={act.busy || picked.length === 0}
          onClick={send}
        >
          Forward
        </button>
        <button type="button" onClick={onClose}>
          Cancel
        </button>
      </div>
    </Modal>
  );
}

/** When a message was sent and read, and who reacted (WhatsApp's "Message
 * info"): the times the two businesses' copies carry. */
function InfoSheet({
  m,
  other,
  onClose,
}: {
  m: LinkMessage;
  other: string;
  onClose: () => void;
}) {
  return (
    <Modal title="Message info" onClose={onClose}>
      <p className="hint" data-testid="info-what">
        “
        {m.kind === "card" && m.card
          ? cardLine(m.card, m.mine, other)
          : previewLine(m, other)}
        ”
      </p>
      <dl className="info-times" data-testid="chat-info-times">
        <dt>{m.mine ? "Sent" : "Received"}</dt>
        <dd>{fmtTime(m.created_at)}</dd>
        {m.mine ? (
          <>
            <dt>Read by {other}</dt>
            <dd>{m.read_at ? fmtTime(m.read_at) : "Not yet"}</dd>
          </>
        ) : (
          <>
            <dt>Read by you</dt>
            <dd>{m.read_at ? fmtTime(m.read_at) : "Not yet"}</dd>
          </>
        )}
      </dl>
      {m.reactions && m.reactions.length > 0 && (
        <>
          <h3 style={{ marginBottom: 4 }}>Reactions</h3>
          <ul className="info-reactions">
            {m.reactions.map((r) => (
              <li key={r.emoji}>{reactionWords(r)}</li>
            ))}
          </ul>
        </>
      )}
      <div className="row-actions">
        <button type="button" onClick={onClose}>
          Close
        </button>
      </div>
    </Modal>
  );
}

/** A link's preview in its bubble: the page's host, title and words. */
function PreviewCard({ p }: { p: LinkPreview }) {
  return (
    <a
      className="chat-preview"
      href={p.url}
      target="_blank"
      rel="noopener noreferrer"
      data-testid="chat-preview"
    >
      <span className="host">{p.host}</span>
      <span className="title">{p.title}</span>
      {p.description && <span className="desc">{p.description}</span>}
    </a>
  );
}

function Bubble({
  m,
  other,
  onReply,
  canReply,
  latest = false,
  onDone = () => undefined,
  onForward,
  onHide,
  onJump,
  flash = false,
  first = true,
  onReact,
  onStar,
  onInfo,
  onRetry,
  onDiscard,
  arrive = false,
}: {
  m: LinkMessage;
  other: string;
  onReply: (m: LinkMessage) => void;
  canReply: boolean;
  latest?: boolean;
  onDone?: () => void;
  onForward?: (m: LinkMessage) => void;
  onHide?: (m: LinkMessage) => void;
  /** Goes to the message a quote quotes. */
  onJump?: (id: string) => void;
  /** Shown for a moment after a jump lands on it. */
  flash?: boolean;
  /** Starts a run of one sender's messages: it has the tail and the name. */
  first?: boolean;
  /** React to it with an emoji (the same one again takes the reaction back). */
  onReact?: (m: LinkMessage, emoji: string) => void;
  /** Star it, or take the star off. */
  onStar?: (m: LinkMessage) => void;
  /** Show when it was sent and read. */
  onInfo?: (m: LinkMessage) => void;
  /** Not sent: tries again, or gives up on the message. */
  onRetry?: (clientId: string) => void;
  onDiscard?: (clientId: string) => void;
  /** A card that just arrived: it slides in with a little bounce. */
  arrive?: boolean;
}) {
  const card = m.kind === "card" ? m.card : undefined;
  const fold = useFold(m.body ?? "");
  const local = m.local;
  const hasMenu =
    !local &&
    (!!(onReact || onStar || onInfo) ||
      canReply ||
      (!card && !!(onForward || onHide || m.body)));
  const mineEmoji = m.reactions?.find((r) => r.mine)?.emoji;
  const what = card ? cardLine(card, m.mine, other) : previewLine(m, other);
  // The time and ticks sit in the corner of the last line of text (the text
  // keeps room for them); with a photo only, a link preview or a card they
  // have a row of their own.
  const inline = !card && !!m.body && !m.preview && local !== "failed";
  return (
    <BubbleShell
      id={m.id}
      mine={m.mine}
      first={first}
      testid={card ? "chat-card" : "chat-message"}
      className={`${card ? "card-bubble" : ""}${arrive ? " tk-arrive" : ""}${flash ? " flash" : ""}${m.reactions?.length ? " has-reactions" : ""}${local === "failed" ? " failed" : ""}`.trim()}
      what={what}
      onReply={canReply ? () => onReply(m) : undefined}
      quickReact={!!onReact}
      menu={
        hasMenu
          ? (close, more) => ({
              onPick: onReact
                ? (e: string) => (close(), onReact(m, e))
                : undefined,
              reactions: onReact ? (
                <span
                  className="reaction-picker"
                  role="group"
                  aria-label="React"
                >
                  {REACTIONS.map((e) => (
                    <button
                      key={e}
                      type="button"
                      className="react-pick"
                      aria-pressed={mineEmoji === e}
                      aria-label={`React with ${REACTION_NAMES[e]}`}
                      title={REACTION_NAMES[e]}
                      onClick={() => (close(), onReact(m, e))}
                    >
                      {e}
                    </button>
                  ))}
                  <button
                    type="button"
                    className="react-pick react-more"
                    aria-label="More reactions"
                    title="More reactions"
                    onClick={more.pick}
                  >
                    <Icon name="plus" />
                  </button>
                </span>
              ) : undefined,
              actions: (
                <>
                  {onInfo && (
                    <PopItem icon="info" onClick={() => (close(), onInfo(m))}>
                      Message info
                    </PopItem>
                  )}
                  {canReply && (
                    <PopItem icon="reply" onClick={() => (close(), onReply(m))}>
                      Reply
                    </PopItem>
                  )}
                  {!card && m.body && <CopyItem text={m.body} />}
                  {!card && onForward && (
                    <PopItem
                      icon="forward"
                      onClick={() => (close(), onForward(m))}
                    >
                      Forward
                    </PopItem>
                  )}
                  {onStar && (
                    <PopItem icon="star" onClick={() => (close(), onStar(m))}>
                      {m.starred ? "Unstar" : "Star"}
                    </PopItem>
                  )}
                  {!card && onHide && (
                    <>
                      <div className="pop-sep" role="separator" />
                      <PopItem
                        icon="trash"
                        danger
                        onClick={() => (close(), onHide(m))}
                      >
                        Delete for me
                      </PopItem>
                    </>
                  )}
                </>
              ),
            })
          : undefined
      }
      after={
        <>
          {local === "failed" && (
            <span
              className="chat-failed"
              role="alert"
              data-testid="chat-failed"
              title={`Not sent. ${m.localError ?? ""}`}
            >
              <span className="sr-only">Not sent. {m.localError}</span>
              <button
                type="button"
                className="fail-act"
                aria-label="Retry"
                title="Try again"
                onClick={() => onRetry?.(m.id.replace("local-", ""))}
              >
                <Icon name="retry" />
              </button>
              <button
                type="button"
                className="fail-act"
                aria-label="Delete"
                title="Delete"
                onClick={() => onDiscard?.(m.id.replace("local-", ""))}
              >
                <Icon name="trash" />
              </button>
            </span>
          )}
          <span className={`stamp${inline ? " inline" : ""}`}>
            {m.starred && (
              <span className="starred" role="img" aria-label="Starred">
                <Icon name="star" />
              </span>
            )}
            <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
              {clock(m.created_at)}
            </time>
            {/* Our message is written in the other business's conversation in
            the same transaction it is sent, so it is delivered as soon as
            it is here: two ticks, coloured once they have read it. */}
            {local === "sending" && (
              <span className="ticks pending" role="img" aria-label="Sending">
                <Icon name="clock" />
              </span>
            )}
            {local === "failed" && (
              <span className="ticks failed" role="img" aria-label="Not sent">
                <span aria-hidden="true">!</span>
              </span>
            )}
            {m.mine && !card && !local && <Ticks read={!!m.read_at} />}
          </span>
          {m.reactions && m.reactions.length > 0 && !local && (
            <span className="reactions" role="group" aria-label="Reactions">
              {m.reactions.map((r) => (
                <button
                  key={r.emoji}
                  type="button"
                  className="reaction-chip"
                  aria-pressed={!!r.mine}
                  aria-label={`${reactionWords(r)}. ${r.mine ? "Tap to take yours back." : "Tap to react the same."}`}
                  title={reactionWords(r)}
                  disabled={!onReact}
                  onClick={() => onReact?.(m, r.emoji)}
                >
                  <span aria-hidden="true">{r.emoji}</span>
                  {r.count > 1 && (
                    <span className="count" aria-hidden="true">
                      {r.count}
                    </span>
                  )}
                </button>
              ))}
            </span>
          )}{" "}
        </>
      }
    >
      {first && !m.mine && !card && m.sender_name && (
        <span className="who">{m.sender_name}</span>
      )}
      {m.forwarded && (
        <span className="forwarded" data-testid="chat-forwarded">
          Forwarded
        </span>
      )}
      {m.reply && onJump && (
        <button
          type="button"
          className="quote quote-btn"
          data-testid="chat-quote"
          title="Go to the message"
          onClick={() => onJump(m.reply?.pair_id ?? "")}
        >
          <span className="quote-who">{m.reply.mine ? "You" : other}</span>
          <span className="quote-text">
            {previewLine({ ...m.reply, mine: m.reply.mine }, other)}
          </span>
        </button>
      )}
      {m.reply && !onJump && (
        <span className="quote" data-testid="chat-quote">
          <span className="quote-who">{m.reply.mine ? "You" : other}</span>
          <span className="quote-text">
            {previewLine({ ...m.reply, mine: m.reply.mine }, other)}
          </span>
        </span>
      )}
      {card ? (
        <CardFrame card={card} title={cardLine(card, m.mine, other)}>
          <Link to={cardHref(card)} className="button small">
            {card.doc === "order" || card.doc === "purchase"
              ? "View order"
              : "View invoice"}
            <span className="sr-only"> {card.reference}</span>
          </Link>
          {latest && !m.mine && <CardActions card={card} onDone={onDone} />}
        </CardFrame>
      ) : (
        <>
          {m.attachment && (
            <AttachmentView m={m} a={m.attachment} other={other} />
          )}
          {m.body && (
            <span className="text">
              <MentionText text={fold.shown} mentions={m.mentions} />
              {fold.folded && <ReadMore onClick={fold.more} />}
              {inline && (
                <span
                  className={`stamp-gap${m.mine ? " mine" : ""}`}
                  aria-hidden="true"
                />
              )}
            </span>
          )}
          {m.preview && <PreviewCard p={m.preview} />}
        </>
      )}
    </BubbleShell>
  );
}

/** The card frame (chat-tools.md §3): the document's kind and reference,
 * its state as a pill, what it is, an order's progress, and its buttons. */
function CardFrame({
  card,
  title,
  children,
}: {
  card: DocCard;
  title: string;
  children: ReactNode;
}) {
  const st = cardStatus(card);
  return (
    <article className="tk-chatcard" aria-label={cardKicker(card)}>
      <span className="tk-chatcard-top">
        <span className="tk-kicker">{cardKicker(card)}</span>
        <span className={`tk-pill ${st.tone}`}>{st.word}</span>
      </span>
      <span className="card-title">{title}</span>
      {cardSub(card) && (
        <span className="tk-chatcard-sub">{cardSub(card)}</span>
      )}
      {st.step !== undefined && (
        <span className="tk-progress">
          <span
            className="tk-progress-bar"
            role="img"
            aria-label={`${ORDER_STEPS[st.step]}: step ${st.step + 1} of ${ORDER_STEPS.length}`}
          >
            {ORDER_STEPS.map((w, i) => (
              <span key={w} className={i <= (st.step ?? -1) ? "done" : ""} />
            ))}
          </span>
          <span className="tk-progress-words" aria-hidden="true">
            {ORDER_STEPS.map((w, i) => (
              <span key={w} className={i === st.step ? "now" : ""}>
                {w}
              </span>
            ))}
          </span>
        </span>
      )}
      <span className="tk-chatcard-actions">{children}</span>
    </article>
  );
}

/** Diary lines (chat-tools.md §3): changes we made ourselves, told as
 * lines; several made close together are one line, the newest, under
 * which the earlier ones unfold. */
function DiaryRun({
  run,
  other,
  fresh,
}: {
  run: LinkMessage[];
  other: string;
  /** Arrived while the conversation is open: it lands, lit in lime. */
  fresh: (m: LinkMessage) => boolean;
}) {
  const [open, setOpen] = useState(false);
  // The newest change shows; the ones before it fold under it.
  const lead = run[run.length - 1];
  if (!lead) return null;
  const rest = run.slice(0, -1);
  const line = (m: LinkMessage, more?: ReactNode) =>
    (m.card || m.kind === "diary") && (
      <DiaryLine
        key={m.id}
        msg={m.id}
        to={m.card ? cardHref(m.card) : undefined}
        fresh={fresh(m)}
        more={more}
        when={
          <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
            You, {clock(m.created_at)}
          </time>
        }
      >
        {m.card ? cardLine(m.card, true, other) : m.body}
        {m.card && cardSub(m.card) && (
          <span className="tk-diary-sub"> {cardSub(m.card)}</span>
        )}
      </DiaryLine>
    );
  return (
    <>
      {open && rest.map((m) => line(m))}
      {line(
        lead,
        rest.length > 0 && (
          <button
            type="button"
            className="tk-diary-more"
            aria-expanded={open}
            onClick={() => setOpen(!open)}
          >
            {open
              ? "Fold"
              : `and ${rest.length} earlier ${rest.length === 1 ? "change" : "changes"}`}
          </button>
        ),
      )}
    </>
  );
}

/** Diary lines made within this long of each other are one line. */
const DIARY_RUN_MS = 5 * 60_000;

/** One conversation: its stream, following it while open, and the
 * composer. */
export function ConversationPane({
  conv,
  onBack,
  onChanged,
  others = [],
  roles = {},
  link,
  onEndLink,
  focus,
}: {
  conv: Conversation;
  onBack: () => void;
  onChanged: () => void;
  /** How we trade with each linked business, by link (the picker's lines). */
  roles?: Record<string, LinkRole>;
  /** The link with this business, for the contact panel. */
  link?: BusinessLink;
  /** Ends the link (only for a member who may manage links). */
  onEndLink?: (l: BusinessLink) => void;
  /** A message to open the conversation at (a search result). */
  focus?: string;
  /** The other conversations, to forward to. */
  others?: Conversation[];
}) {
  const { can } = useAuth();
  const write = can("chat.trade") && conv.state === "active";
  const [messages, setMessages] = useState<LinkMessage[]>();
  const [typing, setTyping] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
  const logo = conv.logo ? logoSrc(conv.business) : undefined;
  // The business's card (area, what it sells), as its public page shows
  // it: found by the same search, so only a business that lets others find
  // it has one.
  const [card, setCard] = useState<LinkCard>();
  useEffect(() => {
    if (!infoOpen || card) return;
    let live = true;
    get<{ results: LinkCard[] }>(
      `/v1/links/search?q=${encodeURIComponent(conv.name)}`,
    ).then(
      (r) =>
        live && setCard(r.results.find((c) => c.business === conv.business)),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [infoOpen, card, conv.name, conv.business]);
  useEffect(() => {
    if (!infoOpen) return;
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setInfoOpen(false);
    document.addEventListener("keydown", esc);
    return () => document.removeEventListener("keydown", esc);
  }, [infoOpen]);
  const [forwarding, setForwarding] = useState<LinkMessage>();
  const typedAt = useRef(0);
  const [error, setError] = useState<unknown>();
  const chatTools = useChatTools();
  // A diary line kept: what is newer is pulled in (set once pull exists).
  const afterDiary = useRef<() => void>(() => undefined);
  const items = useLoad(
    () =>
      get<{ items: PriceItem[] }>(`/v1/link-chats/${conv.link}/items`).then(
        (r) => r.items,
        () => [] as PriceItem[],
      ),
    [conv.link],
  );
  const c = useComposer<LinkMessage>({
    draftKey: conv.link,
    rules: BUSINESS_FILES,
    enabled: write,
    sources: {
      // With chat tools, the business we are talking to is not offered:
      // it gets the message already.
      "@": others
        .filter(
          (o) =>
            o.state === "active" &&
            !(chatTools && (o.link === conv.link || !can("chat.trade"))),
        )
        .map((o) => ({
          kind: "business" as const,
          id: o.link,
          name: o.name,
          sub: chatTools ? linkedLine(roles[o.link]) : undefined,
        })),
      "#": (items.data ?? [])
        .filter((it) => it.available)
        .map((it) => ({
          kind: "item" as const,
          id: it.item_id,
          name: it.name,
          sub: priceWords(it),
        })),
    },
    replyLine: (r) => previewLine(r, conv.name),
    closes: [infoOpen, !!forwarding],
    grouped: chatTools ? { taskiem: true } : undefined,
    tools: chatTools,
    diaryWhere: conv.link,
    onDiary: () => afterDiary.current(),
    // We are typing: the other side's typing line, told at most every 3 s.
    onType: (v) => {
      // "/pro" opens the tools and "@Taskiem" asks Taskiem: neither reaches
      // them, so they are not told we are typing.
      if (chatTools && (/^\//.test(v) || /(^|[\s,(])@taskiem\b/i.test(v)))
        return;
      const now = Date.now();
      if (v.trim() && now - typedAt.current > 3000) {
        typedAt.current = now;
        post(`${path}/typing`).catch(() => undefined);
      }
    },
    onSend: (s) => sendMessage(s),
  });
  const { setReply } = c;
  const [searchOpen, setSearchOpen] = useState(false);
  const searchButton = useRef<HTMLButtonElement>(null);
  const act = useAction();
  const path = `/v1/link-chats/${conv.link}`;
  // The thread's scrolling, as WhatsApp Web's: older messages load as the
  // reader nears the top; the view follows new messages only while the
  // reader is at the bottom; "N unread messages" marks where they begin;
  // a quote, or a search result, jumps to its message.
  const loadingOlder = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const [hasOlder, setHasOlder] = useState(false);
  const [detached, setDetached] = useState(false); // not at the newest page
  const [unread, setUnread] = useState<{ id: string; count: number }>();
  // Messages being sent, shown at the end of the thread until the server has
  // them (lib/outbox.ts).
  const outbox = useOutbox(conv.link);
  const outgoing = useMemo(
    () => outbox.map((o) => asMessage(o, conv.link)),
    [outbox, conv.link],
  );
  const threadItems = useMemo(
    () => (messages ? [...messages, ...(detached ? [] : outgoing)] : undefined),
    [messages, outgoing, detached],
  );
  const thread = useThread({
    items: threadItems,
    isMine: (m) => m.mine,
    hasOlder,
    loadOlder: olderCb,
  });
  const { stream, flashId } = thread;
  const last = messages?.[messages.length - 1]?.created_at;

  // What is newer, every few seconds (while the newest page is the one
  // shown); read while seen.
  const markRead = useCallback(
    async (list: LinkMessage[]) => {
      if (
        list.some((m) => !m.mine && !m.read_at) &&
        document.visibilityState === "visible"
      )
        await post(`${path}/read`).then(onChanged, () => undefined);
    },
    [path, onChanged],
  );
  const pull = useCallback(
    async (after?: string) => {
      const out = await get<{ messages: LinkMessage[]; typing?: boolean }>(
        `${path}/messages${after ? `?after=${encodeURIComponent(after)}` : ""}`,
      );
      setTyping(!!out.typing);
      if (after && out.messages.length === 0) return;
      setMessages((cur) => merge(cur ?? [], out.messages));
      await markRead(out.messages);
      // A full page: there may be more after it (the reader was away).
      const newest = out.messages[out.messages.length - 1];
      if (after && out.messages.length >= PAGE && newest)
        await pull(newest.created_at);
    },
    [path, markRead],
  );
  afterDiary.current = () => void pull(last).catch(setError);

  // Open at the newest page (the first unread is marked), or, from a
  // search result or a quote, at that message.
  const openNewest = useCallback(async () => {
    const out = await get<{ messages: LinkMessage[]; typing?: boolean }>(
      `${path}/messages`,
    );
    setTyping(!!out.typing);
    thread.reset();
    setDetached(false);
    setHasOlder(out.messages.length >= PAGE);
    setUnread(firstUnread(out.messages));
    setMessages(out.messages);
    await markRead(out.messages);
  }, [path, markRead]);
  const openAt = useCallback(
    async (id: string) => {
      const out = await get<{ messages: LinkMessage[] }>(
        `${path}/messages?around=${encodeURIComponent(id)}&limit=${PAGE}`,
      );
      const at = out.messages.findIndex((m) => m.id === id);
      thread.reset(id);
      // Newer messages beyond this window: the view is detached from the
      // newest page until the reader jumps to the latest.
      // (A full half of newer ones, or of older ones, means there may be more.)
      const half = Math.floor(PAGE / 2);
      setDetached(at >= 0 && out.messages.length - at - 1 >= PAGE - half);
      setHasOlder(at >= half);
      setUnread(undefined);
      setMessages(out.messages);
    },
    [path],
  );
  useEffect(() => {
    setMessages(undefined);
    setReply(undefined);
    if (focus) openAt(focus).catch(() => openNewest().catch(setError));
    else openNewest().catch(setError);
  }, [openNewest, openAt, focus]);
  const loadOlder = useCallback(async () => {
    const first = messages?.[0];
    const el = stream.current;
    if (!first || !el || loadingOlder.current) return;
    loadingOlder.current = true;
    try {
      const out = await get<{ messages: LinkMessage[] }>(
        `${path}/messages?before=${encodeURIComponent(first.created_at)}&limit=${PAGE}`,
      );
      if (out.messages.length < PAGE) setHasOlder(false);
      if (out.messages.length > 0) {
        thread.keepPlace();
        setMessages((cur) => merge(cur ?? [], out.messages));
      }
    } catch (e) {
      setError(e);
    } finally {
      loadingOlder.current = false;
    }
  }, [messages, path]);
  loadOlderRef.current = () => void loadOlder();
  const toLatest = () => {
    setError(undefined);
    openNewest().catch(setError);
  };
  const jump = (id: string) => {
    if (!thread.show(id))
      openAt(id).catch(() =>
        setError(new Error("That message is no longer here.")),
      );
  };
  // A search result: the thread goes to the message (on a phone the panel
  // closes to show it; on a computer it stays beside the thread).
  const onPhone = () => window.matchMedia?.("(max-width: 899px)").matches;
  const closeSearch = useCallback(() => {
    setSearchOpen(false);
    requestAnimationFrame(() => searchButton.current?.focus());
  }, []);
  const jumpFound = (id: string) => {
    jump(id);
    if (onPhone()) closeSearch();
  };
  // A day: the first message of it, or after it.
  const jumpToDay = async (day: string) => {
    const start = new Date(`${day}T00:00:00`);
    if (Number.isNaN(start.getTime())) return false;
    const before = new Date(start.getTime() - 1).toISOString();
    const out = await get<{ messages: LinkMessage[] }>(
      `${path}/messages?after=${encodeURIComponent(before)}&limit=1`,
    );
    const first = out.messages[0];
    if (!first) return false;
    jumpFound(first.id);
    return true;
  };
  // The live stream brings what changes as it happens; while it is open the
  // polling only reconciles (and says so by being slow), and without it the
  // page polls as it did.
  const live = useChatLive();
  const connected = live.connected;
  const readSoon = useRef<number>(undefined);
  useEffect(() => () => window.clearTimeout(readSoon.current), []);
  const detachedNow = useRef(detached);
  detachedNow.current = detached;
  useEffect(
    () =>
      live.subscribe((e) => {
        if (e.type === "message" && e.message.link === conv.link) {
          const m = e.message;
          // Not into a window away from the newest page: it would leave a gap.
          if (!detachedNow.current)
            setMessages((cur) => (cur ? merge(cur, [m]) : cur));
          // Read (once for a burst of them: a conversation of hundreds arriving
          // together asks once, not hundreds of times).
          if (
            !m.mine &&
            !m.read_at &&
            document.visibilityState === "visible" &&
            document.hasFocus()
          ) {
            window.clearTimeout(readSoon.current);
            readSoon.current = window.setTimeout(
              () => post(`${path}/read`).then(onChanged, () => undefined),
              400,
            );
          }
        } else if (e.type === "gone" && e.link === conv.link)
          setMessages((cur) => cur?.filter((x) => x.id !== e.id));
      }),
    [live, conv.link, path, onChanged],
  );
  useEffect(() => {
    const t = setInterval(
      () => {
        if (document.visibilityState === "visible" && !detached)
          pull(last).catch(() => undefined);
      },
      connected ? EVERY_MS * 5 : EVERY_MS,
    );
    return () => clearInterval(t);
  }, [pull, last, detached, connected]);
  // Our own ticks follow theirs reading: the page's read marks change in place.
  useEffect(() => {
    const t = setInterval(
      () => {
        // The newest page again: our ticks, and the other side's reactions.
        if (document.visibilityState !== "visible" || detached) return;
        get<{ messages: LinkMessage[] }>(`${path}/messages`).then(
          (out) => setMessages((cur) => merge(cur ?? [], out.messages)),
          () => undefined,
        );
      },
      connected ? EVERY_MS * 10 : EVERY_MS * 3,
    );
    return () => clearInterval(t);
  }, [messages, path, detached, connected]);
  // Sent at once: it shows in the thread with a clock, the box is cleared,
  // and the server's copy takes its place; if it cannot be sent it stays,
  // with Retry and Delete. The client id makes a retry the same message.
  // What the server accepts of ours (from here, or from a conversation we
  // have since left) joins this thread only if it belongs to it.
  useEffect(
    () =>
      onSent((link, m) => {
        if (link === conv.link) setMessages((cur) => merge(cur ?? [], [m]));
        onChanged();
      }),
    [conv.link, onChanged],
  );
  const sendMessage = (s: Sending<LinkMessage>) => {
    const { file, reply } = s;
    const o: Outgoing = {
      clientId: newClientId(),
      body: s.text,
      replyTo: reply?.id,
      quote: reply
        ? {
            pair_id: reply.id,
            mine: reply.mine,
            kind: reply.kind,
            body: reply.body,
            card: reply.card,
            attachment: reply.attachment,
          }
        : undefined,
      file,
      mentions: s.mentions,
      state: "sending",
      at: new Date().toISOString(),
    };
    sendOutgoing(conv.link, o);
    if (detached) void openNewest();
    if (file?.type.startsWith("image/")) {
      const r = new FileReader();
      const link = conv.link;
      r.onload = () =>
        updateOutgoing(link, o.clientId, { preview: String(r.result) });
      r.readAsDataURL(file);
    }
  };
  const retry = (clientId: string) => retryOutgoing(conv.link, clientId);
  const discard = (clientId: string) => discardOutgoing(conv.link, clientId);
  // A reaction: shown at once, then what the server says (both sides see it).
  const setReactions = (id: string, reactions: Reaction[]) =>
    setMessages((cur) =>
      cur?.map((x) => (x.id === id ? { ...x, reactions } : x)),
    );
  const react = (m: LinkMessage, emoji: string) => {
    const was = m.reactions;
    const taking = m.reactions?.some((r) => r.mine && r.emoji === emoji);
    setReactions(m.id, toggleReaction(was, emoji, !taking));
    const url = `${path}/messages/${m.id}/reaction`;
    (taking
      ? del<{ reactions: Reaction[] }>(url)
      : put<{ reactions: Reaction[] }>(url, { emoji })
    ).then(
      (out) => setReactions(m.id, out.reactions),
      (e: unknown) => {
        setReactions(m.id, was ?? []);
        setError(actionError(e));
      },
    );
  };
  const star = (m: LinkMessage) => {
    const on = !m.starred;
    const mark = (starred: boolean) =>
      setMessages((cur) =>
        cur?.map((x) => (x.id === m.id ? { ...x, starred } : x)),
      );
    mark(on);
    (on
      ? put(`${path}/messages/${m.id}/star`)
      : del(`${path}/messages/${m.id}/star`)
    ).then(
      () => undefined,
      (e: unknown) => {
        mark(!on);
        setError(actionError(e));
      },
    );
  };
  const [info, setInfo] = useState<LinkMessage>();
  const settings = (
    body: { muted?: boolean; archived?: boolean; pinned?: boolean },
    done: string,
  ) => void act.run(async () => (await put(path, body), onChanged()), done);
  const { confirm, node: confirmNode } = useConfirm();
  const hide = async (m: LinkMessage) => {
    if (
      !(await confirm({
        title: "Delete this message?",
        body: "It goes from your business's side. They keep theirs.",
        ok: "Delete for me",
        danger: true,
      }))
    )
      return;
    void act.run(async () => {
      await post(`${path}/messages/${m.id}/hide`);
      setMessages((cur) => cur?.filter((x) => x.id !== m.id));
      onChanged();
    }, "Deleted for you");
  };
  const shown = threadItems;
  // What arrives while the conversation is open lands with its motion;
  // what was there already, or loads from further back, just shows.
  const openedNewest = useRef<string | undefined>(undefined);
  if (shown && shown.length > 0 && openedNewest.current === undefined)
    openedNewest.current = shown[shown.length - 1]?.created_at ?? "";
  const fresh = (m: LinkMessage) =>
    openedNewest.current !== undefined && m.created_at > openedNewest.current;
  // Diary lines made close together on one day are one line (its first).
  const runs = useMemo(() => {
    const lead = new Map<string, LinkMessage[]>();
    const member = new Set<string>();
    let run: LinkMessage[] | undefined;
    for (const m of shown ?? []) {
      const diary = m.kind === "diary" || (chatTools && !!m.card && isDiary(m.card, m.mine));
      const last = run?.[run.length - 1];
      if (
        diary &&
        run &&
        last &&
        dayLabel(last.created_at) === dayLabel(m.created_at) &&
        Date.parse(m.created_at) - Date.parse(last.created_at) <= DIARY_RUN_MS
      ) {
        run.push(m);
        member.add(m.id);
      } else if (diary) {
        run = [m];
        lead.set(m.id, run);
      } else run = undefined;
    }
    return { lead, member };
  }, [shown, chatTools]);
  // A card's actions are offered on its document's newest card only.
  const newest = new Set<string>();
  const seen = new Set<string>();
  for (const m of [...(messages ?? [])].reverse()) {
    if (m.kind !== "card" || !m.card) continue;
    const key = `${m.card.doc}:${m.card.reference}`;
    if (seen.has(key)) continue;
    seen.add(key);
    newest.add(m.id);
  }
  return (
    <section
      className={`chat-pane${searchOpen ? " with-side" : ""}`}
      aria-label={`Conversation with ${conv.name}`}
      data-testid="chat-pane"
      onKeyDown={c.onPaneKeyDown}
      {...c.dropProps}
    >
      <ChatHead
        onBack={onBack}
        menuLabel="More about this conversation"
        avatar={
          <button
            type="button"
            className="chat-avatar-btn"
            aria-label={`About ${conv.name}`}
            aria-expanded={infoOpen}
            onClick={() => {
              setSearchOpen(false);
              setInfoOpen(!infoOpen);
            }}
          >
            <ChatAvatar name={conv.name} logo={logo} />
          </button>
        }
        title={conv.name}
        subtitle={
          (connected ? live.typing.has(conv.link) : typing) ? (
            <span className="typing" data-testid="chat-typing">
              typing…
            </span>
          ) : link ? (
            roleLine(link.role)
          ) : undefined
        }
        actions={
          <button
            type="button"
            className="icon-button"
            aria-label="Search this conversation"
            aria-expanded={searchOpen}
            ref={searchButton}
            data-testid="chat-search-open"
            onClick={() => {
              setInfoOpen(false);
              setSearchOpen(!searchOpen);
            }}
          >
            <Icon name="search" />
          </button>
        }
        items={(close) => (
          <>
            <button
              type="button"
              onClick={() => (close(), setSearchOpen(false), setInfoOpen(true))}
            >
              Contact info
            </button>
            {can("chat.trade") && (
              <>
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() => {
                    close();
                    settings(
                      { pinned: !conv.pinned },
                      conv.pinned ? "Unpinned" : "Pinned",
                    );
                  }}
                >
                  {conv.pinned ? "Unpin" : "Pin"}
                </button>
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() => {
                    close();
                    settings(
                      { muted: !conv.muted },
                      conv.muted ? "Unmuted" : "Muted",
                    );
                  }}
                >
                  {conv.muted ? "Unmute" : "Mute"}
                </button>
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() => {
                    close();
                    settings(
                      { archived: !conv.archived },
                      conv.archived ? "Unarchived" : "Archived",
                    );
                  }}
                >
                  {conv.archived ? "Unarchive" : "Archive"}
                </button>
              </>
            )}
          </>
        )}
      />
      <ErrorBox error={error ?? act.error ?? c.fileError} />
      <ChatStream
        items={shown}
        thread={thread}
        hasOlder={hasOlder}
        prefix="chat"
        keyOf={(m) => m.id}
        startsRun={startsRun}
        unread={unread}
        empty={`No messages yet. Say hello to ${conv.name}.`}
        render={(m, { first }) =>
          runs.member.has(m.id) ? null : runs.lead.has(m.id) ? (
            <DiaryRun
              run={runs.lead.get(m.id) ?? [m]}
              other={conv.name}
              fresh={fresh}
            />
          ) : (
            <Bubble
              m={m}
              other={conv.name}
              canReply={write && !m.local}
              onRetry={retry}
              onDiscard={discard}
              onReply={setReply}
              onJump={jump}
              first={first}
              flash={flashId === m.id}
              latest={newest.has(m.id)}
              onDone={() => pull(last).catch(setError)}
              onForward={
                write && others.some((o) => o.state === "active")
                  ? setForwarding
                  : undefined
              }
              onHide={can("chat.trade") ? hide : undefined}
              onReact={write && !m.local ? react : undefined}
              onStar={m.local ? undefined : star}
              onInfo={m.local ? undefined : setInfo}
              arrive={!!m.card && fresh(m)}
            />
          )
        }
        overlay={
          thread.away || detached ? (
            <ThreadJump
              detached={detached}
              count={thread.newCount}
              onClick={() => (detached ? toLatest() : thread.toBottom())}
            />
          ) : undefined
        }
      />
      {write ? (
        <ComposerView
          c={c}
          busy={act.busy}
          prefix="chat"
          textId="chat-text"
          textLabel="Message"
        />
      ) : (
        <p className="hint" style={{ padding: "10px 14px" }}>
          {conv.state === "active"
            ? "You can read this conversation. Someone who writes to linked businesses for your business can reply."
            : "This link has ended. The conversation stays."}
        </p>
      )}
      {info && (
        <InfoSheet
          m={info}
          other={conv.name}
          onClose={() => setInfo(undefined)}
        />
      )}
      {forwarding && (
        <ForwardSheet
          from={conv}
          m={forwarding}
          to={others.filter(
            (o) => o.state === "active" && o.link !== conv.link,
          )}
          onClose={() => setForwarding(undefined)}
          onSent={onChanged}
        />
      )}
      {searchOpen && (
        <ChatSearchPanel
          name={conv.name}
          path={path}
          onClose={closeSearch}
          onJump={jumpFound}
          onDate={jumpToDay}
        />
      )}
      {infoOpen && (
        <ContactInfo
          conv={conv}
          logo={logo}
          link={link}
          card={card}
          canManage={can("chat.trade")}
          busy={act.busy}
          media={<MediaAndFiles messages={messages ?? []} other={conv.name} />}
          onClose={() => setInfoOpen(false)}
          onSetting={settings}
          onEndLink={
            link && link.state === "active" && onEndLink
              ? async () => {
                  if (
                    await confirm({
                      title: `End the link with ${link.name}?`,
                      body: "What passed between you stays on both sides.",
                      ok: "End link",
                      danger: true,
                    })
                  )
                    onEndLink(link);
                }
              : undefined
          }
          onSearch={() => {
            setInfoOpen(false);
            setSearchOpen(true);
          }}
        />
      )}
      {confirmNode}
    </section>
  );
}

/** A document's thread on its page: its cards in the conversation with
 * the other business, and the replies about it, with a reply box (§8).
 * Nothing while the document has no card. */
export function DocThread({
  doc,
  reference,
}: {
  doc: "order" | "purchase" | "invoice" | "bill";
  reference: string;
}) {
  const { can } = useAuth();
  const thread = useLoad(
    () =>
      get<{ messages: LinkMessage[] }>(
        `/v1/link-chats/thread?doc=${doc}&reference=${encodeURIComponent(reference)}`,
      ),
    [doc, reference],
  );
  // Cards land moments after what made them: the thread follows itself.
  const reloadThread = thread.reload;
  useEffect(() => {
    const t = setInterval(
      () => document.visibilityState === "visible" && reloadThread(),
      EVERY_MS * 2,
    );
    return () => clearInterval(t);
  }, [reloadThread]);
  const [text, setText] = useState("");
  const act = useAction();
  const list = thread.data?.messages ?? [];
  const cards = list.filter((m) => m.kind === "card");
  const anchor = cards[cards.length - 1];
  if (!anchor) return null;
  return (
    <section
      className="card"
      aria-labelledby="doc-thread"
      data-testid="doc-thread"
    >
      <h2 id="doc-thread">Messages about it</h2>
      <div className="chat-stream" style={{ maxHeight: "none" }}>
        {list.map((m) => (
          <Bubble
            key={m.id}
            m={m}
            other="They"
            canReply={false}
            onReply={() => undefined}
          />
        ))}
      </div>
      <ErrorBox error={act.error} />
      {can("chat.trade") && (
        <form
          className="composer"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(
              async () => (
                await post(`/v1/link-chats/${anchor.link}/messages`, {
                  text: text.trim(),
                  reply_to: anchor.id,
                }),
                setText(""),
                thread.reload()
              ),
              "Sent",
            );
          }}
        >
          <label className="sr-only" htmlFor="doc-thread-text">
            Reply about {reference}
          </label>
          <textarea
            id="doc-thread-text"
            rows={1}
            maxLength={4000}
            placeholder={`Reply about ${reference}`}
            value={text}
            onChange={(e) => setText(e.target.value)}
          />
          <button
            type="submit"
            className="primary"
            disabled={act.busy || !text.trim()}
          >
            Send
          </button>
        </form>
      )}
    </section>
  );
}
