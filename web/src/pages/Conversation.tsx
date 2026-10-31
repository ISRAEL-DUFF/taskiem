import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { get, post, put } from "../api";
import { useAuth } from "../auth";
import {
  byDay,
  cardHref,
  cardLine,
  cardSub,
  clock,
  merge,
  previewLine,
  type Conversation,
  type DocCard,
  type LinkMessage,
} from "../lib/linkchat";
import { ErrorBox, fmtTime, Skeleton, useAction, useLoad } from "../ui";

// The conversation with a linked business (business links L3; §5.5): the
// stream of bubbles, theirs on the left and ours on the right, grouped by
// day, with the time and the ticks in the corner, quoted replies, and the
// documents between the two businesses as cards; the composer at the
// bottom. As WhatsApp works, deliberately (§5.5): a bubble's corner shows
// the clock, as WhatsApp's does, with the time in words on hover.

const EVERY_MS = 3000;

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

function Bubble({
  m,
  other,
  onReply,
  canReply,
  latest = false,
  onDone = () => undefined,
}: {
  m: LinkMessage;
  other: string;
  onReply: (m: LinkMessage) => void;
  canReply: boolean;
  latest?: boolean;
  onDone?: () => void;
}) {
  const card = m.kind === "card" ? m.card : undefined;
  return (
    <div
      className={`bubble${m.mine ? " mine" : ""}${card ? " card-bubble" : ""}`}
      data-testid={card ? "chat-card" : "chat-message"}
      data-mine={m.mine}
    >
      {!m.mine && !card && m.sender_name && (
        <span className="who">{m.sender_name}</span>
      )}
      {m.reply && (
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
        <span className="text">{m.body}</span>
      )}
      <span className="meta">
        {canReply && (
          <button
            type="button"
            className="reply-btn"
            onClick={() => onReply(m)}
          >
            Reply
            <span className="sr-only">
              {" "}
              to {card ? cardLine(card, m.mine, other) : m.body}
            </span>
          </button>
        )}
        <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
          {clock(m.created_at)}
        </time>
        {m.mine && !card && (
          <span
            className={`ticks${m.read_at ? " read" : ""}`}
            aria-label={m.read_at ? "Read" : "Delivered"}
            role="img"
          >
            {m.read_at ? "✓✓" : "✓"}
          </span>
        )}
      </span>
    </div>
  );
}

/** One conversation: its stream, following it while open, and the
 * composer. */
