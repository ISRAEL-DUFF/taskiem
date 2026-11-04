import { useCallback, useEffect, useRef, useState } from "react";
import { useThread } from "../lib/useThread";
import { ThreadJump } from "./ThreadParts";
import { EmojiPanel } from "./EmojiPanel";
import { insertAt } from "../lib/emoji";
import { getDraft, setDraft } from "../lib/drafts";
import { useAutoGrow } from "../lib/useAutoGrow";
import { get, post, upload } from "../api";
import type { Conversation } from "../lib/linkchat";
import { priceWords, type SupplierPrices } from "../lib/pricelists";
import { MentionList, useMentions } from "./Mentions";
import { Icon } from "../icons";
import { MicButton, RecordingBar, useVoiceNote } from "./VoiceNote";
import { clock, dayLabel } from "../lib/linkchat";
import {
  boldParts,
  mergeTaskiem,
  type TaskiemMessage,
} from "../lib/taskiemchat";
import { ErrorBox, fmtTime, Skeleton, useAction, useLoad } from "../ui";

// The Taskiem conversation on the web (business links L3, §5.5): the
// person's own conversation with Taskiem, the one they have on WhatsApp,
// with the same words (status, orders, cash in, invoice …) and the same
// history from every channel. Their messages on the right, Taskiem's on
// the left; the buttons of Taskiem's newest answer press as on the phone.

const EVERY_MS = 5000;
/** The newest page, and a page older, as the API gives them. */
const PAGE = 50;
const CHANNEL: Record<string, string> = {
  whatsapp: "WhatsApp",
  telegram: "Telegram",
  slack: "Slack",
};

