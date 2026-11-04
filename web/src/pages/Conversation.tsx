import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { del, get, post, put, upload } from "../api";
import { useAuth } from "../auth";
import {
  attachmentLabel,
  byDay,
  cardHref,
  cardLine,
  cardSub,
  clock,
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
} from "../lib/linkchat";
import { Icon } from "../icons";
import { MicButton, RecordingBar, useVoiceNote } from "./VoiceNote";
import { MentionList, MentionText, useMentions } from "./Mentions";
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
import { EmojiPanel } from "./EmojiPanel";
import { PendingFile } from "./PendingFile";
import { AttachMenu } from "./AttachMenu";
import { CameraSheet } from "./CameraSheet";
import { insertAt } from "../lib/emoji";
import { getDraft, setDraft } from "../lib/drafts";
import { useAutoGrow } from "../lib/useAutoGrow";
import { roleLine, type BusinessLink, type LinkCard } from "../lib/links";
import { ErrorBox, fmtTime, Modal, Skeleton, useAction, useLoad } from "../ui";

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
/** A swipe this far right replies. */
const SWIPE_REPLY_PX = 60;
/** A finger held this long on a bubble opens its menu, as WhatsApp's. */
const HOLD_MS = 500;

/** What can be attached: a photo or a PDF (a voice note is recorded). */
const ATTACH = "image/jpeg,image/png,image/webp,application/pdf";
const MAX_FILE = 25 * 1024 * 1024;

/** What can be done from a card, while it is its document's newest:
 * accept or decline a new order, accept a change, say all came, settle a
 * query, pay a bill (§5.5). */
