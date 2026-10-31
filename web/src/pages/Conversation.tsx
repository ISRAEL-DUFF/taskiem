import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Link } from "react-router-dom";
import { get, post, put, upload } from "../api";
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
  type Attachment,
  type Conversation,
  type DocCard,
  type LinkMessage,
} from "../lib/linkchat";
import { Icon } from "../icons";
import { ErrorBox, fmtTime, Skeleton, useAction, useLoad } from "../ui";

// The conversation with a linked business (business links L3; §5.5): the
// stream of bubbles, theirs on the left and ours on the right, grouped by
// day, with the time and the ticks in the corner, quoted replies, and the
// documents between the two businesses as cards; the composer at the
// bottom. As WhatsApp works, deliberately (§5.5): a bubble's corner shows
// the clock, as WhatsApp's does, with the time in words on hover.

const EVERY_MS = 3000;

/** What can be attached: a photo or a PDF (a voice note is recorded). */
const ATTACH = "image/jpeg,image/png,image/webp,application/pdf";
const MAX_FILE = 25 * 1024 * 1024;
const MAX_VOICE_SECONDS = 600;

/** Records a voice note with the browser's microphone: secs counts up
 * while recording; stop(true) hands the note to onDone, stop(false)
 * throws it away. Unavailable where the browser cannot record. */
function useVoiceNote(
  onDone: (f: File) => void,
  onError: (e: unknown) => void,
) {
  const [secs, setSecs] = useState<number>();
  const rec = useRef<MediaRecorder | null>(null);
  const keep = useRef(false);
  const can =
    typeof window !== "undefined" &&
    typeof window.MediaRecorder !== "undefined" &&
    !!navigator.mediaDevices?.getUserMedia;
  const stop = useCallback((keepIt: boolean) => {
    keep.current = keepIt;
    if (rec.current && rec.current.state !== "inactive") rec.current.stop();
  }, []);
  useEffect(() => () => stop(false), [stop]);
  useEffect(() => {
    if (secs === undefined) return;
    if (secs >= MAX_VOICE_SECONDS) {
      stop(true);
      return;
    }
    const t = setTimeout(() => setSecs((s) => (s ?? 0) + 1), 1000);
    return () => clearTimeout(t);
  }, [secs, stop]);
  const start = async () => {
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const type = [
        "audio/ogg;codecs=opus",
        "audio/webm;codecs=opus",
        "audio/mp4",
      ].find((t) => MediaRecorder.isTypeSupported(t));
      const r = new MediaRecorder(
        stream,
        type ? { mimeType: type } : undefined,
      );
      const chunks: Blob[] = [];
      r.ondataavailable = (e) => {
        if (e.data.size > 0) chunks.push(e.data);
      };
      r.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        setSecs(undefined);
        rec.current = null;
        if (!keep.current || chunks.length === 0) return;
        const mime = (r.mimeType || "audio/webm").split(";")[0] ?? "audio/webm";
        const ext = mime.includes("ogg")
          ? "ogg"
          : mime.includes("mp4")
            ? "m4a"
            : "webm";
        onDone(new File(chunks, `voice-note.${ext}`, { type: mime }));
      };
      keep.current = false;
      rec.current = r;
      r.start();
      setSecs(0);
    } catch {
      onError(
        new Error(
          "The microphone could not be used. Allow it for this page and try again.",
        ),
      );
    }
  };
  return { can, secs, start, stop };
}

const minsSecs = (s: number) =>
  `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;

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
        <>
          {m.attachment && (
            <AttachmentView m={m} a={m.attachment} other={other} />
          )}
          {m.body && <span className="text">{m.body}</span>}
        </>
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
              to {card ? cardLine(card, m.mine, other) : previewLine(m, other)}
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
  const [file, setFile] = useState<File>();
  const [fileError, setFileError] = useState<unknown>();
  const pick = useRef<HTMLInputElement>(null);
  const snap = useRef<HTMLInputElement>(null);
  const voice = useVoiceNote(setFile, setFileError);
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

  const choose = (f: File | undefined) => {
    setFileError(undefined);
    if (!f) return;
    if (f.size > MAX_FILE) {
      setFileError(new Error(`${f.name} is larger than 25 MB.`));
      return;
    }
    setFile(f);
  };
  const send = () => {
    const body = text.trim();
    if (!body && !file) return;
    void act
      .run(async () => {
        let out: { message: LinkMessage };
        if (file) {
          const form = new FormData();
          form.append("file", file, file.name);
          if (body) form.append("text", body);
          if (reply) form.append("reply_to", reply.id);
          out = await upload<{ message: LinkMessage }>(`${path}/files`, form);
        } else {
          out = await post<{ message: LinkMessage }>(`${path}/messages`, {
            text: body,
            reply_to: reply?.id,
          });
        }
        setMessages((cur) => merge(cur ?? [], [out.message]));
        setText("");
        setFile(undefined);
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
      <ErrorBox error={error ?? act.error ?? fileError} />
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
          {file && (
            <div className="composer-reply" data-testid="chat-attaching">
              <span className="quote">
                {file.type.startsWith("audio/")
                  ? "Voice note"
                  : file.type.startsWith("image/")
                    ? `Photo: ${file.name}`
                    : file.name}{" "}
                · {fileSize(file.size)}
              </span>
              <button
                type="button"
                className="small"
                onClick={() => setFile(undefined)}
              >
                Remove<span className="sr-only"> the file</span>
              </button>
            </div>
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
            accept="image/*"
            capture="environment"
            hidden
            onChange={(e) => {
              choose(e.target.files?.[0]);
              e.target.value = "";
            }}
          />
          {voice.secs !== undefined ? (
            <div className="composer" data-testid="chat-recording">
              <span className="recording" role="status">
                <span className="dot" aria-hidden="true" />
                Recording {minsSecs(voice.secs)}
              </span>
              <button
                type="button"
                className="small"
                onClick={() => voice.stop(false)}
              >
                Cancel<span className="sr-only"> the voice note</span>
              </button>
              <button
                type="button"
                className="primary"
                onClick={() => voice.stop(true)}
              >
                Done
              </button>
            </div>
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
                aria-label="Attach a photo or a PDF"
                title="Attach a photo or a PDF"
                onClick={() => pick.current?.click()}
              >
                <Icon name="attach" />
              </button>
              <button
                type="button"
                className="icon-button"
                aria-label="Take a photo"
                title="Take a photo"
                onClick={() => snap.current?.click()}
              >
                <Icon name="camera" />
              </button>
              <label className="sr-only" htmlFor="chat-text">
                Message
              </label>
              <textarea
                id="chat-text"
                rows={1}
                maxLength={4000}
                placeholder={file ? "Add a caption" : "Type a message"}
                value={text}
                onChange={(e) => setText(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter" && !e.shiftKey) {
                    e.preventDefault();
                    send();
                  }
                }}
              />
              {!text.trim() && !file && voice.can ? (
                <button
                  type="button"
                  className="icon-button primary"
                  aria-label="Record a voice note"
                  title="Record a voice note"
                  onClick={() => {
                    setFileError(undefined);
                    void voice.start();
                  }}
                >
                  <Icon name="mic" />
                </button>
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
