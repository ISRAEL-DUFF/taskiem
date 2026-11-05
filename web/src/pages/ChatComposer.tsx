import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type DragEvent,
  type KeyboardEvent,
} from "react";
import { Icon } from "../icons";
import { insertAt } from "../lib/emoji";
import { isHeic, preparePhoto } from "../lib/photo";
import { useComposerFocus } from "../lib/composerFocus";
import { getDraft, setDraft } from "../lib/drafts";
import { useAutoGrow } from "../lib/useAutoGrow";
import { MentionList, useMentions, type Mentions } from "./Mentions";
import { MicButton, RecordingBar, useVoiceNote } from "./VoiceNote";
import { PendingFile } from "./PendingFile";
import { AttachMenu } from "./AttachMenu";
import { CameraSheet } from "./CameraSheet";

// The composer of every conversation (a business's and Taskiem's): the box
// that grows, emoji, a photo (and, where it is taken, a PDF), the camera,
// a voice note, "@" and "#", a pasted or dropped picture, a reply, a draft
// kept per conversation, Esc one layer at a time, and the cursor going back.
// What differs between conversations is what they are given: where a draft
// is kept, what may be attached, what is done with what is sent.

// The emoji list is large: it is loaded when the panel is first opened.
const EmojiPanel = lazy(() =>
  import("./EmojiPanel").then((m) => ({ default: m.EmojiPanel })),
);

/** What may be attached, and what is said when it may not. */
export type FileRules = {
  accept: string;
  max: number;
  maxWords: string;
  types: RegExp;
  typesMessage: string;
  /** The clip's menu offers a document. */
  documents: boolean;
  dropWords: string;
  manyWords: string;
};

export const BUSINESS_FILES: FileRules = {
  accept: "image/jpeg,image/png,image/webp,application/pdf",
  max: 25 * 1024 * 1024,
  maxWords: "25 MB",
  types: /^(image\/(jpeg|png|webp)|application\/pdf|audio\/)/,
  typesMessage: "Send a photo (JPEG, PNG or WebP), a PDF or a voice note.",
  documents: true,
  dropWords: "Drop a photo or a PDF to send it",
  manyWords:
    "Only one file can be sent at a time: the first was taken. Send the others after it.",
};

export const TASKIEM_FILES: FileRules = {
  accept: "image/jpeg,image/png,image/webp",
  max: 10 * 1024 * 1024,
  maxWords: "10 MB",
  types: /^(image\/(jpeg|png|webp)|audio\/)/,
  typesMessage: "Send a photo (JPEG, PNG or WebP) or a voice note.",
  documents: false,
  dropWords: "Drop a photo to send it",
  manyWords:
    "Only one photo can be sent at a time: the first was taken. Send the others after it.",
};

export type Sending<R> = {
  text: string;
  file?: File;
  reply?: R;
  mentions?: ReturnType<Mentions["refs"]>;
  /** A voice note held and let go: it goes at once, and the box is left. */
  held: boolean;
};

export type ComposerOptions<R> = {
  /** Where the unsent words are kept (and shown in the list). */
  draftKey: string;
  sources: Parameters<typeof useMentions>[3];
  rules: FileRules;
  /** What happens to what is sent (the box is already emptied). */
  onSend: (s: Sending<R>) => void;
  /** The words changed: someone may be told we are typing. */
  onType?: (v: string) => void;
  /** What a reply being written is quoting, in a line. */
  replyLine?: (r: R) => string;
  /** More things that, closing, give the cursor back (a sheet, a panel). */
  closes?: boolean[];
  /** Drops and the box are off (a conversation one may only read). */
  enabled?: boolean;
};