function CardActions({ card, onDone }: { card: DocCard; onDone: () => void }) {
  const { can } = useAuth();
  const act = useAction();
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
              onClick={() =>
                confirm(`Decline ${card.reference}?`) &&
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
  const kept = messages.filter((m) => m.attachment).reverse();
  if (kept.length === 0) return null;
  return (
    <section className="chat-info-media" aria-label="Media and files">
      <h4>Media and files ({kept.length})</h4>
      <ul>
        {kept.map((m) => (
          <li key={m.id}>
            {m.attachment && (
              <AttachmentView m={m} a={m.attachment} other={other} />
            )}
          </li>
        ))}
      </ul>
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

/** The ticks under a message of ours: two when it is delivered, blue once
 * they have read it. */
export function Ticks({ read }: { read: boolean }) {
  return (
    <span
      className={`ticks${read ? " read" : ""}`}
      role="img"
      aria-label={read ? "Read" : "Delivered"}
    >
      <Icon name="tick-double" />
    </span>
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
  onShow,
  first = true,
  onReact,
  onStar,
  onInfo,
  onRetry,
  onDiscard,
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
  /** A search result: shows it in the conversation, around it. */
  onShow?: (id: string) => void;
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
}) {
  const card = m.kind === "card" ? m.card : undefined;
  const [more, setMore] = useState(false);
  const [copied, setCopied] = useState(false);
  const local = m.local;
  const hasMenu =
    !local &&
    (!!(onReact || onStar || onInfo) ||
      canReply ||
      (!card && !!(onForward || onHide || m.body)));
  const mineEmoji = m.reactions?.find((r) => r.mine)?.emoji;
  // Swipe right to reply, and a finger held to open the menu (reply,
  // copy, forward, delete for me), on a touch screen, as WhatsApp's; the
  // hold is let go by a move or a scroll.
  const drag = useRef<{ x: number; y: number; id: number } | null>(null);
  const hold = useRef<number>(undefined);
  const held = useRef(false);
  const [dx, setDx] = useState(0);
  const endHold = () => {
    window.clearTimeout(hold.current);
    hold.current = undefined;
  };
  const touch = {
    onPointerDown: (e: React.PointerEvent) => {
      if (e.pointerType === "mouse") return;
      if (canReply)
        drag.current = { x: e.clientX, y: e.clientY, id: e.pointerId };
      if (!hasMenu) return;
      endHold();
      hold.current = window.setTimeout(() => {
        hold.current = undefined;
        held.current = true;
        drag.current = null;
        setDx(0);
        setCopied(false);
        setMore(true);
      }, HOLD_MS);
    },
    onPointerMove: (e: React.PointerEvent) => {
      const d = drag.current;
      if (d && e.pointerId === d.id) {
        const x = e.clientX - d.x;
        if (Math.abs(x) > 10 || Math.abs(e.clientY - d.y) > 10) endHold();
        if (Math.abs(e.clientY - d.y) > 30 && x < 20) {
          drag.current = null;
          setDx(0);
          return;
        }
        setDx(Math.max(0, Math.min(x, 80)));
      } else if (e.pointerType !== "mouse") endHold();
    },
    onPointerUp: () => {
      endHold();
      if (drag.current && dx >= SWIPE_REPLY_PX) onReply(m);
      drag.current = null;
      setDx(0);
    },
    onPointerCancel: () => {
      endHold();
      drag.current = null;
      setDx(0);
    },
    // A long press on a touch screen also asks for the browser's own menu.
    onContextMenu: (e: React.MouseEvent) => {
      if (held.current) {
        e.preventDefault();
        held.current = false;
      }
    },
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.key === "Escape" && more) setMore(false);
    },
  };
  const what = card ? cardLine(card, m.mine, other) : previewLine(m, other);
  // The time and ticks sit in the corner of the last line of text (the text
  // keeps room for them); with a photo only, a link preview or a card they
  // have a row of their own.
  const inline =
    !card && !!m.body && !m.preview && !onShow && local !== "failed";
  return (
    <div
      className={`bubble${m.mine ? " mine" : ""}${first ? " first" : ""}${card ? " card-bubble" : ""}${flash ? " flash" : ""}${m.reactions?.length ? " has-reactions" : ""}`}
      data-msg={m.id}
      data-testid={card ? "chat-card" : "chat-message"}
      data-mine={m.mine}
      style={dx ? { transform: `translateX(${dx}px)` } : undefined}
      {...touch}
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
          {m.reply.mine ? "You" : other}:{" "}
          {previewLine({ ...m.reply, mine: m.reply.mine }, other)}
        </button>
      )}
      {m.reply && !onJump && (
        <span className="quote" data-testid="chat-quote">
          {m.reply.mine ? "You" : other}:{" "}
          {previewLine({ ...m.reply, mine: m.reply.mine }, other)}
        </span>
      )}
      {card ? (
        <>
          <span className="card-title">{cardLine(card, m.mine, other)}</span>
          {cardSub(card) && <span className="block hint">{cardSub(card)}</span>}
          <span className="row-actions" style={{ marginTop: 4 }}>
            <Link to={cardHref(card)} className="button small">
              Open<span className="sr-only"> {card.reference}</span>
            </Link>
          </span>
          {latest && !m.mine && <CardActions card={card} onDone={onDone} />}
        </>
      ) : (
        <>
          {m.attachment && (
            <AttachmentView m={m} a={m.attachment} other={other} />
          )}
          {m.body && (
            <span className="text">
              <MentionText text={m.body} mentions={m.mentions} />
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
      {more && (
        <span
          className="row-actions bubble-more"
          data-testid="chat-more"
          role="group"
          aria-label={`Options for ${what}`}
        >
          {onReact && (
            <span className="reaction-picker" role="group" aria-label="React">
              {REACTIONS.map((e) => (
                <button
                  key={e}
                  type="button"
                  className="react-pick"
                  aria-pressed={mineEmoji === e}
                  aria-label={`React with ${REACTION_NAMES[e]}`}
                  title={REACTION_NAMES[e]}
                  onClick={() => (setMore(false), onReact(m, e))}
                >
                  {e}
                </button>
              ))}
            </span>
          )}
          {canReply && (
            <button
              type="button"
              className="small"
              onClick={() => (setMore(false), onReply(m))}
            >
              Reply
            </button>
          )}
          {!card && m.body && (
            <button
              type="button"
              className="small"
              onClick={() =>
                void navigator.clipboard?.writeText(m.body ?? "").then(
                  () => setCopied(true),
                  () => undefined,
                )
              }
            >
              {copied ? "Copied" : "Copy"}
            </button>
          )}
          {!card && onForward && (
            <button
              type="button"
              className="small"
              onClick={() => (setMore(false), onForward(m))}
            >
              Forward
            </button>
          )}
          {onStar && (
            <button
              type="button"
              className="small"
              onClick={() => (setMore(false), onStar(m))}
            >
              {m.starred ? "Unstar" : "Star"}
            </button>
          )}
          {onInfo && (
            <button
              type="button"
              className="small"
              onClick={() => (setMore(false), onInfo(m))}
            >
              Info
            </button>
          )}
          {!card && onHide && (
            <button
              type="button"
              className="small danger"
              onClick={() => (setMore(false), onHide(m))}
            >
              Delete for me
            </button>
          )}
        </span>
      )}
      {(canReply || hasMenu) && !onShow && (
        <button
          type="button"
          className="bubble-chevron"
          aria-expanded={more}
          onClick={() => (setMore(!more), setCopied(false))}
        >
          <Icon name="down" />
          <span className="sr-only">More for {what}</span>
        </button>
      )}
      {local === "failed" && (
        <span className="chat-failed" role="alert" data-testid="chat-failed">
          <span className="bang" aria-hidden="true">
            !
          </span>
          <span className="why">Not sent. {m.localError}</span>
          <button
            type="button"
            className="small"
            onClick={() => onRetry?.(m.id.replace("local-", ""))}
          >
            Retry
          </button>
          <button
            type="button"
            className="small"
            onClick={() => onDiscard?.(m.id.replace("local-", ""))}
          >
            Delete
          </button>
        </span>
      )}
      <span className={`stamp${inline ? " inline" : ""}`}>
        {onShow && (
          <button
            type="button"
            className="reply-btn"
            data-testid="chat-show"
            onClick={() => onShow(m.id)}
          >
            Show
            <span className="sr-only"> in the conversation</span>
          </button>
        )}
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
      )}
    </div>
  );
}

/** One conversation: its stream, following it while open, and the
 * composer. */
export function ConversationPane({
  conv,
  onBack,
  onChanged,
  others = [],
  link,
  onEndLink,
  focus,
}: {
  conv: Conversation;
  onBack: () => void;
  onChanged: () => void;
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
  const [menuOpen, setMenuOpen] = useState(false);
  const [infoOpen, setInfoOpen] = useState(false);
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
  useEffect(() => {
    if (!menuOpen) return;
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setMenuOpen(false);
    const away = (e: MouseEvent) =>
      !(e.target as Element).closest?.(".chat-menu-wrap") && setMenuOpen(false);
    document.addEventListener("keydown", esc);
    document.addEventListener("mousedown", away);
    return () => {
      document.removeEventListener("keydown", esc);
      document.removeEventListener("mousedown", away);
    };
  }, [menuOpen]);
  const [forwarding, setForwarding] = useState<LinkMessage>();
  const typedAt = useRef(0);
  const [error, setError] = useState<unknown>();
  const [text, setText] = useState(() => getDraft(conv.link));
  const textBox = useRef<HTMLTextAreaElement>(null);
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [cameraOpen, setCameraOpen] = useState(false);
  const [dragging, setDragging] = useState(false);
  useAutoGrow(textBox, text);
  // An unsent message is kept per conversation (and shown in the list).
  useEffect(() => {
    setDraft(conv.link, text);
    // Only a change of the words is kept: a switch of conversation loads one.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [text]);
  const items = useLoad(
    () =>
      get<{ items: PriceItem[] }>(`/v1/link-chats/${conv.link}/items`).then(
        (r) => r.items,
        () => [] as PriceItem[],
      ),
    [conv.link],
  );
  const mentions = useMentions(text, setText, textBox, {
    "@": others
      .filter((o) => o.state === "active")
      .map((o) => ({ kind: "business" as const, id: o.link, name: o.name })),
    "#": (items.data ?? [])
      .filter((it) => it.available)
      .map((it) => ({
        kind: "item" as const,
        id: it.item_id,
        name: it.name,
        sub: priceWords(it),
      })),
  });
  const [reply, setReply] = useState<LinkMessage>();
  const [pending, setFile] = useState<File>();
  const file = pending;
  const [fileError, setFileError] = useState<unknown>();
  const pick = useRef<HTMLInputElement>(null);
  const snap = useRef<HTMLInputElement>(null);
  // A voice note held and released goes at once; one tapped waits for Send.
  const sendRef = useRef<(f?: File) => void>(() => undefined);
  const voice = useVoiceNote(
    (f, now) => (now ? sendRef.current(f) : setFile(f)),
    setFileError,
  );
  const [q, setQ] = useState("");
  const [found, setFound] = useState<LinkMessage[]>();
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
  const { stream, topMark, flashId } = thread;
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
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
  // A search result: close the search, then go to the message in place.
  const [showing, setShowing] = useState<string>();
  const showFound = (id: string) => {
    setQ("");
    setFound(undefined);
    setShowing(id);
  };
  useEffect(() => {
    if (!showing || found) return;
    setShowing(undefined);
    jump(showing);
    // jump is stable enough: it reads the thread's refs and openAt.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showing, found]);
  useEffect(() => {
    const t = setInterval(() => {
      if (document.visibilityState === "visible" && !detached)
        pull(last).catch(() => undefined);
    }, EVERY_MS);
    return () => clearInterval(t);
  }, [pull, last, detached]);
  // Our own ticks follow theirs reading: the page's read marks change in place.
  useEffect(() => {
    const t = setInterval(() => {
      // The newest page again: our ticks, and the other side's reactions.
      if (document.visibilityState !== "visible" || detached) return;
      get<{ messages: LinkMessage[] }>(`${path}/messages`).then(
        (out) => setMessages((cur) => merge(cur ?? [], out.messages)),
        () => undefined,
      );
    }, EVERY_MS * 3);
    return () => clearInterval(t);
  }, [messages, path, detached]);
  const choose = (f: File | undefined) => {
    setFileError(undefined);
    if (!f) return;
    if (f.size > MAX_FILE) {
      setFileError(new Error(`${f.name} is larger than 25 MB.`));
      return;
    }
    if (!/^(image\/(jpeg|png|webp)|application\/pdf|audio\/)/.test(f.type)) {
      setFileError(
        new Error("Send a photo (JPEG, PNG or WebP), a PDF or a voice note."),
      );
      return;
    }
    setFile(f);
  };
  // The clip's menu: a photo, a document, or the camera (the computer's
  // webcam, or on a phone the camera app).
  const attach = (what: "photo" | "document" | "camera") => {
    if (what === "camera") {
      const touch = window.matchMedia?.("(pointer: coarse)").matches;
      if (!touch && typeof navigator.mediaDevices?.getUserMedia === "function")
        setCameraOpen(true);
      else snap.current?.click();
      return;
    }
    const el = pick.current;
    if (!el) return;
    el.accept =
      what === "photo" ? "image/jpeg,image/png,image/webp" : "application/pdf";
    el.click();
  };
  const addEmoji = (c: string) => {
    const el = textBox.current;
    const start = el?.selectionStart ?? text.length;
    const r = insertAt(text, start, el?.selectionEnd ?? start, c);
    typed(r.text);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(r.caret, r.caret);
    });
  };
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
  const send = (held?: File) => {
    const file = held ?? pending;
    const body = held ? "" : text.trim();
    if (!body && !file) return;
    const o: Outgoing = {
      clientId: newClientId(),
      body,
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
      mentions: file ? undefined : mentions.refs(),
      state: "sending",
      at: new Date().toISOString(),
    };
    if (!held) {
      setText("");
      setFile(undefined);
      mentions.reset();
    }
    setReply(undefined);
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
  sendRef.current = send;
  const settings = (
    body: { muted?: boolean; archived?: boolean; pinned?: boolean },
    done: string,
  ) => void act.run(async () => (await put(path, body), onChanged()), done);
  // We are typing: the other side's typing line, told at most every 3 s.
  const typed = (v: string) => {
    setText(v);
    const now = Date.now();
    if (v.trim() && now - typedAt.current > 3000) {
      typedAt.current = now;
      post(`${path}/typing`).catch(() => undefined);
    }
  };
  const hide = (m: LinkMessage) => {
    if (!confirm("Delete this message for your business? They keep theirs."))
      return;
    void act.run(async () => {
      await post(`${path}/messages/${m.id}/hide`);
      setMessages((cur) => cur?.filter((x) => x.id !== m.id));
      onChanged();
    }, "Deleted for you");
  };
  const search = (v: string) => {
    setQ(v);
    if (v.trim().length < 2) return setFound(undefined);
    get<{ messages: LinkMessage[] }>(
      `${path}/messages?q=${encodeURIComponent(v.trim())}`,
    ).then((r) => setFound(r.messages), setError);
  };
  const shown = found ?? threadItems;
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
      className="chat-pane"
      aria-label={`Conversation with ${conv.name}`}
      data-testid="chat-pane"
      onDragEnter={(e) => {
        if (write && e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragOver={(e) => {
        if (write && e.dataTransfer.types.includes("Files")) e.preventDefault();
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null))
          setDragging(false);
      }}
      onDrop={(e) => {
        if (!write) return;
        e.preventDefault();
        setDragging(false);
        choose(e.dataTransfer.files[0]);
        // One file goes at a time, with its caption: say so.
        if (e.dataTransfer.files.length > 1)
          setFileError(
            new Error(
              "Only one file can be sent at a time: the first was taken. Send the others after it.",
            ),
          );
      }}
    >
      <div className="chat-head">
        <button
          type="button"
          className="icon-button chat-back"
          onClick={onBack}
          aria-label="Back to the chats"
        >
          <Icon name="back" />
        </button>
        <button
          type="button"
          className="chat-avatar"
          aria-label={`About ${conv.name}`}
          aria-expanded={infoOpen}
          onClick={() => setInfoOpen(!infoOpen)}
        >
          {conv.name.slice(0, 1).toUpperCase()}
        </button>
        <h2>
          {conv.name}
          {typing && (
            <span className="typing" data-testid="chat-typing">
              typing…
            </span>
          )}
        </h2>
        {can("chat.trade") && (
          <div className="chat-menu-wrap">
            <button
              type="button"
              className="icon-button"
              aria-label="More about this conversation"
              aria-expanded={menuOpen}
              aria-haspopup="true"
              onClick={() => setMenuOpen(!menuOpen)}
            >
              <Icon name="more" />
            </button>
            {menuOpen && (
              <div className="chat-menu" data-testid="chat-menu">
                <button
                  type="button"
                  onClick={() => (setMenuOpen(false), setInfoOpen(true))}
                >
                  Contact info
                </button>
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() => {
                    setMenuOpen(false);
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
                    setMenuOpen(false);
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
                    setMenuOpen(false);
                    settings(
                      { archived: !conv.archived },
                      conv.archived ? "Unarchived" : "Archived",
                    );
                  }}
                >
                  {conv.archived ? "Unarchive" : "Archive"}
                </button>
              </div>
            )}
          </div>
        )}
      </div>
      <div
        className="filters"
        role="search"
        aria-label={`Search the conversation with ${conv.name}`}
        style={{ padding: "8px 12px 0" }}
      >
        <label className="search">
          <span className="sr-only">Search this conversation</span>
          <Icon name="search" />
          <input
            type="search"
            placeholder="Search"
            value={q}
            onChange={(e) => search(e.target.value)}
            data-testid="chat-search"
          />
        </label>
      </div>
      <ErrorBox error={error ?? act.error ?? fileError} />
      <div className="chat-stream-wrap">
        <div
          className="chat-stream"
          aria-live="polite"
          data-testid="chat-stream"
          role="log"
          tabIndex={0}
          aria-label="Chat history"
          ref={stream}
          onScroll={thread.onScroll}
        >
          {hasOlder && !found && shown && shown.length > 0 && (
            <div
              ref={topMark}
              className="chat-top"
              data-testid="chat-top"
              aria-hidden="true"
            />
          )}
          {!shown ? (
            <Skeleton rows={3} label="Loading the conversation…" />
          ) : shown.length === 0 ? (
            <p className="hint" style={{ alignSelf: "center" }}>
              {found
                ? "No messages say that."
                : `No messages yet. Say hello to ${conv.name}.`}
            </p>
          ) : (
            byDay(shown).map((g) => (
              <div key={g.day} className="chat-group">
                <span className="chat-day">{g.day}</span>
                {g.messages.map((m, i) => (
                  <Fragment key={m.id}>
                    {!found && unread?.id === m.id && (
                      <div
                        className="chat-unread"
                        data-unread
                        data-testid="chat-unread"
                      >
                        {unread.count === 1
                          ? "1 unread message"
                          : `${unread.count} unread messages`}
                      </div>
                    )}
                    <Bubble
                      m={m}
                      other={conv.name}
                      canReply={write && !m.local}
                      onRetry={retry}
                      onDiscard={discard}
                      onReply={setReply}
                      onJump={found ? undefined : jump}
                      onShow={found ? showFound : undefined}
                      first={startsRun(g.messages[i - 1], m)}
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
                    />
                  </Fragment>
                ))}
              </div>
            ))
          )}
        </div>
        {!found && (thread.away || detached) && (
          <ThreadJump
            detached={detached}
            count={thread.newCount}
            onClick={() => (detached ? toLatest() : thread.toBottom())}
          />
        )}
      </div>
      {write ? (
        <>
          {reply && (
            <div
              className="composer-reply"
              data-testid="chat-replying"
              onKeyDown={(e) => {
                if (e.key === "Escape") setReply(undefined);
              }}
            >
              <span className="quote">
                Replying to {previewLine(reply, conv.name)}
              </span>
              <button
                type="button"
                className="small"
                onClick={() => setReply(undefined)}
              >
                Cancel<span className="sr-only"> the reply</span>
              </button>
            </div>
          )}
          {file && (
            <PendingFile file={file} onRemove={() => setFile(undefined)} />
          )}
          <input
            ref={pick}
            type="file"
            accept={ATTACH}
            hidden
            data-testid="chat-file"
            onChange={(e) => {
              choose(e.target.files?.[0]);
              e.target.value = "";
            }}
          />
          <input
            ref={snap}
            type="file"
            accept="image/jpeg,image/png,image/webp"
            capture="environment"
            hidden
            onChange={(e) => {
              choose(e.target.files?.[0]);
              e.target.value = "";
            }}
          />
          {emojiOpen && (
            <EmojiPanel onPick={addEmoji} onClose={() => setEmojiOpen(false)} />
          )}
          <MentionList m={mentions} />
          {voice.secs !== undefined || voice.holding ? (
            <RecordingBar voice={voice} />
          ) : (
            <form
              className="composer"
              onSubmit={(e) => {
                e.preventDefault();
                send();
              }}
            >
              <button
                type="button"
                className="icon-button"
                aria-label="Emoji"
                title="Emoji"
                aria-expanded={emojiOpen}
                data-testid="emoji-button"
                onClick={() => setEmojiOpen(!emojiOpen)}
              >
                <Icon name="emoji" />
              </button>
              <AttachMenu onChoose={attach} />
              <label className="sr-only" htmlFor="chat-text">
                Message
              </label>
              <textarea
                id="chat-text"
                ref={textBox}
                rows={1}
                maxLength={4000}
                placeholder={file ? "Add a caption" : "Type a message"}
                value={text}
                onChange={(e) => {
                  typed(e.target.value);
                  mentions.update(e.target.value, e.target.selectionStart);
                }}
                onPaste={(e) => {
                  // A pasted picture is a photo to send, with a caption.
                  const f = Array.from(e.clipboardData.files).find((x) =>
                    x.type.startsWith("image/"),
                  );
                  if (f) {
                    e.preventDefault();
                    choose(f);
                  }
                }}
                onKeyDown={(e) => {
                  if (mentions.onKeyDown(e)) return;
                  if (e.key === "Escape") {
                    // Esc gives up what is being prepared: the panel, a
                    // photo waiting for its caption, then a reply.
                    if (emojiOpen) setEmojiOpen(false);
                    else if (file) setFile(undefined);
                    else if (reply) setReply(undefined);
                    return;
                  }
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    send();
                  }
                }}
              />
              {!text.trim() && !file && (
                <button
                  type="button"
                  className="icon-button camera-quick"
                  aria-label="Take a photo"
                  title="Take a photo"
                  data-testid="camera-quick"
                  onClick={() => attach("camera")}
                >
                  <Icon name="camera" />
                </button>
              )}
              {!text.trim() && !file && voice.can ? (
                <MicButton
                  voice={voice}
                  onStart={() => setFileError(undefined)}
                />
              ) : (
                <button
                  type="submit"
                  className="primary"
                  disabled={act.busy || (!text.trim() && !file)}
                >
                  {act.busy && file ? "Sending…" : "Send"}
                </button>
              )}
            </form>
          )}
        </>
      ) : (
        <p className="hint" style={{ padding: "10px 14px" }}>
          {conv.state === "active"
            ? "You can read this conversation. Someone who writes to linked businesses for your business can reply."
            : "This link has ended. The conversation stays."}
        </p>
      )}
      {dragging && (
        <div className="chat-drop" data-testid="chat-drop" aria-hidden="true">
          Drop a photo or a PDF to send it
        </div>
      )}
      {cameraOpen && (
        <CameraSheet
          onClose={() => setCameraOpen(false)}
          onTake={(f) => {
            setCameraOpen(false);
            choose(f);
          }}
        />
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
      {infoOpen && (
        <aside
          className="chat-info"
          aria-label={`About ${conv.name}`}
          data-testid="chat-info"
        >
          <div className="chat-info-head">
            <h3>Contact info</h3>
            <button
              type="button"
              className="icon-button"
              aria-label="Close contact info"
              onClick={() => setInfoOpen(false)}
            >
              <Icon name="close" />
            </button>
          </div>
          <div className="chat-info-body">
            <span className="chat-avatar big" aria-hidden="true">
              {conv.name.slice(0, 1).toUpperCase()}
            </span>
            <p className="chat-info-name">{conv.name}</p>
            {link && <p className="hint">{roleLine(link.role)}</p>}
            {link?.state === "active" && link.answered_at && (
              <p className="hint">Linked {fmtTime(link.answered_at)}</p>
            )}
            {card && (card.area || card.tagline || card.items.length > 0) && (
              <section
                className="chat-info-card"
                aria-label="About the business"
              >
                {card.tagline && <p>{card.tagline}</p>}
                {card.area && <p className="hint">{card.area}</p>}
                {card.items.length > 0 && (
                  <ul className="chat-info-items">
                    {card.items.slice(0, 5).map((it) => (
                      <li key={it.name}>{it.name}</li>
                    ))}
                  </ul>
                )}
              </section>
            )}
            {conv.state !== "active" && (
              <p className="hint">The link has ended.</p>
            )}
            {can("chat.trade") && (
              <div className="chat-info-actions">
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() =>
                    settings(
                      { pinned: !conv.pinned },
                      conv.pinned ? "Unpinned" : "Pinned",
                    )
                  }
                >
                  {conv.pinned ? "Unpin this chat" : "Pin this chat"}
                </button>
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() =>
                    settings(
                      { muted: !conv.muted },
                      conv.muted ? "Unmuted" : "Muted",
                    )
                  }
                >
                  {conv.muted ? "Unmute notifications" : "Mute notifications"}
                </button>
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() =>
                    settings(
                      { archived: !conv.archived },
                      conv.archived ? "Unarchived" : "Archived",
                    )
                  }
                >
                  {conv.archived ? "Unarchive this chat" : "Archive this chat"}
                </button>
                {link && link.state === "active" && onEndLink && (
                  <button
                    type="button"
                    className="danger"
                    disabled={act.busy}
                    onClick={() =>
                      confirm(
                        `End the link with ${link.name}? What passed between you stays on both sides.`,
                      ) && onEndLink(link)
                    }
                  >
                    End link
                  </button>
                )}
              </div>
            )}
            <MediaAndFiles messages={messages ?? []} other={conv.name} />
          </div>
        </aside>
      )}
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
