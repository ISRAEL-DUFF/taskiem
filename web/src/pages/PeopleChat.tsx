import { useCallback, useEffect, useRef, useState } from "react";
import { useSavedPicks } from "./SavedPicks";
import { sendSaved } from "./SavedCard";
import { SaveAsItem, useSaveAs } from "./SaveAs";
import type { Suggestion } from "../lib/mentions";
import { SavedCardView } from "./SavedCard";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { ApiError, del, get, post, put, upload } from "../api";
import { Icon } from "../icons";
import { toast } from "../toast";
import { KitField, Pill, Sheet } from "../kit";
import { getDraft } from "../lib/drafts";
import { naira, toKobo } from "../lib/invoices";
import { clock, REACTIONS } from "../lib/linkchat";
import {
  accountDigits,
  canSendTo,
  cardKind,
  closedWords,
  forwardCap,
  forwardSaid,
  forwardWords,
  headWords,
  KEPT_WORDS,
  lastSeenWords,
  mayDeleteForEveryone,
  mergePeople,
  messageLine,
  peopleFileURL,
  PEOPLE_TRANSFER_WARNING,
  phoneWords,
  photoOf,
  priceNowWords,
  REPORT_CONSENT,
  SCAM_WARNING,
  sendProblem,
  sharedChatPath,
  splitReady,
  splitTitle,
  stockWords,
  tickState,
  TICK_WORDS,
  withLatestPeople,
  type CardAsk,
  type ChatList,
  type OpenedCard,
  type PayoutAccount,
  type PeopleCard,
  type PeopleChat,
  type PeopleChatList,
  type PeopleMessage,
} from "../lib/people";
import { clientFor, mayHaveArrived, REPORT_REASONS, type KeptSend, type ShopItem } from "../lib/personchat";
import { usePagedList } from "../lib/usePagedList";
import { AT_FIND_WORDS, atFindState, chatWithPresence, typingWords, checkPlace, FIND_PAGES, type AtFind, mapURL, placeFigures, placeProblem, threadWithGone, threadWithLive, TypingGate, type Place } from "../lib/peoplelive";
import { livePoll, usePeopleLive } from "./PeopleLive";
import { useThread } from "../lib/useThread";
import type { Group } from "../lib/groups";
import type { PersonOrderDetail, PersonOrders } from "../lib/youorders";
import { EmptyState, ErrorBox, fmtTime, Items, Modal, Row, Skeleton, useAction, useLoad } from "../ui";
import { AppAttachmentView } from "./AppCards";
import { BasketCardBody } from "./Basket";
import { BubbleShell, CopyItem, PopItem } from "./BubbleShell";
import { ChatAvatar } from "./ChatAvatar";
import { BUSINESS_FILES, ComposerView, useComposer } from "./ChatComposer";
import { ChatHead } from "./ChatHead";
import { ChatStream } from "./ChatStream";
import { useConfirm } from "./ConfirmSheet";
import { MoreRows } from "./MoreRows";
import { TellMe } from "./PersonChats";

// A chat with a person on You (docs/spec/people-chatting.md §7, §8, §10.1,
// §11): the messages with their ticks, replies, reactions, stars, forwards,
// deleting, photos and files, the warnings on strangers' messages, a
// request's Accept, Delete and Block and report, cards shared live and
// "Your share" cards. It looks and behaves as a chat with a business does.

const POLL_MS = 4000;

export const REACTION_NAMES: Record<string, string> = { "👍": "thumbs up", "❤️": "heart", "😂": "laughing", "😮": "surprise", "😢": "sad", "🙏": "thanks" };

const draftKey = (id: string) => `people:${id}`;

let localN = 0;

/** A photo as a data URL, for its preview while it is sent. */
export const asDataURL = (f: File) =>
  new Promise<string | undefined>((done) => {
    if (!f.type.startsWith("image/")) return done(undefined);
    const r = new FileReader();
    r.onload = () => done(typeof r.result === "string" ? r.result : undefined);
    r.onerror = () => done(undefined);
    r.readAsDataURL(f);
  });

/** The ticks under a message of ours: one until it is delivered, two once
 * it is, coloured only when the answer says they read it. */
export function PeopleTicks({ m, readTicks = true }: { m: Pick<PeopleMessage, "local" | "status">; readTicks?: boolean }) {
  const t = tickState(m, readTicks);
  if (t === "sending")
    return (
      <span className="ticks pending" role="img" aria-label="Sending">
        <Icon name="clock" />
      </span>
    );
  return (
    <span className={`ticks${t === "read" ? " read" : ""}`} role="img" aria-label={TICK_WORDS[t]} data-testid="people-ticks" data-ticks={t}>
      <Icon name={t === "sent" ? "tick" : "tick-double"} />
    </span>
  );
}