export function TaskiemPane({ onBack }: { onBack: () => void }) {
  const [messages, setMessages] = useState<TaskiemMessage[]>();
  const [live, setLive] = useState<string>(); // the answer whose buttons press
  const [error, setError] = useState<unknown>();
  const [text, setText] = useState(() => getDraft("taskiem"));
  const textBox = useRef<HTMLTextAreaElement>(null);
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [dragging, setDragging] = useState(false);
  useAutoGrow(textBox, text);
  // An unsent message is kept (and shown in the list).
  useEffect(() => {
    setDraft("taskiem", text);
  }, [text]);
  // "@" a linked business, "#" an item of a supplier's price list.
  const convs = useLoad(
    () =>
      get<{ conversations: Conversation[] }>("/v1/link-chats").then(
        (r) => r.conversations,
        () => [] as Conversation[],
      ),
    [],
  );
  const prices = useLoad(
    () =>
      get<{ suppliers: SupplierPrices[] }>("/v1/supplier-prices").then(
        (r) => r.suppliers,
        () => [] as SupplierPrices[],
      ),
    [],
  );
  const mentions = useMentions(text, setText, textBox, {
    "@": (convs.data ?? [])
      .filter((c) => c.state === "active")
      .map((c) => ({ kind: "business" as const, id: c.link, name: c.name })),
    "#": (prices.data ?? []).flatMap((p) =>
      p.items
        .filter((it) => it.available)
        .map((it) => ({
          kind: "item" as const,
          id: `${p.link}:${it.item_id}`,
          name: it.name,
          sub: `${p.business} · ${priceWords(it)}`,
        })),
    ),
  });
  // A photo waiting for its caption, or a voice note tapped (not held).
  const [file, setFile] = useState<File>();
  const [fileError, setFileError] = useState<unknown>();
  const pick = useRef<HTMLInputElement>(null);
  const snap = useRef<HTMLInputElement>(null);
  const sendFileRef = useRef<(f: File) => void>(() => undefined);
  const voice = useVoiceNote(
    (f, now) => (now ? sendFileRef.current(f) : setFile(f)),
    setFileError,
  );
  const act = useAction();
  const last = messages?.[messages.length - 1]?.created_at;
  // Scrolls as the other conversations do (lib/useThread): older messages
  // load near the top, new ones are followed only at the bottom.
  const [hasOlder, setHasOlder] = useState(false);
  const loadingOlder = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const thread = useThread({
    items: messages,
    isMine: (m) => m.inbound,
    hasOlder,
    loadOlder: olderCb,
  });

  const pull = useCallback(async (after?: string) => {
    const out = await get<{ messages: TaskiemMessage[] }>(
      `/v1/taskiem-chat${after ? `?after=${encodeURIComponent(after)}` : ""}`,
    );
    if (after && out.messages.length === 0) return;
    setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
    // A full page of what is newer: there may be more (the reader was away).
    const newest = out.messages[out.messages.length - 1];
    if (after && out.messages.length >= 200 && newest)
      await pull(newest.created_at);
  }, []);
  // The newest page: the view is placed at its end.
  useEffect(() => {
    get<{ messages: TaskiemMessage[] }>("/v1/taskiem-chat")
      .then((out) => {
        thread.reset();
        setHasOlder(out.messages.length >= PAGE);
        setMessages(mergeTaskiem([], out.messages));
      })
      .catch(setError);
    // The thread's reset is stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const loadOlder = async () => {
    const first = messages?.[0];
    if (!first || loadingOlder.current) return;
    loadingOlder.current = true;
    try {
      const out = await get<{ messages: TaskiemMessage[] }>(
        `/v1/taskiem-chat?before=${encodeURIComponent(first.created_at)}`,
      );
      if (out.messages.length < PAGE) setHasOlder(false);
      if (out.messages.length > 0) {
        thread.keepPlace();
        setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
      }
    } catch (e) {
      setError(e);
    } finally {
      loadingOlder.current = false;
    }
  };
  loadOlderRef.current = () => void loadOlder();
  useEffect(() => {
    const t = setInterval(() => {
      if (document.visibilityState === "visible")
        pull(last).catch(() => undefined);
    }, EVERY_MS);
    return () => clearInterval(t);
  }, [pull, last]);

  const say = (body: { text?: string; reply?: string }) =>
    void act
      .run(async () => {
        const out = await post<{ messages: TaskiemMessage[] }>(
          "/v1/taskiem-chat",
          body,
        );
        setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
        const answer = [...out.messages]
          .reverse()
          .find((m) => !m.inbound && m.buttons?.some((b) => b.id));
        setLive(answer ? `${answer.id}@${answer.created_at}` : undefined);
        if (body.text !== undefined && !body.reply) {
          setText("");
          mentions.reset();
        }
      }, false)
      .then(() => undefined);
  // A voice note or a photo: read as one sent on WhatsApp.
  const sendFile = (f: File, caption = "") =>
    void act
      .run(async () => {
        const form = new FormData();
        form.append("file", f, f.name);
        if (caption) form.append("text", caption);
        const out = await upload<{ messages: TaskiemMessage[] }>(
          "/v1/taskiem-chat/files",
          form,
        );
        setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
        const answer = [...out.messages]
          .reverse()
          .find((m) => !m.inbound && m.buttons?.some((b) => b.id));
        setLive(answer ? `${answer.id}@${answer.created_at}` : undefined);
        setFile(undefined);
        if (caption) setText("");
      }, false)
      .then(() => undefined);
  sendFileRef.current = (f) => sendFile(f);
  const send = () => {
    const body = text.trim();
    if (file) sendFile(file, body);
    else if (body) say({ text: body });
  };
  const choose = (f: File | undefined) => {
    setFileError(undefined);
    if (f && f.size > 10 * 1024 * 1024)
      return setFileError(new Error(`${f.name} is larger than 10 MB.`));
    if (f && !/^image\/(jpeg|png|webp)$/.test(f.type))
      return setFileError(
        new Error("Send a photo (JPEG, PNG or WebP) or a voice note."),
      );
    if (f) setFile(f);
  };
  const addEmoji = (c: string) => {
    const el = textBox.current;
    const start = el?.selectionStart ?? text.length;
    const r = insertAt(text, start, el?.selectionEnd ?? start, c);
    setText(r.text);
    mentions.update(r.text, r.caret);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(r.caret, r.caret);
    });
  };

  return (
    <section
      className="chat-pane"
      aria-label="Conversation with Taskiem"
      data-testid="taskiem-pane"
      onDragEnter={(e) => {
        if (e.dataTransfer.types.includes("Files")) {
          e.preventDefault();
          setDragging(true);
        }
      }}
      onDragOver={(e) => {
        if (e.dataTransfer.types.includes("Files")) e.preventDefault();
      }}
      onDragLeave={(e) => {
        if (!e.currentTarget.contains(e.relatedTarget as Node | null))
          setDragging(false);
      }}
      onDrop={(e) => {
        e.preventDefault();
        setDragging(false);
        choose(e.dataTransfer.files[0]);
      }}
    >
      {dragging && (
        <div className="chat-drop" aria-hidden="true">
          Drop a photo to send it
        </div>
      )}
      <div className="chat-head">
        <button
          type="button"
          className="icon-button chat-back"
          onClick={onBack}
          aria-label="Back to the chats"
        >
          <Icon name="back" />
        </button>
        <span className="chat-avatar" aria-hidden="true">
          T
        </span>
        <h2>Taskiem</h2>
      </div>
      <p className="hint" style={{ padding: "8px 14px 0", margin: 0 }}>
        The same conversation you have with Taskiem on your phone: say{" "}
        <strong>help</strong> to see what it can do.
      </p>
      <ErrorBox error={error ?? act.error ?? fileError} />
      <div className="chat-stream-wrap">
        <div
          className="chat-stream"
          aria-live="polite"
          data-testid="taskiem-stream"
          role="log"
          tabIndex={0}
          aria-label="Chat history"
          ref={thread.stream}
          onScroll={thread.onScroll}
        >
          {hasOlder && messages && messages.length > 0 && (
            <div
              ref={thread.topMark}
              className="chat-top"
              data-testid="taskiem-top"
              aria-hidden="true"
            />
          )}
          {!messages ? (
            <Skeleton rows={3} label="Loading the conversation…" />
          ) : messages.length === 0 ? (
            <p className="hint" style={{ alignSelf: "center" }}>
              Nothing yet. Say <strong>status</strong> to see how the business
              is doing.
            </p>
          ) : (
            groups(messages).map((g) => (
              <div key={g.day} className="chat-group">
                <span className="chat-day">{g.day}</span>
                {g.messages.map((m, i) => {
                  const pressable = live === `${m.id}@${m.created_at}`;
                  // The time sits in the corner of the last line when there
                  // are no buttons and it came from here.
                  const inline =
                    m.channel === "web" && !(m.buttons && m.buttons.length > 0);
                  return (
                    <div
                      key={`${m.id}-${i}`}
                      className={`bubble${m.inbound ? " mine" : ""}${i === 0 || g.messages[i - 1]?.inbound !== m.inbound ? " first" : ""}`}
                      data-msg={m.id}
                      data-testid="taskiem-message"
                      data-mine={m.inbound}
                    >
                      {!m.inbound &&
                        (i === 0 || g.messages[i - 1]?.inbound) && (
                          <span className="who">Taskiem</span>
                        )}
                      <span className="text">
                        {boldParts(m.text).map((p, k) =>
                          p.bold ? <strong key={k}>{p.text}</strong> : p.text,
                        )}
                        {inline && (
                          <span className="stamp-gap" aria-hidden="true" />
                        )}
                      </span>
                      {m.buttons && m.buttons.length > 0 && (
                        <span className="row-actions" style={{ marginTop: 6 }}>
                          {m.buttons.map((b, j) =>
                            pressable && b.id ? (
                              <button
                                key={j}
                                type="button"
                                className="small"
                                disabled={act.busy}
                                onClick={() =>
                                  say({ reply: b.id, text: b.title })
                                }
                              >
                                {b.title}
                              </button>
                            ) : (
                              <span key={j} className="chip">
                                {b.title}
                              </span>
                            ),
                          )}
                        </span>
                      )}
                      <span className={`stamp${inline ? " inline" : ""}`}>
                        {m.channel !== "web" && (
                          <span>on {CHANNEL[m.channel] ?? m.channel} ·</span>
                        )}
                        <time
                          dateTime={m.created_at}
                          title={fmtTime(m.created_at)}
                        >
                          {clock(m.created_at)}
                        </time>
                      </span>
                    </div>
                  );
                })}
              </div>
            ))
          )}
        </div>
        {thread.away && (
          <ThreadJump count={thread.newCount} onClick={thread.toBottom} />
        )}
      </div>
      {file && (
        <div className="composer-reply" data-testid="taskiem-attaching">
          <span className="quote">
            {file.type.startsWith("audio/")
              ? "Voice note"
              : `Photo: ${file.name}`}
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
        accept="image/jpeg,image/png,image/webp"
        hidden
        data-testid="taskiem-file"
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
            onClick={() => setEmojiOpen(!emojiOpen)}
          >
            <Icon name="emoji" />
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label="Send a photo"
            title="Send a photo: with its name and price as the caption, it is added as a product"
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
          <label className="sr-only" htmlFor="taskiem-text">
            Message to Taskiem
          </label>
          <textarea
            id="taskiem-text"
            ref={textBox}
            rows={1}
            maxLength={4000}
            placeholder={file ? "Add a caption" : "Type a message"}
            value={text}
            onChange={(e) => {
              setText(e.target.value);
              mentions.update(e.target.value, e.target.selectionStart);
            }}
            onPaste={(e) => {
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
                if (emojiOpen) setEmojiOpen(false);
                else if (file) setFile(undefined);
                return;
              }
              if (e.key === "Enter" && !e.shiftKey) {
                e.preventDefault();
                send();
              }
            }}
          />
          {!text.trim() && !file && voice.can ? (
            <MicButton voice={voice} onStart={() => setFileError(undefined)} />
          ) : (
            <button
              type="submit"
              className="primary"
              disabled={act.busy || (!text.trim() && !file)}
            >
              Send
            </button>
          )}
        </form>
      )}
    </section>
  );
}

function groups(
  list: TaskiemMessage[],
): { day: string; messages: TaskiemMessage[] }[] {
  const out: { day: string; messages: TaskiemMessage[] }[] = [];
  for (const m of list) {
    const day = dayLabel(m.created_at);
    const g = out[out.length - 1];
    if (g && g.day === day) g.messages.push(m);
    else out.push({ day, messages: [m] });
  }
  return out;
}
