import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSavedPicks } from "./SavedPicks";
import { sendSaved } from "./SavedCard";
import { SaveAsItem, useSaveAs } from "./SaveAs";
import { PayFromMessages, PhotoAccountItems } from "./PayChip";
import type { Suggestion } from "../lib/mentions";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { ApiError, del, get, post, put, upload } from "../api";
import { Icon } from "../icons";
import { Sheet } from "../kit";
import { getDraft } from "../lib/drafts";
import { clock, REACTIONS } from "../lib/linkchat";
import {
  forwardWords,
  messageLine,
  PEOPLE_TRANSFER_WARNING,
  SCAM_WARNING,
  sendProblem,
  mergePeople,
  withLatestPeople,
  type CardAsk,
} from "../lib/people";
import {
  fromTaskiem,
  groupFileURL,
  groupHeadWords,
  groupInfoPath,
  groupThreadWithLive,
  groupThreadWithReceipts,
  mayDeleteGroupForEveryone,
  mentionable,
  mentionedIn,
  mentionPieces,
  noPostWords,
  reactionCountWords,
  readByWords,
  type Group,
  type GroupMember,
  type GroupMembers,
  type GroupMessage,
  type GroupMessageInfo,
} from "../lib/groups";
import { clientFor, mayHaveArrived, type KeptSend } from "../lib/personchat";
import { AT_FIND_WORDS, atFindState, FIND_PAGES, TypingGate, typingWords, type AtFind, type Place } from "../lib/peoplelive";
import { useThread } from "../lib/useThread";
import { ErrorBox, fmtTime, Skeleton, useAction, useLoad } from "../ui";
import { AppAttachmentView } from "./AppCards";
import { BubbleShell, CopyItem, PopItem } from "./BubbleShell";
import { ChatAvatar } from "./ChatAvatar";
import { BUSINESS_FILES, ComposerView, useComposer } from "./ChatComposer";
import { ChatHead } from "./ChatHead";
import { ChatStream } from "./ChatStream";
import { useConfirm } from "./ConfirmSheet";
import { asDataURL, CardSheet, DeleteAsk, ForwardSheet, PeopleCardView, PeopleTicks, PlaceSheet, PlaceView, REACTION_NAMES, ShareSheet, SplitSheet } from "./PeopleChat";
import { livePoll, usePeopleLive } from "./PeopleLive";

// A people group on You (docs/spec/people-chatting.md §9, §13; "As built:
// stage 5b, groups (server)" and "…, group safety (server)"): everything a
// chat with a person has (words, photos, voice notes, files, places,
// cards, replies, reactions with counts, stars, forwarding, deleting,
// search, live updates, typing), with each sender's name and photo as
// their settings let, @mentions of the members, "Read by" on our own
// messages, the events as centred lines (Taskiem's moderators' too), and a
// closed or left group's notice in place of the composer.

const POLL_MS = 4000;

const draftKey = (id: string) => `group:${id}`;

/** A draft kept for a group, for the list's rows. */
export const groupDraft = (id: string) => getDraft(draftKey(id));

let localN = 0;

type Page = { messages: GroupMessage[]; more: boolean };