/** /you/people/:id and /you/people/requests/:id: one chat with a person. */
export function PeopleChatPage() {
  const { id = "" } = useParams();
  const nav = useNavigate();
  const loc = useLocation();
  // Opened at a message (a search's or the starred list's): the thread is
  // placed there, reading older pages until it is in.
  const at = new URLSearchParams(loc.search).get("at") ?? undefined;
  const base = `/v1/me/people/${encodeURIComponent(id)}`;
  const chat = useLoad(() => get<PeopleChat>(base), [base]);
  const live = usePeopleLive();
  // Online and last seen as the live connection last told.
  const c = chatWithPresence(chat.data, live.presence);
  // The message opened at, not found: no longer in the chat, or further back than was read.
  const [atMissing, setAtMissing] = useState<AtFind>();
  const [looking, setLooking] = useState(false);
  const [messages, setMessages] = useState<PeopleMessage[]>();
  const [hasOlder, setHasOlder] = useState(false);
  const [pollError, setPollError] = useState<unknown>();
  const [problem, setProblem] = useState<string>();
  const [sendError, setSendError] = useState<unknown>();
  const kept = useRef<KeptSend | undefined>(undefined);
  const [sheet, setSheet] = useState<
    | { k: "info" | "report" | "share" | "split" | "place"; blockToo?: boolean }
    | { k: "forward"; m: PeopleMessage }
    | { k: "card"; m: PeopleMessage }
    | { k: "delete"; m: PeopleMessage }
    | undefined
  >();
  const act = useAction();
  const sure = useConfirm();
  const loadingOlder = useRef(false);
  // The chat's first reading (and Keep looking) runs: polls wait.
  const loadingFirst = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const thread = useThread({ items: messages, isMine: (m) => m.mine, hasOlder, loadOlder: olderCb });

  /** Older pages read from `all` until `find` is in, at most FIND_PAGES. */
  const findOlder = useCallback(
    async (all: PeopleMessage[], more: boolean, find: string) => {
      for (let i = 0; i < FIND_PAGES && more && !all.some((m) => m.id === find); i++) {
        const oldest = all.find((m) => !m.local);
        if (!oldest) break;
        const older = await get<{ messages: PeopleMessage[]; more: boolean }>(`${base}/messages?before=${encodeURIComponent(oldest.id)}`);
        all = mergePeople(all, older.messages);
        more = older.more;
      }
      return { all, more };
    },
    [base],
  );
  const pull = useCallback(
    async (first: boolean, find?: string) => {
      // While the chat's first reading runs (reading older pages for a
      // message opened at), a poll or reconnection waits: it would put the
      // latest page in place of what is being read, and lose the jump.
      if (!first && loadingFirst.current) return;
      const out = await get<{ messages: PeopleMessage[]; more: boolean }>(`${base}/messages`);
      if (first && find && !out.messages.some((m) => m.id === find)) {
        // The message opened at is further up: older pages, until it is in.
        const { all, more } = await findOlder(out.messages, out.more, find);
        setAtMissing(atFindState(all.some((m) => m.id === find), more));
        setMessages(all);
        setHasOlder(more);
      } else {
        setMessages((cur) => (first ? out.messages : withLatestPeople(cur ?? [], out.messages, out.more)));
        if (first) setHasOlder(out.more);
        if (first && find) setAtMissing(undefined);
      }
      setPollError(undefined);
      // Read marks: a request's reading tells the sender nothing (§7).
      if (document.visibilityState === "visible" && out.messages.some((m) => !m.mine && !m.read_at)) await post(`${base}/read`).catch(() => undefined);
    },
    [base, findOlder],
  );
  const reloadChat = chat.reload;
  const firstRun = useRef(0);
  useEffect(() => {
    setMessages(undefined);
    setAtMissing(undefined);
    thread.reset(at);
    const run = ++firstRun.current;
    loadingFirst.current = true;
    pull(true, at)
      .catch(setPollError)
      .finally(() => {
        if (firstRun.current === run) loadingFirst.current = false;
      });
  }, [base, pull, at]);
  /** The message opened at is further back than was read: more pages. */
  const keepLooking = () => {
    if (!at || !messages || loadingFirst.current) return;
    const run = firstRun.current;
    loadingFirst.current = true;
    setLooking(true);
    findOlder(messages, hasOlder, at)
      .then(({ all, more }) => {
        if (firstRun.current !== run) return;
        const found = all.some((m) => m.id === at);
        setAtMissing(atFindState(found, more));
        if (found) thread.reset(at);
        else thread.keepPlace();
        setMessages((cur) => mergePeople(cur ?? [], all));
        setHasOlder(more);
      })
      .catch(setPollError)
      .finally(() => {
        if (firstRun.current === run) loadingFirst.current = false;
        setLooking(false);
      });
  };
  // Polling: as before while the live connection is not open, slowly while it is.
  const connected = live.connected;
  useEffect(() => {
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      pull(false).catch(setPollError);
      reloadChat();
    }, livePoll(connected, POLL_MS));
    return () => clearInterval(t);
  }, [pull, reloadChat, connected]);
  // Back to the tab: what came while it was hidden is read now (the read
  // mark goes with it), not at the next look, a minute away while live.
  useEffect(() => {
    const seen = () => {
      if (document.visibilityState === "visible") pull(false).catch(setPollError);
    };
    document.addEventListener("visibilitychange", seen);
    return () => document.removeEventListener("visibilitychange", seen);
  }, [pull]);
  // What the live connection tells: messages of this chat in place at once
  // (theirs read while the tab is seen), the chat's own changes, messages
  // deleted elsewhere, and, after a reconnection, what was missed.
  const readSoon = useRef<number>(undefined);
  const chatSoon = useRef<number>(undefined);
  useEffect(
    () =>
      live.subscribe((e) => {
        if (e.type === "message" && e.data.chat_id === id) {
          setMessages((cur) => threadWithLive(cur, id, e.data));
          if (!e.data.message.mine && !e.data.message.read_at && document.visibilityState === "visible") {
            window.clearTimeout(readSoon.current);
            readSoon.current = window.setTimeout(() => void post(`${base}/read`).catch(() => undefined), 300);
          }
        } else if (e.type === "gone" && e.data.chat_id === id) {
          if (e.data.id) setMessages((cur) => threadWithGone(cur, id, e.data));
          else nav("/you/people/requests", { replace: true });
        } else if (e.type === "chat" && e.data.id === id) {
          window.clearTimeout(chatSoon.current);
          chatSoon.current = window.setTimeout(reloadChat, 200);
        } else if (e.type === "ready") {
          pull(false).catch(setPollError);
          reloadChat();
        }
      }),
    [live.subscribe, id, base, pull, reloadChat, nav],
  );
  useEffect(
    () => () => {
      window.clearTimeout(readSoon.current);
      window.clearTimeout(chatSoon.current);
    },
    [],
  );
  // Typing: said while the words change (at most every 3 seconds), stopped
  // when the box empties or the chat is left.
  const gate = useRef(new TypingGate());
  const typed = (v: string) => {
    if (!c || c.request || c.waiting || c.blocked || c.gone || c.suspended) return;
    const say = gate.current.change(v, Date.now());
    if (say !== null) void post(`${base}/typing`, { typing: say }).catch(() => undefined);
  };
  useEffect(() => {
    const g = gate.current;
    return () => {
      if (g.leave()) void post(`${base}/typing`, { typing: false }).catch(() => undefined);
    };
  }, [base]);
  loadOlderRef.current = () => {
    const oldest = messages?.find((m) => !m.local);
    if (!oldest || loadingOlder.current || loadingFirst.current) return;
    loadingOlder.current = true;
    get<{ messages: PeopleMessage[]; more: boolean }>(`${base}/messages?before=${encodeURIComponent(oldest.id)}`)
      .then((out) => {
        thread.keepPlace();
        setMessages((cur) => mergePeople(cur ?? [], out.messages));
        setHasOlder(out.more);
      })
      .catch(setPollError)
      .finally(() => (loadingOlder.current = false));
  };

  const send = async (text: string, file?: File, reply?: PeopleMessage) => {
    setProblem(undefined);
    gate.current.sent();
    const client = file ? `${Date.now()}-${++localN}` : clientFor(kept.current, text, () => `${Date.now()}-${++localN}`);
    kept.current = undefined;
    const preview = file ? await asDataURL(file) : undefined;
    const mine: PeopleMessage = {
      id: `local-${client}`,
      mine: true,
      kind: file ? "media" : "text",
      body: text,
      reply_to: reply?.id,
      attachment: file ? { kind: file.type.startsWith("image/") ? "photo" : file.type.startsWith("audio/") ? "voice" : "file", type: file.type, size: file.size, name: file.name } : undefined,
      read_at: null,
      created_at: new Date().toISOString(),
      local: true,
      localPreview: preview,
    };
    setMessages((cur) => mergePeople(cur ?? [], [mine]));
    thread.toBottom?.();
    try {
      let out: PeopleMessage;
      if (file) {
        const form = new FormData();
        form.append("file", file);
        if (text) form.append("text", text);
        form.append("client_id", client);
        if (reply) form.append("reply_to", reply.id);
        out = await upload<PeopleMessage>(`${base}/files`, form);
      } else out = await post<PeopleMessage>(`${base}/messages`, { text, client_id: client, reply_to: reply?.id });
      setMessages((cur) => mergePeople((cur ?? []).filter((m) => m.id !== mine.id), [out]));
      setSendError(undefined);
      // The first message to someone (a request now waiting for them), or
      // an answer to a request: the chat says so at once, not at the next look.
      if (c?.request || c?.waiting || !c?.last) reloadChat();
    } catch (e) {
      setMessages((cur) => cur?.filter((m) => m.id !== mine.id));
      if (text && !file) {
        composer.setText((cur) => (cur === "" ? text : cur));
        if (e instanceof ApiError && mayHaveArrived(e.status)) kept.current = { text, client };
      }
      // Held by the checks, not taken, or too many: said in plain words.
      const said = sendProblem(e);
      if (said) setProblem(said);
      else setSendError(e);
      reloadChat();
    }
  };

  const shareCard = async (card: CardAsk) => {
    setProblem(undefined);
    try {
      const out = await post<PeopleMessage>(`${base}/messages`, { card, client_id: `${Date.now()}-${++localN}` });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
      thread.toBottom?.();
      if (c?.waiting || !c?.last) reloadChat();
      return true;
    } catch (e) {
      // Refused (held by the checks, not taken, too many): said above the
      // composer, and not "Shared" or "Sent".
      const said = sendProblem(e);
      if (!said) throw e;
      setProblem(said);
      reloadChat();
      return false;
    }
  };

  /** A place (§7): sent as it was seen in the preview, with its name. */
  const sharePlace = async (place: Place) => {
    setProblem(undefined);
    try {
      const out = await post<PeopleMessage>(`${base}/messages`, { location: place, client_id: `${Date.now()}-${++localN}` });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
      thread.toBottom?.();
      if (c?.waiting || !c?.last) reloadChat();
      return true;
    } catch (e) {
      const said = sendProblem(e);
      if (!said) throw e;
      setProblem(said);
      reloadChat();
      return false;
    }
  };

  const closed = closedWords(c);
  const savedTexts = useSavedPicks("share-mine");
  // Save as… on a message (saved objects S1, part 4): one's own.
  const saveAs = useSaveAs({ onSaved: () => savedTexts.reload() });
  // A saved thing other than a text, picked: shared as a card (saved
  // objects S1), after a yes to sharing what is sensitive in it.
  const shareSaved = (pick: Suggestion) =>
    void (async () => {
      setProblem(undefined);
      try {
        const out = await sendSaved(
          (confirm) => post<PeopleMessage>(`${base}/messages`, { card: { type: "saved", saved: { id: pick.id, owner: "mine", confirm } }, client_id: `${Date.now()}-${++localN}` }),
          (question) => sure.confirm({ title: question, ok: "Share" }),
        );
        if (!out) return;
        setMessages((cur) => mergePeople(cur ?? [], [out]));
        thread.toBottom?.();
      } catch (e) {
        const said = sendProblem(e);
        if (said) setProblem(said);
        else setSendError(e);
      }
    })();
  const composer = useComposer<PeopleMessage>({
    draftKey: draftKey(id),
    rules: BUSINESS_FILES,
    // Saved texts (saved objects S1): their words into the box.
    sources: { "@": [], "#": [], saved: savedTexts.saved, savedComplete: savedTexts.complete, wantSaved: savedTexts.want, shareSaved, saveNamed: (name) => saveAs.named(name) },
    replyLine: (m) => messageLine(m),
    // What a send ran into goes once the words are changed; the other
    // person is told we are typing.
    onType: (v) => {
      setSendError(undefined);
      typed(v);
    },
    onSend: (s) => {
      setSendError(undefined);
      void send(s.text, s.file, s.reply);
    },
    enabled: !!c && !c.blocked && !c.gone && !c.suspended && !c.request,
    closes: [!!sheet],
  });

  const name = c?.name ?? "this person";
  const change = (body: Record<string, boolean>, said: string) =>
    act.run(async () => {
      await put(base, body);
      reloadChat();
    }, said);
  const unblock = () =>
    act.run(async () => {
      await del(`${base}/block`);
      reloadChat();
    }, `${name} can write to you again`);
  const block = async () => {
    const yes = await sure.confirm({
      title: `Block ${name}?`,
      body: `${name} will not be able to write to you, or see when you were last here or your photo. They are not told.`,
      ok: "Block",
      danger: true,
    });
    if (!yes) return;
    await act.run(async () => {
      const r = await post<{ removed: boolean }>(`${base}/block`);
      if (r.removed) nav("/you/people/requests", { replace: true });
      else reloadChat();
    }, `Blocked ${name}`);
  };
  const deleteChat = async () => {
    const yes = await sure.confirm({ title: `Delete this chat?`, body: `Its messages go from your chats. ${name} keeps their own copy.`, ok: "Delete", danger: true });
    if (!yes) return;
    await act.run(async () => {
      await del(base);
      nav("/you", { replace: true });
    }, "Chat deleted");
  };
  const accept = () =>
    act.run(async () => {
      await post(`${base}/accept`);
      nav(`/you/people/${encodeURIComponent(id)}`, { replace: true });
      reloadChat();
      void pull(false);
    }, `You can chat with ${name} now`);
  const decline = async () => {
    const yes = await sure.confirm({ title: "Delete this request?", body: `${name} is not told.`, ok: "Delete", danger: true });
    if (!yes) return;
    await act.run(async () => {
      await post(`${base}/decline`);
      nav("/you/people/requests", { replace: true });
    }, "Request deleted");
  };
  const react = (m: PeopleMessage, emoji: string) =>
    act.run(async () => {
      const out = await put<PeopleMessage>(`${base}/messages/${encodeURIComponent(m.id)}`, { reaction: m.my_reaction === emoji ? "" : emoji });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
    }, false);
  const star = (m: PeopleMessage) =>
    act.run(async () => {
      const out = await put<PeopleMessage>(`${base}/messages/${encodeURIComponent(m.id)}`, { starred: !m.starred });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
    }, m.starred ? "Unstarred" : "Starred");
  const markPaid = (m: PeopleMessage, paid: boolean) =>
    act.run(async () => {
      const out = await post<PeopleMessage>(`${base}/messages/${encodeURIComponent(m.id)}/paid`, { paid });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
    }, paid ? "Marked as paid" : "Marked as not paid yet");

  const byID = new Map((messages ?? []).map((m) => [m.id, m]));
  const theyType = !!c && !c.request && live.typing.has(id);
  const sub = headWords(c);
  // Read ticks go between the two only while both have them on: the
  // server hides reads otherwise, and the client does not show one either.
  const readTicks = c?.person ? c.person.read_ticks : true;

  return (
    <section className="chat-pane person-pane" aria-label={`Chat with ${name}`} data-testid="people-pane" onKeyDown={composer.onPaneKeyDown} {...composer.dropProps}>
      <h1 className="sr-only">{c ? `Chat with ${c.name}` : "Chat"}</h1>
      {/* "typing…" in the head, said politely to a screen reader. */}
      <p className="sr-only" aria-live="polite" data-testid="people-typing-live">
        {typingWords(theyType ? [name] : [])}
      </p>
      <ChatHead
        onBack={() => nav(c?.request ? "/you/people/requests" : "/you")}
        menuLabel="More about this chat"
        avatar={<ChatAvatar name={name} logo={photoOf(c?.person)} />}
        title={c?.name ?? "Chat"}
        subtitle={
          theyType ? (
            <span className="typing" data-testid="people-typing">
              typing…
            </span>
          ) : sub ? (
            <span data-testid="people-head-line">{sub}</span>
          ) : undefined
        }
        items={
          c
            ? (close) => (
                <>
                  <button type="button" role="menuitem" onClick={() => (close(), setSheet({ k: "info" }))} data-testid="people-info">
                    Contact info
                  </button>
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => (close(), nav(`/you/people/search?chat=${encodeURIComponent(id)}&name=${encodeURIComponent(c.name)}`))}
                    data-testid="people-search-chat"
                  >
                    Search in this chat
                  </button>
                  {!c.request && (
                    <>
                      <button type="button" role="menuitem" onClick={() => (close(), void change({ muted: !c.muted }, c.muted ? "Notifications on" : "Muted"))}>
                        {c.muted ? "Unmute notifications" : "Mute notifications"}
                      </button>
                      <button
                        type="button"
                        role="menuitem"
                        onClick={() => {
                          close();
                          void change({ archived: !c.archived }, c.archived ? "Back in your chats" : "Archived").then((ok) => ok && !c.archived && nav("/you"));
                        }}
                      >
                        {c.archived ? "Unarchive" : "Archive"}
                      </button>
                    </>
                  )}
                  {!c.gone && (
                    <button type="button" role="menuitem" onClick={() => (close(), void (c.blocked ? unblock() : block()))} data-testid="people-block">
                      {c.blocked ? `Unblock ${c.name}` : `Block ${c.name}`}
                    </button>
                  )}
                  <button type="button" role="menuitem" onClick={() => (close(), setSheet({ k: "report" }))} data-testid="people-report">
                    Report
                  </button>
                  {!c.request && (
                    <button type="button" role="menuitem" onClick={() => (close(), void deleteChat())}>
                      Delete chat
                    </button>
                  )}
                </>
              )
            : undefined
        }
      />
      {problem && (
        <div className="notice pc-notice" role="alert" data-testid="people-problem">
          <span className="grow">
            {problem}
            {/Your standing/.test(problem) && (
              <>
                {" "}
                <Link to="/you/settings/standing" data-testid="people-problem-standing">
                  Open Your standing
                </Link>
              </>
            )}
          </span>
          <button type="button" className="small" onClick={() => setProblem(undefined)}>
            OK
          </button>
        </div>
      )}
      <ErrorBox error={sendError ?? act.error ?? pollError ?? chat.error} />
      {at && atMissing && (
        <p className="notice" role="status" data-testid="people-at-missing" data-state={atMissing}>
          <span className="grow">{AT_FIND_WORDS[atMissing]}</span>
          {atMissing === "far" && (
            <>
              {" "}
              <button type="button" className="small" onClick={keepLooking} disabled={looking} data-testid="people-at-more">
                {looking ? "Looking…" : "Keep looking"}
              </button>
            </>
          )}
        </p>
      )}
      <ChatStream
        items={messages}
        thread={thread}
        hasOlder={hasOlder}
        prefix="people"
        keyOf={(m) => m.id}
        startsRun={(prev, m) => !prev || prev.mine !== m.mine}
        empty={c?.request ? <>No messages.</> : <>Say hello to {name}.</>}
        render={(m, { first }) => {
          const quoted = m.reply_to ? byID.get(m.reply_to) : undefined;
          const fw = forwardWords(m);
          const card = !m.deleted ? m.card : undefined;
          const caption = m.deleted ? "" : m.attachment ? (["Photo", "Voice note", "File", m.attachment.name].includes(m.body) ? "" : m.body) : card || m.location ? "" : m.body;
          const warnTransfer = !m.mine && !!m.warn_transfer && !m.deleted;
          const warnScam = !m.mine && !!m.warn && !m.deleted;
          const inline = !!caption && !warnTransfer && !warnScam && !m.my_reaction && !m.their_reaction;
          const what = messageLine(m).split("\n")[0]?.slice(0, 60) ?? "message";
          const live = !m.local && !m.deleted;
          return (
            <BubbleShell
              id={m.id}
              mine={m.mine}
              first={first}
              testid="people-message"
              className={`${card ? "card-bubble" : ""}${m.my_reaction || m.their_reaction ? " has-reactions" : ""}${thread.flashId === m.id ? " flash" : ""}`.trim()}
              what={what}
              onReply={live && composer.o.enabled !== false && !c?.request ? () => composer.setReply(m) : undefined}
              quickReact={live && !c?.request}
              menu={
                live
                  ? (close, more) => ({
                      onPick: c?.request ? undefined : (e: string) => (close(), void react(m, e)),
                      reactions: c?.request ? undefined : (
                        <span className="reaction-picker" role="group" aria-label="React">
                          {REACTIONS.map((e) => (
                            <button
                              key={e}
                              type="button"
                              className="react-pick"
                              aria-pressed={m.my_reaction === e}
                              aria-label={`React with ${REACTION_NAMES[e]}`}
                              title={REACTION_NAMES[e]}
                              onClick={() => (close(), void react(m, e))}
                            >
                              {e}
                            </button>
                          ))}
                          <button type="button" className="react-pick react-more" aria-label="More reactions" title="More reactions" onClick={more.pick}>
                            <Icon name="plus" />
                          </button>
                        </span>
                      ),
                      actions: (
                        <>
                          {!c?.request && !c?.blocked && !c?.gone && (
                            <PopItem icon="reply" onClick={() => (close(), composer.setReply(m))}>
                              Reply
                            </PopItem>
                          )}
                          {!card && m.body && <CopyItem text={m.body} />}
                          {!card && m.body && <SaveAsItem text={m.body} onSave={saveAs.fromWords} close={close} />}
                          {card?.type !== "split" && card?.type !== "basket" && !c?.request && (
                            <PopItem icon="forward" onClick={() => (close(), setSheet({ k: "forward", m }))}>
                              Forward
                            </PopItem>
                          )}
                          <PopItem icon="star" onClick={() => (close(), void star(m))}>
                            {m.starred ? "Unstar" : "Star"}
                          </PopItem>
                          <div className="pop-sep" role="separator" />
                          <PopItem icon="trash" danger onClick={() => (close(), setSheet({ k: "delete", m }))}>
                            Delete
                          </PopItem>
                        </>
                      ),
                    })
                  : undefined
              }
              after={
                <>
                  <span className={inline ? "stamp inline" : "stamp"}>
                    {m.starred && (
                      <span className="starred" role="img" aria-label="Starred">
                        <Icon name="star" />
                      </span>
                    )}
                    <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
                      {clock(m.created_at)}
                    </time>
                    {m.mine && <PeopleTicks m={m} readTicks={readTicks} />}
                  </span>
                  {(m.my_reaction || m.their_reaction) && (
                    <span className="reactions" role="group" aria-label="Reactions" data-testid="people-reactions">
                      {[m.their_reaction, m.my_reaction].filter(Boolean).map((e, i) => (
                        <span key={i} className="reaction-chip" title={i === 0 && m.their_reaction ? `${name} reacted` : "You reacted"}>
                          <span aria-hidden="true">{e}</span>
                          <span className="sr-only">{i === 0 && m.their_reaction ? `${name} reacted ${REACTION_NAMES[e ?? ""] ?? e}` : `You reacted ${REACTION_NAMES[e ?? ""] ?? e}`}</span>
                        </span>
                      ))}
                    </span>
                  )}
                </>
              }
            >
              {fw && (
                <span className="forwarded" data-testid="people-forwarded">
                  <Icon name="forward" /> {fw}
                </span>
              )}
              {m.reply_to && (
                <span className="quote" data-testid="people-quote">
                  <span className="quote-who">{quoted ? (quoted.mine ? "You" : name) : "Reply"}</span>
                  <span className="quote-text">{quoted ? messageLine(quoted) : "A message further up"}</span>
                </span>
              )}
              {m.deleted && (
                <span className="text people-deleted" data-testid="people-deleted">
                  <Icon name="minus" /> {m.mine ? "You deleted this message" : "This message was deleted"}
                </span>
              )}
              {card && (
                <PeopleCardView
                  card={card}
                  onOpen={card.type !== "split" ? () => setSheet({ k: "card", m }) : undefined}
                  mine={m.mine}
                  onPaid={card.type === "split" && live && !c?.gone && !c?.request ? (paid) => void markPaid(m, paid) : undefined}
                  busy={act.busy}
                />
              )}
              {m.attachment && !m.deleted && <AppAttachmentView a={m.attachment} src={m.local ? undefined : peopleFileURL(id, m.id)} preview={m.localPreview} who={name} mine={m.mine} />}
              {m.location && !m.deleted && <PlaceView place={m.location} />}
              {caption && (
                <span className="text">
                  {caption}
                  {inline && <span className={`stamp-gap${m.mine ? " mine" : ""}`} aria-hidden="true" />}
                </span>
              )}
              {warnTransfer && (
                <span className="pc-transfer-warn" role="note" data-testid="people-transfer-warning">
                  <Icon name="warn" />
                  <span>{PEOPLE_TRANSFER_WARNING}</span>
                </span>
              )}
              {warnScam && !warnTransfer && (
                <span className="pc-transfer-warn" role="note" data-testid="people-scam-warning">
                  <Icon name="warn" />
                  <span>{SCAM_WARNING}</span>
                </span>
              )}
            </BubbleShell>
          );
        }}
      />
      {c?.waiting && !c.request && !c.blocked && !c.gone && !c.suspended && (
        <p className="pc-push-line" role="status" data-testid="people-waiting">
          Your message waits for {c.name} to accept it.
        </p>
      )}
      {c?.request ? (
        <div className="people-request-bar" role="group" aria-label="Message request" data-testid="request-bar">
          <p>
            <strong>{c.name}</strong> wants to chat with you. They are not told you read this unless you accept.
          </p>
          <div className="row-actions">
            <button type="button" className="primary" disabled={act.busy} onClick={() => void accept()} data-testid="request-accept">
              Accept
            </button>
            <button type="button" disabled={act.busy} onClick={() => void decline()} data-testid="request-delete">
              Delete
            </button>
            <button type="button" className="danger" disabled={act.busy} onClick={() => setSheet({ k: "report", blockToo: true })} data-testid="request-block">
              Block and report
            </button>
          </div>
        </div>
      ) : closed ? (
        <p className="customer-note stop" role="status" data-testid="people-closed">
          {closed}
        </p>
      ) : c?.blocked ? (
        <p className="customer-note stop" role="status" data-testid="people-blocked">
          You blocked {c.name}.{" "}
          <button type="button" className="link" onClick={() => void unblock()}>
            Unblock
          </button>
        </p>
      ) : (
        c && (
          <ComposerView
            c={composer}
            busy={false}
            prefix="people"
            textId="people-text"
            textLabel={`Message ${c.name}`}
            lead={
              <button type="button" className="composer-tools" aria-label="Share a card" data-testid="people-share-button" onClick={() => setSheet({ k: "share" })}>
                <Icon name="grid" />
              </button>
            }
          />
        )
      )}
      {c && !c.request && !c.blocked && !closed && <TellMe people />}
      {c && sheet?.k === "info" && <InfoSheet c={c} onClose={() => setSheet(undefined)} onChanged={reloadChat} />}
      {c && sheet?.k === "report" && (
        <PeopleReportSheet
          chat={id}
          name={c.name}
          blockToo={!!sheet.blockToo}
          blocked={c.blocked}
          onClose={() => setSheet(undefined)}
          onDone={(removed) => {
            if (removed) nav("/you/people/requests", { replace: true });
            else reloadChat();
          }}
        />
      )}
      {c && sheet?.k === "forward" && <ForwardSheet from={{ chat_id: id }} m={sheet.m} onClose={() => setSheet(undefined)} />}
      {c && sheet?.k === "card" && <CardSheet base={base} m={sheet.m} onClose={() => setSheet(undefined)} />}
      {c && sheet?.k === "share" && <ShareSheet onClose={() => setSheet(undefined)} onSend={shareCard} onSplit={() => setSheet({ k: "split" })} onPlace={() => setSheet({ k: "place" })} together={!c.request && !c.waiting && !c.gone && !c.blocked ? { chat_id: id } : undefined} />}
      {c && sheet?.k === "place" && <PlaceSheet name={c.name} onClose={() => setSheet(undefined)} onSend={sharePlace} />}
      {c && sheet?.k === "split" && <SplitSheet name={c.name} onClose={() => setSheet(undefined)} onSend={shareCard} />}
      {c && sheet?.k === "delete" && (
        <DeleteAsk
          m={sheet.m}
          onClose={() => setSheet(undefined)}
          onDelete={(forEveryone) =>
            void act.run(async () => {
              setSheet(undefined);
              await del(`${base}/messages/${encodeURIComponent(sheet.m.id)}?for=${forEveryone ? "everyone" : "me"}`);
              if (forEveryone) await pull(false);
              else setMessages((cur) => cur?.filter((x) => x.id !== sheet.m.id));
            }, forEveryone ? "Deleted for everyone" : "Deleted for you")
          }
        />
      )}
      {sure.node}
      {saveAs.node}
    </section>
  );
}