export function useComposer<R>(o: ComposerOptions<R>) {
  const enabled = o.enabled ?? true;
  const [text, setText] = useState(() => getDraft(o.draftKey));
  const textBox = useRef<HTMLTextAreaElement>(null);
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [cameraOpen, setCameraOpen] = useState(false);
  const [dragging, setDragging] = useState(false);
  const [reply, setReply] = useState<R>();
  const [file, setFile] = useState<File>();
  const [fileError, setFileError] = useState<unknown>();
  const pick = useRef<HTMLInputElement>(null);
  const snap = useRef<HTMLInputElement>(null);
  useAutoGrow(textBox, text);
  // An unsent message is kept per conversation.
  useEffect(() => {
    setDraft(o.draftKey, text);
    // Only a change of the words is kept: a switch of conversation loads one.
  }, [text]);
  const mentions = useMentions(text, setText, textBox, o.sources);
  // A voice note held and released goes at once; one tapped waits for Send.
  const sendRef = useRef<(f?: File) => void>(() => undefined);
  const voice = useVoiceNote(
    (f, now) => (now ? sendRef.current(f) : setFile(f)),
    setFileError,
  );
  const typed = (v: string) => {
    setText(v);
    o.onType?.(v);
  };
  // The cursor goes back to the box: after sending, and when a panel, a
  // sheet, a waiting photo or a reply is closed; a reply to write or a
  // photo to caption asks for it.
  useComposerFocus(
    textBox,
    [emojiOpen, !!file, !!reply, voice.secs !== undefined, ...(o.closes ?? [])],
    [!!reply, !!file],
  );
  const send = (held?: File) => {
    const f = held ?? file;
    const body = held ? "" : text.trim();
    if (!body && !f) return;
    requestAnimationFrame(() => textBox.current?.focus());
    const s: Sending<R> = {
      text: body,
      file: f,
      reply,
      mentions: f ? undefined : mentions.refs(),
      held: !!held,
    };
    if (!held) {
      setText("");
      setFile(undefined);
      mentions.reset();
    }
    setReply(undefined);
    o.onSend(s);
  };
  sendRef.current = send;
  const choose = async (picked: File | undefined) => {
    setFileError(undefined);
    let f = picked;
    if (!f) return;
    if (isHeic(f)) {
      // An iPhone's photo: a JPEG where the browser can read it.
      try {
        f = await preparePhoto(f);
      } catch (e) {
        setFileError(e);
        return;
      }
    }
    if (f.size > o.rules.max) {
      setFileError(new Error(`${f.name} is larger than ${o.rules.maxWords}.`));
      return;
    }
    if (!o.rules.types.test(f.type)) {
      setFileError(new Error(o.rules.typesMessage));
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
    mentions.update(r.text, r.caret);
    requestAnimationFrame(() => {
      el?.focus();
      el?.setSelectionRange(r.caret, r.caret);
    });
  };
  /** Esc gives up what is being prepared from anywhere in the pane that is
   * not an open menu, sheet or panel (they close themselves): the emoji
   * panel, then a waiting file, then a reply. */
  const onPaneKeyDown = (e: KeyboardEvent) => {
    if (e.key !== "Escape" || e.defaultPrevented) return;
    if (
      e.currentTarget.querySelector(
        '[data-testid="chat-more"], .chat-menu, .attach-menu, .chat-info, .modal',
      )
    )
      return;
    const gave = emojiOpen || !!file || !!reply;
    if (emojiOpen) setEmojiOpen(false);
    else if (file) setFile(undefined);
    else if (reply) setReply(undefined);
    if (gave) e.preventDefault();
  };
  const dropProps = {
    onDragEnter: (e: DragEvent) => {
      if (enabled && e.dataTransfer.types.includes("Files")) {
        e.preventDefault();
        setDragging(true);
      }
    },
    onDragOver: (e: DragEvent) => {
      if (enabled && e.dataTransfer.types.includes("Files")) e.preventDefault();
    },
    onDragLeave: (e: DragEvent) => {
      if (!e.currentTarget.contains(e.relatedTarget as Node | null))
        setDragging(false);
    },
    onDrop: (e: DragEvent) => {
      if (!enabled) return;
      e.preventDefault();
      setDragging(false);
      void choose(e.dataTransfer.files[0]);
      // One file goes at a time, with its caption: say so.
      if (e.dataTransfer.files.length > 1)
        setFileError(new Error(o.rules.manyWords));
    },
  };
  return {
    o,
    text,
    setText,
    typed,
    textBox,
    emojiOpen,
    setEmojiOpen,
    cameraOpen,
    setCameraOpen,
    dragging,
    reply,
    setReply,
    file,
    setFile,
    fileError,
    setFileError,
    pick,
    snap,
    mentions,
    voice,
    send,
    choose,
    attach,
    addEmoji,
    onPaneKeyDown,
    dropProps,
  };
}

export type Composer<R = never> = ReturnType<typeof useComposer<R>>;

/** The composer as it looks, from reply banner to Send. `names` are the
 * ids the conversation's tests and labels know it by. */
export function ComposerView<R>({
  c,
  busy,
  prefix,
  textId,
  textLabel,
}: {
  c: ReturnType<typeof useComposer<R>>;
  busy: boolean;
  /** "chat" or "taskiem": the start of its test ids. */
  prefix: string;
  textId: string;
  textLabel: string;
}) {
  const { voice, file, text, reply } = c;
  return (
    <>
      {reply && c.o.replyLine && (
        <div
          className="composer-reply"
          data-testid={`${prefix}-replying`}
          onKeyDown={(e) => {
            if (e.key === "Escape") {
              e.preventDefault();
              c.setReply(undefined);
            }
          }}
        >
          <span className="quote">Replying to {c.o.replyLine(reply)}</span>
          <button
            type="button"
            className="small"
            onClick={() => c.setReply(undefined)}
          >
            Cancel<span className="sr-only"> the reply</span>
          </button>
        </div>
      )}
      {file && (
        <PendingFile
          file={file}
          testid={`${prefix}-attaching`}
          onRemove={() => c.setFile(undefined)}
        />
      )}
      <input
        ref={c.pick}
        type="file"
        accept={c.o.rules.accept}
        hidden
        data-testid={`${prefix}-file`}
        onChange={(e) => {
          void c.choose(e.target.files?.[0]);
          e.target.value = "";
        }}
      />
      <input
        ref={c.snap}
        type="file"
        accept="image/jpeg,image/png,image/webp"
        capture="environment"
        hidden
        onChange={(e) => {
          void c.choose(e.target.files?.[0]);
          e.target.value = "";
        }}
      />
      {c.emojiOpen && (
        <Suspense fallback={<div className="emoji-panel" aria-busy="true" />}>
          <EmojiPanel
            onPick={c.addEmoji}
            onClose={() => c.setEmojiOpen(false)}
          />
        </Suspense>
      )}
      <MentionList m={c.mentions} />
      {voice.secs !== undefined || voice.holding ? (
        <RecordingBar voice={voice} />
      ) : (
        <form
          className="composer"
          onSubmit={(e) => {
            e.preventDefault();
            c.send();
          }}
        >
          <div className="composer-pill">
            <button
              type="button"
              className="icon-button"
              aria-label="Emoji"
              title="Emoji"
              aria-expanded={c.emojiOpen}
              data-testid="emoji-button"
              onClick={() => c.setEmojiOpen(!c.emojiOpen)}
            >
              <Icon name="emoji" />
            </button>
            <label className="sr-only" htmlFor={textId}>
              {textLabel}
            </label>
            <textarea
              id={textId}
              ref={c.textBox}
              rows={1}
              maxLength={4000}
              placeholder={file ? "Add a caption" : "Type a message"}
              value={text}
              onChange={(e) => {
                c.typed(e.target.value);
                c.mentions.update(e.target.value, e.target.selectionStart);
              }}
              onPaste={(e) => {
                // A pasted picture is a photo to send, with a caption.
                const f = Array.from(e.clipboardData.files).find((x) =>
                  x.type.startsWith("image/"),
                );
                if (f) {
                  e.preventDefault();
                  void c.choose(f);
                }
              }}
              onKeyDown={(e) => {
                if (c.mentions.onKeyDown(e)) return;
                if (e.key === "Escape") {
                  // Esc gives up what is being prepared: the panel, a photo
                  // waiting for its caption, then a reply.
                  if (c.emojiOpen || file || reply) e.preventDefault();
                  if (c.emojiOpen) c.setEmojiOpen(false);
                  else if (file) c.setFile(undefined);
                  else if (reply) c.setReply(undefined);
                  return;
                }
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  c.send();
                }
              }}
            />
            <AttachMenu onChoose={c.attach} documents={c.o.rules.documents} />
            {!text.trim() && !file && (
              <button
                type="button"
                className="icon-button camera-quick"
                aria-label="Take a photo"
                title="Take a photo"
                data-testid="camera-quick"
                onClick={() => c.attach("camera")}
              >
                <Icon name="camera" />
              </button>
            )}
          </div>
          {!text.trim() && !file && voice.can ? (
            <MicButton
              voice={voice}
              onStart={() => c.setFileError(undefined)}
            />
          ) : (
            <button
              type="submit"
              className="primary icon-button send-button"
              disabled={busy || (!text.trim() && !file)}
            >
              <Icon name="send" />
              <span className="sr-only">
                {busy && file ? "Sending…" : "Send"}
              </span>
            </button>
          )}
        </form>
      )}
      {c.dragging && (
        <div
          className="chat-drop"
          data-testid={`${prefix}-drop`}
          aria-hidden="true"
        >
          {c.o.rules.dropWords}
        </div>
      )}
      {c.cameraOpen && (
        <CameraSheet
          onClose={() => c.setCameraOpen(false)}
          onTake={(f) => {
            c.setCameraOpen(false);
            void c.choose(f);
          }}
        />
      )}
    </>
  );
}
