import { useCallback, useEffect, useRef, useState } from "react";
import { get, post, upload } from "../api";
import { Icon } from "../icons";
import { MicButton, RecordingBar, useVoiceNote } from "./VoiceNote";
import { clock, dayLabel } from "../lib/linkchat";
import {
  boldParts,
  mergeTaskiem,
  type TaskiemMessage,
} from "../lib/taskiemchat";
import { ErrorBox, fmtTime, Skeleton, useAction } from "../ui";

// The Taskiem conversation on the web (business links L3, §5.5): the
// person's own conversation with Taskiem, the one they have on WhatsApp,
// with the same words (status, orders, cash in, invoice …) and the same
// history from every channel. Their messages on the right, Taskiem's on
// the left; the buttons of Taskiem's newest answer press as on the phone.

const EVERY_MS = 5000;
const CHANNEL: Record<string, string> = {
  whatsapp: "WhatsApp",
  telegram: "Telegram",
  slack: "Slack",
};

export function TaskiemPane({ onBack }: { onBack: () => void }) {
  const [messages, setMessages] = useState<TaskiemMessage[]>();
  const [live, setLive] = useState<string>(); // the answer whose buttons press
  const [error, setError] = useState<unknown>();
  const [text, setText] = useState("");
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
  const end = useRef<HTMLDivElement>(null);
  const last = messages?.[messages.length - 1]?.created_at;

  const pull = useCallback(async (after?: string) => {
    const out = await get<{ messages: TaskiemMessage[] }>(
      `/v1/taskiem-chat${after ? `?after=${encodeURIComponent(after)}` : ""}`,
    );
    if (after && out.messages.length === 0) return;
    setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
  }, []);
  useEffect(() => {
    pull().catch(setError);
  }, [pull]);
  useEffect(() => {
    const t = setInterval(() => {
      if (document.visibilityState === "visible")
        pull(last).catch(() => undefined);
    }, EVERY_MS);
    return () => clearInterval(t);
  }, [pull, last]);
  useEffect(() => {
    end.current?.scrollIntoView?.({ block: "end" });
  }, [messages?.length]);

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
        if (body.text !== undefined && !body.reply) setText("");
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
    if (f) setFile(f);
  };

  return (
    <section
      className="chat-pane"
      aria-label="Conversation with Taskiem"
      data-testid="taskiem-pane"
    >
      <div className="chat-head">
        <button type="button" className="small chat-back" onClick={onBack}>
          Back<span className="sr-only"> to the chats</span>
        </button>
        <h2>Taskiem</h2>
      </div>
      <p className="hint" style={{ padding: "8px 14px 0", margin: 0 }}>
        The same conversation you have with Taskiem on your phone: say{" "}
        <strong>help</strong> to see what it can do.
      </p>
      <ErrorBox error={error ?? act.error ?? fileError} />
      <div
        className="chat-stream"
        aria-live="polite"
        data-testid="taskiem-stream"
      >
        {!messages ? (
          <Skeleton rows={3} label="Loading the conversation…" />
        ) : messages.length === 0 ? (
          <p className="hint" style={{ alignSelf: "center" }}>
            Nothing yet. Say <strong>status</strong> to see how the business is
            doing.
          </p>
        ) : (
          groups(messages).map((g) => (
            <div key={g.day} style={{ display: "contents" }}>
              <span className="chat-day">{g.day}</span>
              {g.messages.map((m, i) => {
                const pressable = live === `${m.id}@${m.created_at}`;
                return (
                  <div
                    key={`${m.id}-${i}`}
                    className={`bubble${m.inbound ? " mine" : ""}`}
                    data-testid="taskiem-message"
                    data-mine={m.inbound}
                  >
                    {!m.inbound && <span className="who">Taskiem</span>}
                    <span className="text">
                      {boldParts(m.text).map((p, k) =>
                        p.bold ? <strong key={k}>{p.text}</strong> : p.text,
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
                    <span className="meta">
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
        <div ref={end} />
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