/** A card in a people chat: a product, a business, an order or a delivery
 * (opened live), or a "Your share" card with Mark as paid. */
export function PeopleCardView({
  card,
  mine = false,
  onOpen,
  onPaid,
  busy,
}: {
  card: PeopleCard;
  /** Ours: a share we asked for (no Copy number). */
  mine?: boolean;
  onOpen?: () => void;
  onPaid?: (paid: boolean) => void;
  busy?: boolean;
}) {
  // A saved thing shared (saved objects S1): its own card, with Copy and Save this.
  if (card.type === "saved" && card.saved) return <SavedCardView card={card.saved} mine={mine} />;
  const split = card.type === "split";
  const title = split ? splitTitle(card, naira) : card.title;
  if (card.type === "basket" && card.basket_id) {
    return (
      <article className="tk-ref app-card" aria-label={`Order together: ${card.title}`} data-testid="people-card" data-type="basket">
        <BasketCardBody id={card.basket_id} title={card.title} />
      </article>
    );
  }
  const line = split
    ? card.pay_to
      ? `Pay to ${card.pay_to.account_name}, ${card.pay_to.bank} ${card.pay_to.account_number}`
      : ""
    : [card.business && card.business !== card.title ? card.business : "", card.price_kobo !== undefined ? naira(card.price_kobo) : "", card.reference ?? "", card.area ?? ""].filter(Boolean).join(" · ");
  const icon = { product: "tag", business: "store", order: "orders", delivery: "package", split: "split", basket: "orders", saved: "note" }[card.type];
  return (
    <article className="tk-ref app-card" aria-label={`${cardKind(card)}: ${title}`} data-testid="people-card" data-type={card.type}>
      <span className="tk-ref-top">
        <span className="tk-ref-tile order" aria-hidden="true">
          <Icon name={icon} />
        </span>
        <span className="tk-ref-words">
          <span className="tk-kicker">{cardKind(card)}</span>
          <span className="tk-ref-name">{title}</span>
          {line && <span className="tk-ref-sub">{line}</span>}
        </span>
        {split && card.paid && (
          <span className="tk-ref-pill">
            <Pill tone="lime">Paid</Pill>
          </span>
        )}
      </span>
      {split && <span className="tk-ref-sub people-split-note">Taskiem moves no money: pay with your bank app, then mark it paid.</span>}
      {(onOpen || onPaid || (split && !mine && card.pay_to)) && (
        <span className="tk-ref-foot">
          {split && !mine && card.pay_to && (
            <button
              type="button"
              className="tk-btn soft"
              data-testid="split-copy"
              onClick={() => {
                const n = card.pay_to?.account_number ?? "";
                void navigator.clipboard?.writeText(n).then(
                  () => toast("Account number copied"),
                  () => toast(`The account number is ${n}`),
                );
              }}
            >
              Copy number
            </button>
          )}
          {onOpen && (
            <button type="button" className="tk-btn primary" onClick={onOpen} data-testid="people-card-open">
              Open<span className="sr-only"> {title}</span>
            </button>
          )}
          {onPaid &&
            (card.paid ? (
              <button type="button" className="tk-btn soft" disabled={busy} onClick={() => onPaid(false)} data-testid="split-unpaid">
                Not paid yet
              </button>
            ) : (
              <button type="button" className="tk-btn primary" disabled={busy} onClick={() => onPaid(true)} data-testid="split-paid">
                Mark as paid
              </button>
            ))}
        </span>
      )}
    </article>
  );
}

