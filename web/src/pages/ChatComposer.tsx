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
import { toast } from "../toast";
import { insertAt } from "../lib/emoji";
import { isHeic, preparePhoto } from "../lib/photo";
import { useComposerFocus } from "../lib/composerFocus";
import { getDraft, setDraft } from "../lib/drafts";
import { useAutoGrow } from "../lib/useAutoGrow";
import {
  GroupedPicker,
  MentionList,
  useMentions,
  type Mentions,
} from "./Mentions";
import { MicButton, RecordingBar, useVoiceNote } from "./VoiceNote";
import { PendingFile } from "./PendingFile";
import { AttachMenu } from "./AttachMenu";
import { CameraSheet } from "./CameraSheet";
import { ToolSheet, ToolsPanel } from "./ChatTools";
import { OrderSheet, type OrderStart } from "./ChatBuyer";
import { HandoverSheet, SellSheet } from "./ChatStaff";
import { toolsFor, type ToolKey } from "../lib/tools";
import type { Prefill, Request } from "../lib/intents";
import { RequestChip } from "./RequestChip";

type ToolStart = {
  rowId?: string;
  add?: boolean;
  prefill?: Prefill;
  /** A chip already on: a category picked in "@". */
  chip?: string;
  /** The Order sheet: an item in the bag, or a past order again. */
  order?: OrderStart;
};
import { useAuth } from "../auth";
import { categoryTag } from "../lib/tools";
import { useMediaQuery, usePresence } from "../kit";

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
  /** Chat tools (chat-tools.md §3): "@" tags things and businesses in
   * groups (with the Taskiem row when taskiem). */
  grouped?: { taskiem: boolean };
  /** The box's words when empty ("Ask Taskiem, or @ to tag"). */
  placeholder?: string;
  /** Chat tools: the tools button, and "/". */
  tools?: boolean;
  /** Where the tools are open: "taskiem" or a link. */
  diaryWhere?: string;
  /** A tool saved a change: what is newer is shown. */
  onDiary?: (line: unknown) => void;
  /** A business's conversation: its orders and bills only, and, when they
   * share prices with us, ordering from them. */
  scope?: { link: string; name: string; supplies: boolean };
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
    // "/pro" opening the tools is not a message to keep.
    setDraft(o.draftKey, o.tools && /^\/\S*$/.test(text) ? "" : text);
    // Only a change of the words is kept: a switch of conversation loads one.
  }, [text]);
  // A category picked in "@" opens its items (set below, once the tools are).
  const onCategory = useRef<(name: string) => void>(() => undefined);
  const mentions = useMentions(
    text,
    setText,
    textBox,
    o.sources,
    o.grouped && {
      ...o.grouped,
      onCategory: (name) => onCategory.current(name),
    },
  );
  // The tools panel: the grid button, or "/" and a word as the whole message.
  const [toolsOpen, setToolsOpen] = useState(false);
  const [tool, setTool] = useState<{
    key: ToolKey;
    from?: DOMRect;
    start?: ToolStart;
  }>();
  const { can, me } = useAuth();
  const slash = o.tools ? /^\/(\S*)$/.exec(text) : null;
  const toolsShown = !!o.tools && (toolsOpen || !!slash);
  // How many tools what follows "/" names: none, and Enter sends it.
  const toolMatches = toolsShown ? toolsFor(can, slash?.[1] ?? "", me?.roles).length : 0;
  const closeTools = () => {
    setToolsOpen(false);
    if (slash) setText("");
  };
  const openTool = (k: ToolKey, from?: DOMRect, start?: ToolStart) => {
    closeTools();
    setTool({ key: k, from, start });
  };
  // A supplier's category opens their list, to order from; ours, our
  // products.
  onCategory.current = (name) =>
    o.scope?.supplies
      ? openTool("order", undefined, { order: { category: name } })
      : openTool("products", undefined, { chip: categoryTag(name) });
  // A typed request: its tool opens at the form, filled in; the words go.
  const openRequest = (r: Request) => {
    setText("");
    openTool(r.tool, undefined, {
      rowId: r.rowId,
      add: r.add,
      prefill: r.prefill,
    });
  };
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
    [
      emojiOpen,
      !!file,
      !!reply,
      voice.secs !== undefined,
      !!tool,
      ...(o.closes ?? []),
    ],
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
    // Back to the message after the render, unless the person has gone on
    // somewhere (the search box, to find the next one) in the meantime.
    const from = document.activeElement;
    requestAnimationFrame(() => {
      const now = document.activeElement;
      if (now !== from && now !== document.body && now !== el) return;
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
    toolsShown,
    toolsQuery: slash?.[1] ?? "",
    setToolsOpen,
    closeTools,
    toolMatches,
    openRequest,
    tool,
    setTool,
    openTool,
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
  const toolsP = usePresence(c.toolsShown);
  const roomy = useMediaQuery("(min-width: 600px)");
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
      {c.tool?.key === "order" && c.o.scope && (
        <OrderSheet
          link={c.o.scope.link}
          business={c.o.scope.name}
          from={c.tool.from}
          start={c.tool.start?.order}
          onClose={() => c.setTool(undefined)}
          onPlaced={() => c.o.onDiary?.(undefined)}
        />
      )}
      {c.tool?.key === "sell" && (
        <SellSheet
          from={c.tool.from}
          onClose={() => c.setTool(undefined)}
          onSold={() => c.o.onDiary?.(undefined)}
        />
      )}
      {c.tool?.key === "handover" && (
        <HandoverSheet
          from={c.tool.from}
          onClose={() => c.setTool(undefined)}
          onSent={() => c.o.onDiary?.(undefined)}
        />
      )}
      {c.tool && !["order", "sell", "handover"].includes(c.tool.key) && (
        <ToolSheet
          tool={c.tool.key}
          from={c.tool.from}
          start={c.tool.start}
          scope={c.o.scope?.link}
          onReorder={
            c.o.scope?.supplies
              ? (p) => c.openTool("order", undefined, { order: { last: p } })
              : undefined
          }
          onNewOrder={
            c.o.scope?.supplies ? () => c.openTool("order") : undefined
          }
          onClose={() => c.setTool(undefined)}
          onSaved={() => {
            // The server tells the change as a diary line in the person's
            // Taskiem conversation (chat_changes.go): shown there at once;
            // from a business's conversation, said where it went.
            c.o.onDiary?.(undefined);
            if (c.o.diaryWhere && c.o.diaryWhere !== "taskiem")
              toast("Saved. The change is in your Taskiem chat.", "success", {
                href: "/chat?with=taskiem",
                linkText: "Open",
              });
          }}
        />
      )}
      {voice.secs !== undefined || voice.holding ? (
        <RecordingBar voice={voice} />
      ) : (
        <form
          className={`composer${c.o.tools ? " tools-on" : ""}`}
          onSubmit={(e) => {
            e.preventDefault();
            c.send();
          }}
        >
          <GroupedPicker m={c.mentions} />
          {c.o.tools && !c.toolsShown && !c.mentions.q && (
            <RequestChip text={c.text} onOpen={c.openRequest} />
          )}
          {toolsP.mounted && (
            <ToolsPanel
              query={c.toolsQuery}
              onOpen={c.openTool}
              onClose={c.closeTools}
              leaving={toolsP.leaving}
              scope={c.o.scope?.link}
              supplier={c.o.scope?.supplies ? c.o.scope.name : undefined}
            />
          )}
          {c.o.tools && (
            <button
              type="button"
              className={`composer-tools${c.toolsShown ? " open" : ""}`}
              aria-label={c.toolsShown ? "Close tools" : "Open tools"}
              aria-expanded={c.toolsShown}
              data-testid="tools-button"
              onClick={() =>
                c.toolsShown ? c.closeTools() : c.setToolsOpen(true)
              }
            >
              <Icon name="grid" />
            </button>
          )}
          <div className="composer-pill">
            {/* The designs have no emoji button: keyboards have emoji. */}
            {!c.o.tools && (
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
            )}
            <label className="sr-only" htmlFor={textId}>
              {textLabel}
            </label>
            <textarea
              id={textId}
              ref={c.textBox}
              data-chat-box=""
              rows={1}
              maxLength={4000}
              placeholder={
                file
                  ? "Add a caption"
                  : hint(
                      c.o.placeholder
                        ? c.o.placeholder
                        : c.o.grouped
                          ? "Message, @ to tag"
                          : "Type a message",
                      roomy,
                    )
              }
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
                if (
                  c.toolsShown &&
                  (e.key === "Escape" ||
                    ((e.key === "Enter" || e.key === "ArrowUp") &&
                      c.toolMatches > 0))
                ) {
                  // The tools panel takes the keys: Esc closes it, Enter
                  // or the up arrow goes to its first tile. With no tool
                  // named, Enter sends the words as they are.
                  e.preventDefault();
                  if (e.key === "Escape") c.closeTools();
                  else
                    (
                      document.querySelector(
                        ".tk-tools .tk-tile",
                      ) as HTMLButtonElement | null
                    )?.focus();
                  return;
                }
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
            {!file && (
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
              <Icon name="arrow-up" />
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

/** The box's hint on one line: on a phone, the words before its comma
 * ("Ask Taskiem", "Message"); with room, all of it. */
export function hint(words: string, roomy: boolean): string {
  return roomy ? words : (words.split(",")[0] ?? words).trim();
}