export function ConversationPane({
  conv,
  onBack,
  onChanged,
}: {
  conv: Conversation;
  onBack: () => void;
  onChanged: () => void;
}) {
  const { can } = useAuth();
  const write = can("chat.trade") && conv.state === "active";
  const [messages, setMessages] = useState<LinkMessage[]>();
  const [error, setError] = useState<unknown>();
  const [text, setText] = useState("");
  const [reply, setReply] = useState<LinkMessage>();
  const [q, setQ] = useState("");
  const [found, setFound] = useState<LinkMessage[]>();
  const act = useAction();
  const end = useRef<HTMLDivElement>(null);
  const path = `/v1/link-chats/${conv.link}`;
  const last = messages?.[messages.length - 1]?.created_at;

  // The newest page, then what is newer every few seconds; read while seen.
  const pull = useCallback(
    async (after?: string) => {
      const out = await get<{ messages: LinkMessage[] }>(
        `${path}/messages${after ? `?after=${encodeURIComponent(after)}` : ""}`,
      );
      if (after && out.messages.length === 0) return;
      setMessages((cur) => merge(cur ?? [], out.messages));
      if (
        out.messages.some((m) => !m.mine && !m.read_at) &&
        document.visibilityState === "visible"
      ) {
        await post(`${path}/read`).then(onChanged, () => undefined);
      }
    },
    [path, onChanged],
  );
  useEffect(() => {
    setMessages(undefined);
    setReply(undefined);
    pull().catch(setError);
  }, [pull]);
  useEffect(() => {
    const t = setInterval(() => {
      if (document.visibilityState === "visible")
        pull(last).catch(() => undefined);
    }, EVERY_MS);
    return () => clearInterval(t);
  }, [pull, last]);
  // Our own ticks follow theirs reading: the page's read marks change in place.
  useEffect(() => {
    const t = setInterval(() => {
      if (
        document.visibilityState !== "visible" ||
        !messages?.some((m) => m.mine && !m.read_at)
      )
        return;
      get<{ messages: LinkMessage[] }>(`${path}/messages`).then(
        (out) => setMessages((cur) => merge(cur ?? [], out.messages)),
        () => undefined,
      );
    }, EVERY_MS * 3);
    return () => clearInterval(t);
  }, [messages, path]);
  useEffect(() => {
    end.current?.scrollIntoView?.({ block: "end" });
  }, [messages?.length]);

  const send = () => {
    const body = text.trim();
    if (!body) return;
    void act
      .run(async () => {
        const out = await post<{ message: LinkMessage }>(`${path}/messages`, {
          text: body,
          reply_to: reply?.id,
        });
        setMessages((cur) => merge(cur ?? [], [out.message]));
        setText("");
        setReply(undefined);
        onChanged();
      }, false)
      .then(() => undefined);
  };
  const settings = (
    body: { muted?: boolean; archived?: boolean },
    done: string,
  ) => void act.run(async () => (await put(path, body), onChanged()), done);
  const search = (v: string) => {
    setQ(v);
    if (v.trim().length < 2) return setFound(undefined);
    get<{ messages: LinkMessage[] }>(
      `${path}/messages?q=${encodeURIComponent(v.trim())}`,
    ).then((r) => setFound(r.messages), setError);
  };
  const shown = found ?? messages;
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
    >
      <div className="chat-head">
        <button type="button" className="small chat-back" onClick={onBack}>
          Back<span className="sr-only"> to the chats</span>
        </button>
        <h2>{conv.name}</h2>
        {can("chat.trade") && (
          <>
            <button
              type="button"
              className="small"
              disabled={act.busy}
              onClick={() =>
                settings(
                  { muted: !conv.muted },
                  conv.muted ? "Unmuted" : "Muted",
                )
              }
            >
              {conv.muted ? "Unmute" : "Mute"}
            </button>
            <button
              type="button"
              className="small"
              disabled={act.busy}
              onClick={() =>
                settings(
                  { archived: !conv.archived },
                  conv.archived ? "Unarchived" : "Archived",
                )
              }
            >
              {conv.archived ? "Unarchive" : "Archive"}
            </button>
          </>
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
          <input
            type="search"
            placeholder="Search"
            value={q}
            onChange={(e) => search(e.target.value)}
            data-testid="chat-search"
          />
        </label>
      </div>
      <ErrorBox error={error ?? act.error} />
      <div className="chat-stream" aria-live="polite" data-testid="chat-stream">
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
            <div key={g.day} style={{ display: "contents" }}>
              <span className="chat-day">{g.day}</span>
              {g.messages.map((m) => (
                <Bubble
                  key={m.id}
                  m={m}
                  other={conv.name}
                  canReply={write}
                  onReply={setReply}
                  latest={newest.has(m.id)}
                  onDone={() => pull(last).catch(setError)}
                />
              ))}
            </div>
          ))
        )}
        <div ref={end} />
      </div>
      {write ? (
        <>
          {reply && (
            <div className="composer-reply" data-testid="chat-replying">
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
          <form
            className="composer"
            onSubmit={(e) => {
              e.preventDefault();
              send();
            }}
          >
            <label className="sr-only" htmlFor="chat-text">
              Message
            </label>
            <textarea
              id="chat-text"
              rows={1}
              maxLength={4000}
              placeholder="Type a message"
              value={text}
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  send();
                }
              }}
            />
            <button
              type="submit"
              className="primary"
              disabled={act.busy || !text.trim()}
            >
              Send
            </button>
          </form>
        </>
      ) : (
        <p className="hint" style={{ padding: "10px 14px" }}>
          {conv.state === "active"
            ? "You can read this conversation. Someone who writes to linked businesses for your business can reply."
            : "This link has ended. The conversation stays."}
        </p>
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