/** Delete: for me, or for everyone within 24 hours of sending. */
export function DeleteAsk({
  m,
  group = false,
  mayEveryone,
  onClose,
  onDelete,
}: {
  m: Pick<PeopleMessage, "mine" | "deleted" | "local" | "created_at">;
  /** In a group: everyone's phones, the others' copies. */
  group?: boolean;
  /** Whether "Delete for everyone" is offered, when the chat decides
   * (a group: only while a member); else by the message alone. */
  mayEveryone?: boolean;
  onClose: () => void;
  onDelete: (forEveryone: boolean) => void;
}) {
  const everyone = mayEveryone ?? mayDeleteForEveryone(m);
  return (
    <Modal title="Delete this message?" onClose={onClose}>
      <p>
        {everyone
          ? group
            ? "Deleting it for everyone takes it from everyone's phone in the group. A copy or a screenshot they made before stays."
            : "Deleting it for everyone takes it from their phone too. A copy or a screenshot they made before stays."
          : group
            ? "It goes from your copy of the group. The others keep theirs."
            : "It goes from your chat. They keep their copy."}
      </p>
      <div className="row-actions">
        <button type="button" autoFocus onClick={onClose}>
          Cancel
        </button>
        <button type="button" className="danger" onClick={() => onDelete(false)} data-testid="delete-for-me">
          Delete for me
        </button>
        {everyone && (
          <button type="button" className="danger" onClick={() => onDelete(true)} data-testid="delete-for-everyone">
            Delete for everyone
          </button>
        )}
      </div>
    </Modal>
  );
}