/** /you/groups/:id: one group. */
export function GroupChatPage() {
  const { id = "" } = useParams();
  const nav = useNavigate();
  const loc = useLocation();
  const at = new URLSearchParams(loc.search).get("at") ?? undefined;
  const base = `/v1/me/groups/${encodeURIComponent(id)}`;
  const group = useLoad(() => get<Group>(base), [base]);
  const g = group.data;
  // The members now: names and photos for the senders, and who "@" offers.
  // Someone who left reads no members (403).
  const people = useLoad(() => (g && !g.left ? get<GroupMembers>(`${base}/members`) : Promise.resolve(undefined)), [base, !!g, g?.left, g?.members]);
  const members = useMemo(() => people.data?.members ?? [], [people.data]);
  const byPerson = useMemo(() => new Map(members.map((m) => [m.person_id, m])), [members]);
  const live = usePeopleLive();
  const [atMissing, setAtMissing] = useState<AtFind>();
  const [looking, setLooking] = useState(false);
  const [messages, setMessages] = useState<GroupMessage[]>();
  const [hasOlder, setHasOlder] = useState(false);
  const [pollError, setPollError] = useState<unknown>();
  const [problem, setProblem] = useState<string>();
  const [sendError, setSendError] = useState<unknown>();
  const kept = useRef<KeptSend | undefined>(undefined);
  const [sheet, setSheet] = useState<
    | { k: "share" | "split" | "place" }
    | { k: "forward" | "card" | "delete" | "info" | "reactions"; m: GroupMessage }
    | undefined
  >();
  const act = useAction();
  const sure = useConfirm();
  const loadingOlder = useRef(false);
  const loadingFirst = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const thread = useThread({ items: messages, isMine: (m) => m.mine, hasOlder, loadOlder: olderCb });

  // Read marks: what others sent is marked read while the tab is seen.
  const lastSeen = useRef("");
  const markRead = useCallback(
    (all: GroupMessage[] | undefined) => {
      if (!all || document.visibilityState !== "visible") return;
      const latest = [...all].reverse().find((m) => !m.mine && !m.local);
      if (!latest || latest.id === lastSeen.current) return;
      lastSeen.current = latest.id;
      void post(`${base}/read`).catch(() => undefined);
    },
    [base],
  );

  const findOlder = useCallback(
    async (all: GroupMessage[], more: boolean, find: string) => {
      for (let i = 0; i < FIND_PAGES && more && !all.some((m) => m.id === find); i++) {
        const oldest = all.find((m) => !m.local);
        if (!oldest) break;
        const older = await get<Page>(`${base}/messages?before=${encodeURIComponent(oldest.id)}`);
        all = mergePeople(all, older.messages);
        more = older.more;
      }
      return { all, more };
    },
    [base],
  );
  const pull = useCallback(
    async (first: boolean, find?: string) => {
      if (!first && loadingFirst.current) return;
      const out = await get<Page>(`${base}/messages`);
      if (first && find && !out.messages.some((m) => m.id === find)) {
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
      markRead(out.messages);
    },
    [base, findOlder, markRead],
  );
  const reloadGroup = group.reload;
  const reloadPeople = people.reload;
  const firstRun = useRef(0);
  useEffect(() => {
    setMessages(undefined);
    setAtMissing(undefined);
    lastSeen.current = "";
    thread.reset(at);
    const run = ++firstRun.current;
    loadingFirst.current = true;
    pull(true, at)
      .catch(setPollError)
      .finally(() => {
        if (firstRun.current === run) loadingFirst.current = false;
      });
  }, [base, pull, at]);
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
  // Polling: as a people chat's, slowly while the live connection is open.
  const connected = live.connected;
  useEffect(() => {
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      pull(false).catch(setPollError);
      reloadGroup();
    }, livePoll(connected, POLL_MS));
    return () => clearInterval(t);
  }, [pull, reloadGroup, connected]);
  useEffect(() => {
    const seen = () => {
      if (document.visibilityState === "visible") pull(false).catch(setPollError);
    };
    document.addEventListener("visibilitychange", seen);
    return () => document.removeEventListener("visibilitychange", seen);
  }, [pull]);
  // The live connection: the group's messages, our ticks, the group itself,
  // a message deleted for us elsewhere, and what was missed.
  const readSoon = useRef<number>(undefined);
  const groupSoon = useRef<number>(undefined);
  useEffect(
    () =>
      live.subscribe((e) => {
        if (e.type === "group_message" && e.data.group_id === id) {
          setMessages((cur) => groupThreadWithLive(cur, id, e.data));
          if (!e.data.message.mine && document.visibilityState === "visible") {
            window.clearTimeout(readSoon.current);
            readSoon.current = window.setTimeout(() => void post(`${base}/read`).catch(() => undefined), 300);
          }
        } else if (e.type === "group_receipts" && e.data.group_id === id) {
          setMessages((cur) => groupThreadWithReceipts(cur, id, e.data));
        } else if (e.type === "gone" && e.data.chat_id === id) {
          if (e.data.id) setMessages((cur) => cur?.filter((m) => m.id !== e.data.id));
          else nav("/you", { replace: true });
        } else if (e.type === "group" && e.data.id === id) {
          window.clearTimeout(groupSoon.current);
          groupSoon.current = window.setTimeout(() => {
            reloadGroup();
            reloadPeople();
          }, 200);
        } else if (e.type === "ready") {
          pull(false).catch(setPollError);
          reloadGroup();
        }
      }),
    [live.subscribe, id, base, pull, reloadGroup, reloadPeople, nav],
  );
  useEffect(
    () => () => {
      window.clearTimeout(readSoon.current);
      window.clearTimeout(groupSoon.current);
    },
    [],
  );
  // Typing: as a people chat's, only where we may post.
  const gate = useRef(new TypingGate());
  const typed = (v: string) => {
    if (!g?.can_post) return;
    const say = gate.current.change(v, Date.now());
    if (say !== null) void post(`${base}/typing`, { typing: say }).catch(() => undefined);
  };
  useEffect(() => {
    const t = gate.current;
    return () => {
      if (t.leave()) void post(`${base}/typing`, { typing: false }).catch(() => undefined);
    };
  }, [base]);
  loadOlderRef.current = () => {
    const oldest = messages?.find((m) => !m.local);
    if (!oldest || loadingOlder.current || loadingFirst.current) return;
    loadingOlder.current = true;
    get<Page>(`${base}/messages?before=${encodeURIComponent(oldest.id)}`)
      .then((out) => {
        thread.keepPlace();
        setMessages((cur) => mergePeople(cur ?? [], out.messages));
        setHasOlder(out.more);
      })
      .catch(setPollError)
      .finally(() => (loadingOlder.current = false));
  };

  const send = async (text: string, file?: File, reply?: GroupMessage, picks?: { id: string; name: string }[]) => {
    setProblem(undefined);
    gate.current.sent();
    const mentions = mentionedIn(text, mentionable(members), picks);
    const client = file ? `${Date.now()}-${++localN}` : clientFor(kept.current, text, () => `${Date.now()}-${++localN}`);
    kept.current = undefined;
    const preview = file ? await asDataURL(file) : undefined;
    const mine: GroupMessage = {
      id: `local-${client}`,
      group_id: id,
      mine: true,
      sender_id: null,
      sender: "You",
      kind: file ? "media" : "text",
      body: text,
      reply_to: reply?.id,
      attachment: file ? { kind: file.type.startsWith("image/") ? "photo" : file.type.startsWith("audio/") ? "voice" : "file", type: file.type, size: file.size, name: file.name } : undefined,
      created_at: new Date().toISOString(),
      local: true,
      localPreview: preview,
    };
    setMessages((cur) => mergePeople(cur ?? [], [mine]));
    thread.toBottom?.();
    try {
      let out: GroupMessage;
      if (file) {
        const form = new FormData();
        form.append("file", file);
        if (text) form.append("text", text);
        form.append("client_id", client);
        if (reply) form.append("reply_to", reply.id);
        if (mentions.length) form.append("mentions", mentions.join(","));
        out = await upload<GroupMessage>(`${base}/files`, form);
      } else out = await post<GroupMessage>(`${base}/messages`, { text, client_id: client, reply_to: reply?.id, mentions: mentions.length ? mentions : undefined });
      setMessages((cur) => mergePeople((cur ?? []).filter((m) => m.id !== mine.id), [out]));
      setSendError(undefined);
    } catch (e) {
      setMessages((cur) => cur?.filter((m) => m.id !== mine.id));
      if (text && !file) {
        composer.setText((cur) => (cur === "" ? text : cur));
        if (e instanceof ApiError && mayHaveArrived(e.status)) kept.current = { text, client };
      }
      const said = sendProblem(e);
      if (said) setProblem(said);
      else setSendError(e);
      reloadGroup();
    }
  };

  /** A card or a place: said above the composer when refused. */
  const sendOther = async (body: { card: CardAsk } | { location: Place }) => {
    setProblem(undefined);
    try {
      const out = await post<GroupMessage>(`${base}/messages`, { ...body, client_id: `${Date.now()}-${++localN}` });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
      thread.toBottom?.();
      return true;
    } catch (e) {
      const said = sendProblem(e);
      if (!said) throw e;
      setProblem(said);
      reloadGroup();
      return false;
    }
  };

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
          (confirm) => post<GroupMessage>(`${base}/messages`, { card: { type: "saved", saved: { id: pick.id, owner: "mine", confirm } }, client_id: `${Date.now()}-${++localN}` }),
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
  const composer = useComposer<GroupMessage>({
    draftKey: draftKey(id),
    rules: BUSINESS_FILES,
    sources: {
      "@": mentionable(members).map((m) => ({ kind: "customer" as const, id: m.person_id, name: m.name, sub: m.role === "admin" ? "Admin" : undefined })),
      "#": [],
      atLabel: "Members to mention",
      // Saved texts (saved objects S1): their words into the box.
      saved: savedTexts.saved, savedComplete: savedTexts.complete, wantSaved: savedTexts.want, shareSaved, saveNamed: (name) => saveAs.named(name),
    },
    replyLine: (m) => messageLine(m),
    onType: (v) => {
      setSendError(undefined);
      typed(v);
    },
    onSend: (s) => {
      setSendError(undefined);
      void send(s.text, s.file, s.reply, s.picks);
    },
    enabled: !!g?.can_post,
    closes: [!!sheet],
  });

  const name = g?.name ?? "this group";
  const change = (body: Record<string, boolean>, said: string) =>
    act.run(async () => {
      await put(`${base}/me`, body);
      reloadGroup();
    }, said);
  const leave = async () => {
    const yes = await sure.confirm({
      title: `Leave ${name}?`,
      body: "You keep your copy up to now, until you delete it. You won't get new messages or be able to write.",
      ok: "Leave",
      danger: true,
    });
    if (!yes) return;
    await act.run(async () => {
      await post(`${base}/leave`);
      reloadGroup();
    }, `You left ${name}`);
  };
  const deleteForMe = async () => {
    const yes = await sure.confirm({
      title: g?.left ? "Delete this group?" : "Clear this group?",
      body: g?.left ? "Its messages go from your chats for good." : "Its messages up to now go from your copy. The others keep theirs.",
      ok: "Delete",
      danger: true,
    });
    if (!yes) return;
    await act.run(async () => {
      await del(base);
      nav("/you", { replace: true });
    }, "Deleted");
  };
  const react = (m: GroupMessage, emoji: string) =>
    act.run(async () => {
      const out = await put<GroupMessage>(`${base}/messages/${encodeURIComponent(m.id)}`, { reaction: m.my_reaction === emoji ? "" : emoji });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
    }, false);
  const star = (m: GroupMessage) =>
    act.run(async () => {
      const out = await put<GroupMessage>(`${base}/messages/${encodeURIComponent(m.id)}`, { starred: !m.starred });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
    }, m.starred ? "Unstarred" : "Starred");
  const markPaid = (m: GroupMessage, paid: boolean) =>
    act.run(async () => {
      const out = await post<GroupMessage>(`${base}/messages/${encodeURIComponent(m.id)}/paid`, { paid });
      setMessages((cur) => mergePeople(cur ?? [], [out]));
    }, paid ? "Marked as paid" : "Marked as not paid yet");

  const byID = new Map((messages ?? []).map((m) => [m.id, m]));
  const typingNames = (live.groupTyping.get(id) ?? []).map((p) => byPerson.get(p)?.name ?? "Someone");
  const sub = groupHeadWords(g, typingNames);
  const noPost = noPostWords(g);
  const names = members.filter((m) => !m.you).map((m) => m.name);
  const current = !!g && !g.left;

  return (
    <PayFromMessages>
      <section className="chat-pane person-pane group-pane" aria-label={`Group ${name}`} data-testid="group-pane" onKeyDown={composer.onPaneKeyDown} {...composer.dropProps}>
        <h1 className="sr-only">{g ? `Group ${g.name}` : "Group"}</h1>
        <p className="sr-only" aria-live="polite" data-testid="group-typing-live">
          {typingWords(typingNames)}
        </p>
        <ChatHead
          onBack={() => nav("/you")}
          menuLabel="More about this group"
          avatar={<ChatAvatar name={name} logo={g?.photo?.url} />}
          title={
            g ? (
              <Link to={groupInfoPath(id)} className="group-head-link" data-testid="group-head-info">
                {g.name}
              </Link>
            ) : (
              "Group"
            )
          }
          subtitle={sub ? <span className={typingNames.length ? "typing" : undefined} data-testid={typingNames.length ? "group-typing" : "group-head-line"}>{sub}</span> : undefined}
          items={
            g
              ? (close) => (
                  <>
                    <button type="button" role="menuitem" onClick={() => (close(), nav(groupInfoPath(id)))} data-testid="group-info">
                      Group info
                    </button>
                    <button
                      type="button"
                      role="menuitem"
                      onClick={() => (close(), nav(`/you/people/search?group=${encodeURIComponent(id)}&name=${encodeURIComponent(g.name)}`))}
                      data-testid="group-search"
                    >
                      Search in this group
                    </button>
                    {current && (
                      <button type="button" role="menuitem" onClick={() => (close(), void change({ muted: !g.muted }, g.muted ? "Notifications on" : "Muted"))} data-testid="group-mute">
                        {g.muted ? "Unmute notifications" : "Mute notifications"}
                      </button>
                    )}
                    <button
                      type="button"
                      role="menuitem"
                      onClick={() => {
                        close();
                        void change({ archived: !g.archived }, g.archived ? "Back in your chats" : "Archived").then((ok) => ok && !g.archived && nav("/you"));
                      }}
                    >
                      {g.archived ? "Unarchive" : "Archive"}
                    </button>
                    <button type="button" role="menuitem" onClick={() => (close(), nav(`${groupInfoPath(id)}?report=1`))} data-testid="group-report">
                      Report group
                    </button>
                    {current ? (
                      <button type="button" role="menuitem" onClick={() => (close(), void leave())} data-testid="group-leave">
                        Leave group
                      </button>
                    ) : null}
                    <button type="button" role="menuitem" onClick={() => (close(), void deleteForMe())} data-testid="group-delete">
                      {g.left ? "Delete group" : "Clear chat"}
                    </button>
                  </>
                )
              : undefined
          }
        />
        {problem && (
          <div className="notice pc-notice" role="alert" data-testid="group-problem">
            <span className="grow">
              {problem}
              {/Your standing/.test(problem) && (
                <>
                  {" "}
                  <Link to="/you/settings/standing">Open Your standing</Link>
                </>
              )}
            </span>
            <button type="button" className="small" onClick={() => setProblem(undefined)}>
              OK
            </button>
          </div>
        )}
        <ErrorBox error={sendError ?? act.error ?? pollError ?? group.error} />
        {at && atMissing && (
          <p className="notice" role="status" data-testid="group-at-missing" data-state={atMissing}>
            <span className="grow">{AT_FIND_WORDS[atMissing]}</span>
            {atMissing === "far" && (
              <>
                {" "}
                <button type="button" className="small" onClick={keepLooking} disabled={looking}>
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
          prefix="group"
          keyOf={(m) => m.id}
          startsRun={(prev, m) => !prev || prev.kind === "event" || prev.mine !== m.mine || prev.sender_id !== m.sender_id}
          empty={<>Say hello to {name}.</>}
          render={(m, { first }) => {
            if (m.kind === "event")
              return (
                <p className={`group-event${fromTaskiem(m) ? " taskiem" : ""}`} data-testid="group-event" data-type={m.event?.type} id={`m-${m.id}`}>
                  {fromTaskiem(m) && <Icon name="info" />}
                  <span>{m.body}</span>
                </p>
              );
            return (
              <GroupBubble
                m={m}
                first={first}
                quoted={m.reply_to ? byID.get(m.reply_to) : undefined}
                sender={m.sender_id ? byPerson.get(m.sender_id) : undefined}
                names={names}
                flash={thread.flashId === m.id}
                canPost={!!g?.can_post}
                current={current}
                busy={act.busy}
                onReply={() => composer.setReply(m)}
                onSaveAs={saveAs.fromWords}
                onSavePhoto={saveAs.fromPhoto}
                onReact={(e) => void react(m, e)}
                onStar={() => void star(m)}
                onPaid={(paid) => void markPaid(m, paid)}
                onSheet={(k) => setSheet({ k, m })}
                fileURL={groupFileURL(id, m.id)}
              />
            );
          }}
        />
        {noPost ? (
          <div className="customer-note stop group-closed" role="status" data-testid={g?.closed ? "group-closed" : g?.left ? "group-left" : "group-admins-only"}>
            <p>{noPost}</p>
            {g?.closed && g.role === "admin" && g.closed.can_appeal && !g.closed.appeal && (
              <Link className="button small" to={`${groupInfoPath(id)}#appeal`} data-testid="group-appeal-open">
                Appeal
              </Link>
            )}
            {g?.left && (
              <button type="button" className="small danger" onClick={() => void deleteForMe()} data-testid="group-left-delete">
                Delete group
              </button>
            )}
          </div>
        ) : (
          g && (
            <ComposerView
              c={composer}
              busy={false}
              prefix="group"
              textId="group-text"
              textLabel={`Message ${g.name}`}
              lead={
                <button type="button" className="composer-tools" aria-label="Share a card" data-testid="group-share-button" onClick={() => setSheet({ k: "share" })}>
                  <Icon name="grid" />
                </button>
              }
            />
          )
        )}
        {g && sheet?.k === "forward" && <ForwardSheet from={{ group_id: id }} m={sheet.m} onClose={() => setSheet(undefined)} />}
        {g && sheet?.k === "card" && <CardSheet base={base} m={sheet.m} onClose={() => setSheet(undefined)} />}
        {g && sheet?.k === "info" && <ReadBySheet base={base} m={sheet.m} onClose={() => setSheet(undefined)} />}
        {g && sheet?.k === "reactions" && <ReactionsSheet base={base} m={sheet.m} onClose={() => setSheet(undefined)} />}
        {g && sheet?.k === "share" && <ShareSheet onClose={() => setSheet(undefined)} onSend={(card) => sendOther({ card })} onSplit={() => setSheet({ k: "split" })} onPlace={() => setSheet({ k: "place" })} together={g.can_post ? { group_id: id } : undefined} />}
        {g && sheet?.k === "place" && <PlaceSheet name={g.name} onClose={() => setSheet(undefined)} onSend={(location) => sendOther({ location })} />}
        {g && sheet?.k === "split" && <SplitSheet name={g.name} onClose={() => setSheet(undefined)} onSend={(card) => sendOther({ card })} />}
        {g && sheet?.k === "delete" && (
          <DeleteAsk
            m={sheet.m}
            group
            mayEveryone={mayDeleteGroupForEveryone(g, sheet.m)}
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
    </PayFromMessages>
  );
}

/** One message of a group: who sent it (their name and photo, by their
 * settings), what it holds, the mentions marked, reactions counted, our
 * ticks, the warnings on a stranger's message. */
function GroupBubble({
  m,
  first,
  quoted,
  sender,
  names,
  flash,
  canPost,
  current,
  busy,
  onReply,
  onReact,
  onStar,
  onPaid,
  onSheet,
  fileURL,
  onSaveAs,
  onSavePhoto,
}: {
  m: GroupMessage;
  first: boolean;
  quoted?: GroupMessage;
  sender?: GroupMember;
  names: string[];
  flash: boolean;
  canPost: boolean;
  current: boolean;
  busy: boolean;
  onReply: () => void;
  /** Save as… on its words (saved objects S1, part 4). */
  onSaveAs: (text: string) => void;
  /** Pay this account and Save the account on a photo, read on this device. */
  onSavePhoto: (url: string) => void;
  onReact: (emoji: string) => void;
  onStar: () => void;
  onPaid: (paid: boolean) => void;
  onSheet: (k: "forward" | "card" | "delete" | "info" | "reactions") => void;
  fileURL: string;
}) {
  const fw = forwardWords(m);
  const card = !m.deleted ? m.card : undefined;
  const caption = m.deleted ? "" : m.attachment ? (["Photo", "Voice note", "File", m.attachment.name].includes(m.body) ? "" : m.body) : card || m.location ? "" : m.body;
  const warnTransfer = !m.mine && !!m.warn_transfer && !m.deleted;
  const warnScam = !m.mine && !!m.warn && !m.deleted;
  const reactions = m.reactions ?? [];
  const inline = !!caption && !warnTransfer && !warnScam && reactions.length === 0;
  const what = messageLine(m).split("\n")[0]?.slice(0, 60) ?? "message";
  const live = !m.local && !m.deleted;
  const who = m.mine ? "You" : m.sender;
  return (
    <BubbleShell
      id={m.id}
      mine={m.mine}
      first={first}
      testid="group-message"
      className={`${card ? "card-bubble" : ""}${reactions.length ? " has-reactions" : ""}${m.mentions_me ? " mentions-me" : ""}${flash ? " flash" : ""}`.trim()}
      what={`${who}: ${what}`}
      onReply={live && canPost ? onReply : undefined}
      quickReact={live && current}
      menu={
        live
          ? (close, more) => ({
              onPick: current ? (e: string) => (close(), onReact(e)) : undefined,
              reactions: current ? (
                <span className="reaction-picker" role="group" aria-label="React">
                  {REACTIONS.map((e) => (
                    <button
                      key={e}
                      type="button"
                      className="react-pick"
                      aria-pressed={m.my_reaction === e}
                      aria-label={`React with ${REACTION_NAMES[e]}`}
                      title={REACTION_NAMES[e]}
                      onClick={() => (close(), onReact(e))}
                    >
                      {e}
                    </button>
                  ))}
                  <button type="button" className="react-pick react-more" aria-label="More reactions" title="More reactions" onClick={more.pick}>
                    <Icon name="plus" />
                  </button>
                </span>
              ) : undefined,
              actions: (
                <>
                  {canPost && (
                    <PopItem icon="reply" onClick={() => (close(), onReply())}>
                      Reply
                    </PopItem>
                  )}
                  {!card && m.body && <CopyItem text={m.body} />}
                  {!card && m.body && <SaveAsItem text={m.body} onSave={onSaveAs} close={close} />}
                  {m.attachment?.kind === "photo" && <PhotoAccountItems url={fileURL} close={close} saveAccount={onSavePhoto} />}
                  {card?.type !== "split" && card?.type !== "basket" && (
                    <PopItem icon="forward" onClick={() => (close(), onSheet("forward"))}>
                      Forward
                    </PopItem>
                  )}
                  <PopItem icon="star" onClick={() => (close(), onStar())}>
                    {m.starred ? "Unstar" : "Star"}
                  </PopItem>
                  {m.mine && (
                    <PopItem icon="info" onClick={() => (close(), onSheet("info"))}>
                      Message info
                    </PopItem>
                  )}
                  <div className="pop-sep" role="separator" />
                  <PopItem icon="trash" danger onClick={() => (close(), onSheet("delete"))}>
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
            {m.mine && <PeopleTicks m={m} />}
          </span>
          {reactions.length > 0 && (
            <button type="button" className="reactions group-reactions" onClick={() => onSheet("reactions")} data-testid="group-reactions" aria-label={`Reactions: ${reactionCountWords(reactions, REACTION_NAMES)}. Who reacted`}>
              {reactions.map((r) => (
                <span key={r.reaction} className={`reaction-chip${m.my_reaction === r.reaction ? " mine" : ""}`} aria-hidden="true">
                  {r.reaction}
                  {r.count > 1 && <span className="count">{r.count}</span>}
                </span>
              ))}
            </button>
          )}
        </>
      }
    >
      {!m.mine && first && (
        <span className="who group-sender" data-testid="group-sender">
          <ChatAvatar name={m.sender} logo={sender?.person?.photo?.url} className="group-sender-photo" />
          <span>{m.sender}</span>
        </span>
      )}
      {m.mentions_me && (
        <span className="group-mentioned" data-testid="group-mentions-me">
          <span aria-hidden="true">@</span> Mentions you
        </span>
      )}
      {fw && (
        <span className="forwarded" data-testid="group-forwarded">
          <Icon name="forward" /> {fw}
        </span>
      )}
      {m.reply_to && (
        <span className="quote" data-testid="group-quote">
          <span className="quote-who">{quoted ? (quoted.mine ? "You" : quoted.sender) : "Reply"}</span>
          <span className="quote-text">{quoted ? messageLine(quoted) : "A message further up"}</span>
        </span>
      )}
      {m.deleted && (
        <span className="text people-deleted" data-testid="group-deleted">
          <Icon name="minus" /> {m.mine ? "You deleted this message" : "This message was deleted"}
        </span>
      )}
      {card && <PeopleCardView card={card} onOpen={card.type !== "split" ? () => onSheet("card") : undefined} mine={m.mine} onPaid={card.type === "split" && live && current ? onPaid : undefined} busy={busy} />}
      {m.attachment && !m.deleted && <AppAttachmentView a={m.attachment} src={m.local ? undefined : fileURL} preview={m.localPreview} who={who} mine={m.mine} />}
      {m.location && !m.deleted && <PlaceView place={m.location} />}
      {caption && (
        <span className="text">
          {mentionPieces(caption, names).map((p, i) =>
            p.mention ? (
              <mark key={i} className="group-mention" data-testid="group-mention">
                {p.text}
              </mark>
            ) : (
              <span key={i}>{p.text}</span>
            ),
          )}
          {inline && <span className={`stamp-gap${m.mine ? " mine" : ""}`} aria-hidden="true" />}
        </span>
      )}
      {warnTransfer && (
        <span className="pc-transfer-warn" role="note" data-testid="group-transfer-warning">
          <Icon name="warn" />
          <span>{PEOPLE_TRANSFER_WARNING}</span>
        </span>
      )}
      {warnScam && !warnTransfer && (
        <span className="pc-transfer-warn" role="note" data-testid="group-scam-warning">
          <Icon name="warn" />
          <span>{SCAM_WARNING}</span>
        </span>
      )}
    </BubbleShell>
  );
}

/** "Read by" on our own message (A6): only members with read ticks on,
 * with when; who has it; how many don't yet. */
function ReadBySheet({ base, m, onClose }: { base: string; m: GroupMessage; onClose: () => void }) {
  const info = useLoad(() => get<GroupMessageInfo>(`${base}/messages/${encodeURIComponent(m.id)}/info`), [base, m.id]);
  const i = info.data;
  return (
    <Sheet title="Message info" onClose={onClose} testid="group-read-by">
      {!i && !info.error && <Skeleton rows={2} label="Loading who read it…" />}
      <ErrorBox error={info.error} />
      {i && (
        <div className="pc-check">
          <p className="hint" role="status">
            {readByWords(i)}
          </p>
          <h3>Read by</h3>
          {i.read_by.length === 0 ? (
            <p className="hint">No one yet. Only people with read ticks on show here.</p>
          ) : (
            <ul className="group-readers" data-testid="group-read-by-list">
              {i.read_by.map((r) => (
                <li key={r.person_id}>
                  <span className="grow">{r.name}</span>
                  {r.at && <time dateTime={r.at}>{fmtTime(r.at)}</time>}
                </li>
              ))}
            </ul>
          )}
          <h3>Delivered to</h3>
          {i.delivered_to.length === 0 ? (
            <p className="hint">No one else.</p>
          ) : (
            <ul className="group-readers" data-testid="group-delivered-list">
              {i.delivered_to.map((r) => (
                <li key={r.person_id}>
                  <span className="grow">{r.name}</span>
                  {r.at && <time dateTime={r.at}>{fmtTime(r.at)}</time>}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Sheet>
  );
}

/** Who reacted to a message, and with what. */
function ReactionsSheet({ base, m, onClose }: { base: string; m: GroupMessage; onClose: () => void }) {
  const list = useLoad(() => get<{ reactions: { person_id: string; name: string; reaction: string; at: string }[] }>(`${base}/messages/${encodeURIComponent(m.id)}/reactions`), [base, m.id]);
  return (
    <Sheet title="Reactions" onClose={onClose} testid="group-reactions-sheet">
      {!list.data && !list.error && <Skeleton rows={2} label="Loading reactions…" />}
      <ErrorBox error={list.error} />
      {list.data && (
        <ul className="group-readers">
          {list.data.reactions.map((r) => (
            <li key={r.person_id}>
              <span className="grow">{r.name}</span>
              <span>
                <span aria-hidden="true">{r.reaction}</span>
                <span className="sr-only">{REACTION_NAMES[r.reaction] ?? r.reaction}</span>
              </span>
            </li>
          ))}
        </ul>
      )}
    </Sheet>
  );
}