/** Contact info: who they are, as their settings let you see them, and how
 * people chats are kept. */
function InfoSheet({ c, onClose, onChanged }: { c: PeopleChat; onClose: () => void; onChanged: () => void }) {
  const p = c.person;
  const [naming, setNaming] = useState(false);
  const [name, setName] = useState(p?.contact_name ?? "");
  const act = useAction();
  return (
    <Sheet title="Contact info" onClose={onClose} testid="people-info-sheet">
      <div className="people-info">
        <ChatAvatar name={c.name} logo={photoOf(p)} className="chat-link-avatar" />
        <h2>{c.name}</h2>
        {c.gone ? (
          <p className="hint">This person deleted their account.</p>
        ) : c.suspended ? (
          <p className="hint">This account can't get messages right now.</p>
        ) : (
          <>
            {lastSeenWords(p) && <p className="hint">{lastSeenWords(p)}</p>}
            <p data-testid="people-info-number">{c.phone ? phoneWords(c.phone) : "Their number is not shown."}</p>
            {p?.contact_name && p.contact_name !== p.name && <p className="hint">Their own name: {p.name}</p>}
            {c.contact ? (
              naming ? (
                <form
                  className="pc-check"
                  onSubmit={(e) => {
                    e.preventDefault();
                    void act.run(async () => {
                      await put(`/v1/me/contacts/${encodeURIComponent(c.person_id)}`, { name: name.trim() || null });
                      setNaming(false);
                      onChanged();
                    }, "Name saved");
                  }}
                >
                  <KitField label="Their name in your contacts" hint="Empty: their own name">
                    <input value={name} maxLength={120} onChange={(e) => setName(e.target.value)} />
                  </KitField>
                  <button type="submit" className="primary" disabled={act.busy}>
                    Save
                  </button>
                </form>
              ) : (
                <button type="button" onClick={() => setNaming(true)} data-testid="people-rename">
                  Change their name
                </button>
              )
            ) : (
              <p className="hint">Not in your contacts.</p>
            )}
          </>
        )}
        <ErrorBox error={act.error} />
        <p className="hint people-kept" data-testid="people-kept">
          <Icon name="info" /> {KEPT_WORDS}
        </p>
      </div>
    </Sheet>
  );
}

/** Report (and, from a request, block too): why, a note, and consent to
 * send the last 20 messages. */
export function PeopleReportSheet({
  chat,
  name,
  blockToo,
  blocked,
  onClose,
  onDone,
}: {
  chat: string;
  name: string;
  blockToo: boolean;
  blocked: boolean;
  onClose: () => void;
  onDone: (removed: boolean) => void;
}) {
  const [reason, setReason] = useState<(typeof REPORT_REASONS)[number]["key"]>();
  const [note, setNote] = useState("");
  const [consent, setConsent] = useState(false);
  const [alsoBlock, setAlsoBlock] = useState(blockToo && !blocked);
  const [said, setSaid] = useState<string>();
  const act = useAction();
  const base = `/v1/me/people/${encodeURIComponent(chat)}`;
  return (
    <Sheet
      title={said ? "Reported" : blockToo ? `Block and report ${name}` : `Report ${name}`}
      onClose={() => {
        onClose();
        if (said) onDone(false);
      }}
      testid="people-report-sheet"
      footer={
        said ? undefined : (
          <button
            type="button"
            className="tk-cta"
            disabled={!reason || !consent || act.busy}
            data-testid="people-report-send"
            onClick={() =>
              void act.run(async () => {
                const report = { reason, note: note.trim(), consent };
                if (alsoBlock) {
                  const r = await post<{ removed: boolean }>(`${base}/block`, { report });
                  onClose();
                  onDone(r.removed);
                  return;
                }
                const r = await post<{ said: string }>(`${base}/report`, report);
                setSaid(r.said);
              }, alsoBlock ? `Blocked and reported ${name}` : false)
            }
          >
            {act.busy ? "Sending…" : alsoBlock ? "Block and report" : "Send report"}
          </button>
        )
      }
    >
      {said ? (
        <div className="pc-done">
          <p role="status">{said}</p>
        </div>
      ) : (
        <div className="pc-check">
          <fieldset className="pc-choice">
            <legend>What is wrong?</legend>
            {REPORT_REASONS.map((r) => (
              <label key={r.key} className="switch-line">
                <input type="radio" name="people-report-reason" checked={reason === r.key} onChange={() => setReason(r.key)} /> <span>{r.words}</span>
              </label>
            ))}
          </fieldset>
          <KitField label="Tell us more" hint="Optional">
            <textarea rows={3} maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)} />
          </KitField>
          {!blocked && (
            <label className="switch-line">
              <input type="checkbox" checked={alsoBlock} onChange={(e) => setAlsoBlock(e.target.checked)} data-testid="people-report-block" /> <span>Block {name} too</span>
            </label>
          )}
          <label className="switch-line pc-consent">
            <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} data-testid="people-report-consent" /> <span>{REPORT_CONSENT}</span>
          </label>
          <ErrorBox error={act.error} />
        </div>
      )}
    </Sheet>
  );
}

/** Forward one message to up to five chats with people and groups (one
 * for a message forwarded many times), from a people chat or a group
 * (POST /v1/me/forward). */
export function ForwardSheet({ from, m, onClose }: { from: { chat_id: string } | { group_id: string }; m: Pick<PeopleMessage, "id" | "forwarded_many">; onClose: () => void }) {
  const people = usePagedList<PeopleChatList, PeopleChat>("/v1/me/people", (r) => r.chats ?? [], (x) => x.id);
  const groups = usePagedList<{ groups: Group[]; more?: boolean; next?: string }, Group>("/v1/me/groups", (r) => r.groups ?? [], (x) => x.id);
  const [picked, setPicked] = useState<string[]>([]);
  const [failed, setFailed] = useState<string[]>([]);
  const act = useAction();
  const cap = forwardCap(m);
  const here = "chat_id" in from ? from.chat_id : from.group_id;
  const targets = [
    ...(people.rows ?? []).filter((x) => x.id !== here && canSendTo(x)).map((x) => ({ kind: "person" as const, id: x.id, name: x.name })),
    ...(groups.rows ?? []).filter((g) => g.id !== here && g.can_post && !g.left).map((g) => ({ kind: "group" as const, id: g.id, name: g.name })),
  ];
  const nameOf = (id: string) => targets.find((x) => x.id === id)?.name ?? "A chat";
  return (
    <Sheet
      title="Forward to"
      onClose={onClose}
      testid="people-forward-sheet"
      sub={cap === 1 ? "This message was forwarded many times: one chat at a time." : `Up to ${cap} chats at once.`}
      footer={
        <button
          type="button"
          className="tk-cta"
          disabled={picked.length === 0 || act.busy}
          data-testid="people-forward-send"
          onClick={() =>
            void act.run(async () => {
              const to = targets.filter((x) => picked.includes(x.id));
              const r = await post<{ sent: unknown[]; failed: { kind: string; id: string; error: string }[] }>("/v1/me/forward", {
                ...from,
                message_id: m.id,
                to_chats: to.filter((x) => x.kind === "person").map((x) => x.id),
                to_groups: to.filter((x) => x.kind === "group").map((x) => x.id),
              });
              const said = forwardSaid(r.sent?.length ?? 0);
              if (said) toast(said);
              if (!r.failed?.length) return onClose();
              // Only the chats it did not reach stay picked, to try again.
              setFailed(r.failed.map((f) => `${nameOf(f.id)}: ${f.error}`));
              setPicked(r.failed.map((f) => f.id).filter((id) => picked.includes(id)));
            }, false)
          }
        >
          Forward
        </button>
      }
    >
      {(!people.rows || !groups.rows) && !people.error && !groups.error && <Skeleton rows={2} label="Loading your chats…" />}
      <ErrorBox error={people.error ?? groups.error ?? act.error} />
      {failed.length > 0 && (
        <ul className="error" role="alert">
          {failed.map((f) => (
            <li key={f}>{f}</li>
          ))}
        </ul>
      )}
      {people.rows && groups.rows && targets.length === 0 && !people.more && !groups.more && <p className="hint">No other chats with people or groups yet.</p>}
      <fieldset className="pc-choice">
        <legend className="sr-only">Chats</legend>
        {targets.map((x) => {
          const on = picked.includes(x.id);
          return (
            <label key={`${x.kind}:${x.id}`} className="switch-line">
              <input
                type="checkbox"
                checked={on}
                disabled={!on && picked.length >= cap}
                onChange={(e) => setPicked((cur) => (e.target.checked ? [...cur, x.id] : cur.filter((y) => y !== x.id)))}
                data-testid="people-forward-pick"
              />{" "}
              <span>
                {x.name}
                {x.kind === "group" && <span className="chat-kind"> · Group</span>}
              </span>
            </label>
          );
        })}
      </fieldset>
      <MoreRows more={people.more} busy={people.loadingMore} onMore={people.loadMore} count={people.rows?.length ?? 0} label="Show more chats" />
      <MoreRows more={groups.more} busy={groups.loadingMore} onMore={groups.loadMore} count={groups.rows?.length ?? 0} label="Show more groups" />
    </Sheet>
  );
}

/** A shared card opened: read live, as the person opening it (§10.1): a
 * product's price now (and what it was when shared), few left, Order from
 * their shop; a business's card and Chat with them; an order live only when
 * it is yours. Chat starts the person's own chat with the business, from a
 * shared card (§10.2). */
export function CardSheet({ base, m, onClose }: { base: string; m: Pick<PeopleMessage, "id" | "card">; onClose: () => void }) {
  const view = useLoad(() => get<OpenedCard>(`${base}/messages/${encodeURIComponent(m.id)}/card`), [base, m.id]);
  const o = view.data;
  const card = o?.card ?? m.card;
  const blocked = view.error instanceof ApiError && view.error.status === 403;
  const biz = card?.business_id;
  const p = o?.live.product;
  return (
    <Sheet title={card ? card.title : "Card"} onClose={onClose} testid="people-card-sheet">
      {!o && !view.error && <Skeleton rows={2} label="Loading what was shared…" />}
      {blocked ? <p role="status">This business is not taking chats from you, so it can't be opened.</p> : <ErrorBox error={view.error} />}
      {o && (
        <div className="pc-check" data-testid="people-card-live">
          {o.card.type === "product" &&
            (p ? (
              <>
                <p>
                  <strong>{p.name}</strong>
                </p>
                {o.card.business && <p className="hint">From {[o.card.business, o.card.area].filter(Boolean).join(", ")}</p>}
                {p.price_kobo !== undefined && <p data-testid="people-card-price">{priceNowWords(p.price_kobo, o.card.price_kobo, naira)}</p>}
                <p className="hint" data-testid="people-card-stock">
                  {stockWords(p)}
                </p>
                {p.description && <p className="hint">{p.description}</p>}
              </>
            ) : (
              <>
                <p>
                  <strong>{o.card.title}</strong>
                </p>
                <p className="hint" data-testid="people-card-gone">
                  They no longer sell this. What was shared is all there is to see.
                </p>
              </>
            ))}
          {o.live.business && (
            <>
              <p>
                <strong>{o.live.business.name}</strong>
              </p>
              {o.live.business.tagline && <p>{o.live.business.tagline}</p>}
              <p className="hint">{[o.live.business.area, o.live.business.open_now === undefined ? "" : o.live.business.open_now ? "Open now" : "Closed now"].filter(Boolean).join(" · ")}</p>
            </>
          )}
          {o.card.type === "business" && !o.live.business && <p className="hint">This business is not showing itself now.</p>}
          {o.live.order ? (
            <>
              <p>
                <strong>{o.live.order.summary}</strong>
              </p>
              <p>{naira(o.live.order.total_kobo)}</p>
              <Link className="button" to={`/you/orders/${encodeURIComponent(o.live.order.business_id)}/${encodeURIComponent(o.live.order.reference)}`}>
                See your order
              </Link>
            </>
          ) : (
            (o.card.type === "order" || o.card.type === "delivery") && (
              <p className="hint" data-testid="people-card-shared-only">
                {o.card.reference ? `Order ${o.card.reference}` : "An order"}
                {o.card.state ? ` · ${o.card.state} when shared` : ""}. This order is not yours, so only what was shared shows.
              </p>
            )
          )}
          {o.live.delivery && <p>{o.live.delivery.line}</p>}
          {biz && o.card.type === "product" && (
            <Link className="button primary" to={sharedChatPath(biz, "shop")} data-testid="people-card-order">
              Order from their shop
            </Link>
          )}
          {biz && o.card.type === "business" && (o.live.business?.takes_chats ?? true) && (
            <Link className="button primary" to={sharedChatPath(biz)} data-testid="people-card-chat">
              Chat with them
            </Link>
          )}
        </div>
      )}
    </Sheet>
  );
}

/** Share into the chat: a business, a product, one of your orders, or
 * "Your share". */
export function ShareSheet({
  onClose,
  onSend,
  onSplit,
  onPlace,
  together,
}: {
  onClose: () => void;
  onSend: (c: CardAsk) => Promise<boolean>;
  onSplit: () => void;
  onPlace: () => void;
  /** Ordering together here (5b C): the group or the people chat. */
  together?: { group_id?: string; chat_id?: string };
}) {
  const [step, setStep] = useState<"menu" | "business" | "product" | "order" | "together" | { items: string; business: string; name: string }>("menu");
  // One order picked: shared as it is, or its delivery once a rider has it.
  const [order, setOrder] = useState<{ business_id: string; reference: string; business: string }>();
  const act = useAction();
  // "Shared" only when it went: a refusal is said in the chat instead.
  const go = (card: CardAsk) =>
    void act.run(async () => {
      if (!(await onSend(card))) return onClose();
      toast("Shared");
      onClose();
    }, false);
  const picking = step === "business" || step === "product" || step === "together";
  const chats = useLoad(() => (picking ? get<ChatList>("/v1/me/conversations") : Promise.resolve(undefined)), [picking]);
  // "Order together from {business}": a shared basket card in this chat.
  const startTogether = (business_id: string) =>
    void act.run(async () => {
      await post("/v1/me/baskets", { business_id, ...together });
      onClose();
    }, "Basket started: everyone can add what they want");
  const orders = useLoad(() => (step === "order" ? get<PersonOrders>("/v1/me/orders?limit=30") : Promise.resolve(undefined)), [step === "order"]);
  const detail = useLoad(
    () => (order ? get<PersonOrderDetail>(`/v1/me/orders/${encodeURIComponent(order.business_id)}/${encodeURIComponent(order.reference)}`) : Promise.resolve(undefined)),
    [order?.business_id, order?.reference],
  );
  const items = useLoad(
    () => (typeof step === "object" ? get<{ items: ShopItem[] }>(`/v1/me/chats/${encodeURIComponent(step.items)}/shop`) : Promise.resolve(undefined)),
    [typeof step === "object" ? step.items : ""],
  );
  const businesses = (chats.data?.chats ?? []).filter((x) => x.kind === "business" && x.business_id);
  return (
    <Sheet
      title={typeof step === "object" ? step.name : order ? `${order.business} · ${order.reference}` : step === "together" ? "Order together from…" : "Share"}
      onClose={onClose}
      onBack={order ? () => setOrder(undefined) : step === "menu" ? undefined : () => setStep("menu")}
      testid="people-share-sheet"
    >
      <ErrorBox error={act.error ?? chats.error ?? orders.error ?? items.error ?? detail.error} />
      {step === "menu" && (
        <Items label="What to share">
          <Row icon="store" title="A business" sub={<span className="sub">One you chat with</span>}>
            <button type="button" onClick={() => setStep("business")} data-testid="share-business">
              Choose
            </button>
          </Row>
          <Row icon="tag" title="A product" sub={<span className="sub">From a business's shop</span>}>
            <button type="button" onClick={() => setStep("product")} data-testid="share-product">
              Choose
            </button>
          </Row>
          <Row icon="orders" title="One of your orders" sub={<span className="sub">Or its delivery</span>}>
            <button type="button" onClick={() => setStep("order")} data-testid="share-order">
              Choose
            </button>
          </Row>
          {together && (
            <Row icon="orders" title="Order together" sub={<span className="sub">A shared basket from one business: each adds their own</span>}>
              <button type="button" onClick={() => setStep("together")} data-testid="share-together">
                Choose a business
              </button>
            </Row>
          )}
          <Row icon="split" title="Your share" sub={<span className="sub">Ask them to pay you back</span>}>
            <button type="button" onClick={onSplit} data-testid="share-split">
              Make one
            </button>
          </Row>
          <Row icon="place" title="A place" sub={<span className="sub">Where you are now, to meet or to find you</span>}>
            <button type="button" onClick={onPlace} data-testid="share-place">
              Choose
            </button>
          </Row>
        </Items>
      )}
      {picking && (
        <>
          {!chats.data && !chats.error && <Skeleton rows={2} label="Loading your businesses…" />}
          {chats.data && businesses.length === 0 && <EmptyState icon="store">Businesses you chat with show here.</EmptyState>}
          <Items label="Businesses">
            {businesses.map((b) => (
              <Row key={b.id} letter={b.name} title={b.name} testid="share-business-row">
                <button
                  type="button"
                  disabled={act.busy}
                  data-testid={step === "together" ? "together-business" : undefined}
                  onClick={() =>
                    step === "together"
                      ? startTogether(b.business_id ?? "")
                      : step === "business"
                        ? go({ type: "business", business_id: b.business_id })
                        : setStep({ items: b.id, business: b.business_id ?? "", name: b.name })
                  }
                >
                  {step === "together" ? "Order together" : step === "business" ? "Share" : "Their products"}
                  <span className="sr-only"> {b.name}</span>
                </button>
              </Row>
            ))}
          </Items>
        </>
      )}
      {typeof step === "object" && (
        <>
          {!items.data && !items.error && <Skeleton rows={2} label="Loading their products…" />}
          {items.data && items.data.items.length === 0 && <p className="hint">Nothing in their shop yet.</p>}
          <Items label="Products">
            {(items.data?.items ?? []).map((it) => (
              <Row key={it.id} icon="tag" title={it.name} sub={<span className="sub">{it.price_kobo !== undefined ? naira(it.price_kobo) : ""}</span>} testid="share-product-row">
                <button type="button" disabled={act.busy} onClick={() => go({ type: "product", business_id: step.business, item_id: it.id })}>
                  Share<span className="sr-only"> {it.name}</span>
                </button>
              </Row>
            ))}
          </Items>
        </>
      )}
      {step === "order" && order && (
        <>
          {!detail.data && !detail.error && <Skeleton rows={1} label="Loading your order…" />}
          {detail.data && (
            <Items label="Share">
              <Row icon="orders" title="The order" sub={<span className="sub">{detail.data.summary}</span>} testid="share-order-plain">
                <button type="button" disabled={act.busy} onClick={() => go({ type: "order", business_id: order.business_id, reference: order.reference })}>
                  Share<span className="sr-only"> the order {order.reference}</span>
                </button>
              </Row>
              {detail.data.delivery && (
                <Row icon="package" title="Its delivery" sub={<span className="sub">{detail.data.delivery.line}</span>} testid="share-order-delivery">
                  <button type="button" disabled={act.busy} onClick={() => go({ type: "delivery", business_id: order.business_id, reference: order.reference })}>
                    Share<span className="sr-only"> the delivery of {order.reference}</span>
                  </button>
                </Row>
              )}
            </Items>
          )}
        </>
      )}
      {step === "order" && !order && (
        <>
          {!orders.data && !orders.error && <Skeleton rows={2} label="Loading your orders…" />}
          {orders.data && orders.data.orders.filter((o) => !o.earlier).length === 0 && <EmptyState icon="orders">Your orders show here.</EmptyState>}
          <Items label="Your orders">
            {(orders.data?.orders ?? [])
              .filter((o) => !o.earlier)
              .map((o) => (
                <Row key={`${o.business_id}/${o.reference}`} icon="orders" title={`${o.business} · ${o.reference}`} sub={<span className="sub">{o.summary}</span>} testid="share-order-row">
                  <button type="button" disabled={act.busy} onClick={() => setOrder({ business_id: o.business_id, reference: o.reference, business: o.business })}>
                    Choose<span className="sr-only"> {o.reference}</span>
                  </button>
                </Row>
              ))}
          </Items>
        </>
      )}
    </Sheet>
  );
}

/** "Your share": an amount, what for, and the account to pay to (yours
 * saved, or typed here). Taskiem moves no money. */
export function SplitSheet({ name, onClose, onSend }: { name: string; onClose: () => void; onSend: (c: CardAsk) => Promise<boolean> }) {
  const saved = useLoad(() => get<{ account: PayoutAccount | null; words: string }>("/v1/me/payout-account"), []);
  const [amount, setAmount] = useState("");
  const [what, setWhat] = useState("");
  const [other, setOther] = useState(false);
  const [acct, setAcct] = useState<PayoutAccount>({ bank: "", account_number: "", account_name: "" });
  const act = useAction();
  const kobo = toKobo(amount);
  const typed = other || (saved.data && !saved.data.account);
  const okAccount = !typed || (acct.bank.trim() && acct.account_name.trim() && accountDigits(acct.account_number));
  // The server asks for both: at least ₦1, and what it is for.
  const ok = splitReady(kobo, what) && !!okAccount;
  const tooSmall = amount.trim() !== "" && kobo !== undefined && !Number.isNaN(kobo) && kobo < 100;
  return (
    <Sheet
      title="Your share"
      onClose={onClose}
      testid="people-split-sheet"
      footer={
        <button
          type="button"
          className="tk-cta"
          disabled={!ok || act.busy}
          data-testid="split-send"
          onClick={() =>
            void act.run(async () => {
              const card: CardAsk = { type: "split", amount_kobo: kobo, what: what.trim() };
              if (typed) card.pay_to = { bank: acct.bank.trim(), account_name: acct.account_name.trim(), account_number: accountDigits(acct.account_number) };
              // Refused: said in the chat, and not "Sent".
              const sent = await onSend(card);
              if (sent) toast("Sent");
              onClose();
            }, false)
          }
        >
          Send to {name}
        </button>
      }
    >
      <div className="pc-check">
        <p className="hint">Taskiem moves no money. {name} pays you with their bank app, and either of you marks it paid.</p>
        <KitField label="Amount (₦)" error={tooSmall ? "At least ₦1" : undefined}>
          <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} aria-invalid={tooSmall} data-testid="split-amount" />
        </KitField>
        <KitField label="What for" hint="The cake, the taxi, the tickets…">
          <input value={what} maxLength={120} required aria-required="true" onChange={(e) => setWhat(e.target.value)} data-testid="split-what" />
        </KitField>
        {!saved.data && !saved.error && <Skeleton rows={1} label="Loading your account…" />}
        <ErrorBox error={saved.error ?? act.error} />
        {saved.data?.account && !other && (
          <div className="setting-line" data-testid="split-saved-account">
            <div className="grow">
              <strong>Pay to</strong>
              <div className="sub">
                {saved.data.account.account_name}, {saved.data.account.bank} {saved.data.account.account_number}
              </div>
            </div>
            <button type="button" className="small" onClick={() => setOther(true)}>
              Another account
            </button>
          </div>
        )}
        {typed && <AccountFields value={acct} onChange={setAcct} />}
        {saved.data && !saved.data.account && (
          <p className="hint">
            To fill this in for you next time, <Link to="/you/settings/payout">save your account in Settings</Link>.
          </p>
        )}
      </div>
    </Sheet>
  );
}

/** A bank account's three fields. */
export function AccountFields({ value, onChange }: { value: PayoutAccount; onChange: (v: PayoutAccount) => void }) {
  const bad = value.account_number.trim() !== "" && !accountDigits(value.account_number);
  return (
    <>
      <KitField label="Bank">
        <input value={value.bank} maxLength={80} onChange={(e) => onChange({ ...value, bank: e.target.value })} data-testid="account-bank" />
      </KitField>
      <KitField label="Account number" hint={bad ? "Ten digits" : undefined}>
        <input inputMode="numeric" value={value.account_number} onChange={(e) => onChange({ ...value, account_number: e.target.value })} aria-invalid={bad} data-testid="account-number" />
      </KitField>
      <KitField label="Account name">
        <input value={value.account_name} maxLength={120} onChange={(e) => onChange({ ...value, account_name: e.target.value })} data-testid="account-name" />
      </KitField>
    </>
  );
}

/** A draft kept for a people chat, for the list's rows. */
export const peopleDraft = (id: string) => getDraft(draftKey(id));

/** A place in a message: its name, the figures, and a link to a map. */
export function PlaceView({ place }: { place: Place }) {
  const name = place.label || "A place";
  return (
    <a className="chat-file people-place" href={mapURL(place)} target="_blank" rel="noopener noreferrer" data-testid="people-place">
      <Icon name="place" />
      <span className="people-place-words">
        <span className="name">{name}</span>
        <span className="people-place-sub">See it on a map</span>
      </span>
      <span className="sr-only"> (opens a map in a new tab)</span>
    </a>
  );
}

/** Sending a place (§7): where the person is, found only when they ask
 * (the browser asks them then), shown before it is sent, with a name of
 * their own if they like. */
export function PlaceSheet({ name, onClose, onSend }: { name: string; onClose: () => void; onSend: (p: Place) => Promise<boolean> }) {
  const [state, setState] = useState<{ k: "ask" } | { k: "finding" } | { k: "found"; lat: number; lng: number; accuracy?: number } | { k: "problem"; words: string }>({ k: "ask" });
  const [label, setLabel] = useState("");
  const act = useAction();
  const find = () => {
    if (typeof navigator === "undefined" || !navigator.geolocation) {
      setState({ k: "problem", words: placeProblem(0) });
      return;
    }
    setState({ k: "finding" });
    navigator.geolocation.getCurrentPosition(
      (p) => setState({ k: "found", lat: p.coords.latitude, lng: p.coords.longitude, accuracy: p.coords.accuracy }),
      (e) => setState({ k: "problem", words: placeProblem(e.code) }),
      { enableHighAccuracy: true, timeout: 20_000, maximumAge: 60_000 },
    );
  };
  const checked = state.k === "found" ? checkPlace(state.lat, state.lng, label) : undefined;
  return (
    <Sheet
      title="Send a place"
      onClose={onClose}
      testid="people-place-sheet"
      footer={
        state.k === "found" ? (
          <button
            type="button"
            className="tk-cta"
            disabled={!checked?.ok || act.busy}
            data-testid="place-send"
            onClick={() =>
              void act.run(async () => {
                if (!checked?.ok) return;
                const went = await onSend(checked.place);
                if (went) toast("Place sent");
                onClose();
              }, false)
            }
          >
            {act.busy ? "Sending…" : `Send to ${name}`}
          </button>
        ) : undefined
      }
    >
      <div className="pc-check">
        {state.k === "ask" && (
          <>
            <p>Send {name} where you are now. Your browser asks you first; nothing is shared until you press Send.</p>
            <button type="button" className="primary" onClick={find} data-testid="place-find">
              <Icon name="place" /> Use where I am now
            </button>
          </>
        )}
        {state.k === "finding" && <Skeleton rows={1} label="Finding where you are…" />}
        {state.k === "problem" && (
          <>
            <p className="error" role="alert" data-testid="place-problem">
              {state.words}
            </p>
            <button type="button" onClick={find} data-testid="place-again">
              Try again
            </button>
          </>
        )}
        {state.k === "found" && (
          <>
            <div className="people-place-preview" data-testid="place-preview">
              <Icon name="place" />
              <span className="people-place-words">
                <span className="name">{label.trim() || "Where you are now"}</span>
                <span className="people-place-sub">
                  {placeFigures(state)}
                  {state.accuracy !== undefined && state.accuracy > 0 ? `, to about ${Math.round(state.accuracy)} m` : ""}
                </span>
              </span>
              <a href={mapURL(state)} target="_blank" rel="noopener noreferrer" data-testid="place-map">
                Check it on a map<span className="sr-only"> (opens in a new tab)</span>
              </a>
            </div>
            <KitField label="Name it (optional)" hint="For example: the shop's gate, or Mama Put on Herbert Macaulay.">
              <input value={label} maxLength={200} onChange={(e) => setLabel(e.target.value)} data-testid="place-label" />
            </KitField>
            {checked && !checked.ok && (
              <p className="error" role="alert">
                {checked.words}
              </p>
            )}
          </>
        )}
        <ErrorBox error={act.error} />
      </div>
    </Sheet>
  );
}
