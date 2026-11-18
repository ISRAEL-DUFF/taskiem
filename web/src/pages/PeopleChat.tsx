import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError, del, get, post, put, upload } from "../api";
import { Icon } from "../icons";
import { KitField, Pill, Sheet } from "../kit";
import { getDraft } from "../lib/drafts";
import { naira, toKobo } from "../lib/invoices";
import { clock, REACTIONS } from "../lib/linkchat";
import {
  accountDigits,
  canSendTo,
  cardKind,
  forwardCap,
  forwardWords,
  KEPT_WORDS,
  lastSeenWords,
  mayDeleteForEveryone,
  mergePeople,
  messageLine,
  peopleFileURL,
  phoneWords,
  photoOf,
  REPORT_CONSENT,
  SCAM_WARNING,
  sendProblem,
  splitTitle,
  tickState,
  TICK_WORDS,
  withLatestPeople,
  type CardAsk,
  type ChatList,
  type OpenedCard,
  type PayoutAccount,
  type PeopleCard,
  type PeopleChat,
  type PeopleMessage,
} from "../lib/people";
import { clientFor, mayHaveArrived, REPORT_REASONS, TRANSFER_WARNING, type KeptSend, type ShopItem } from "../lib/personchat";
import { useThread } from "../lib/useThread";
import type { PersonOrders } from "../lib/youorders";
import { EmptyState, ErrorBox, fmtTime, Items, Modal, Row, Skeleton, useAction, useLoad } from "../ui";
import { AppAttachmentView } from "./AppCards";
import { BubbleShell, CopyItem, PopItem } from "./BubbleShell";
import { ChatAvatar } from "./ChatAvatar";
import { BUSINESS_FILES, ComposerView, useComposer } from "./ChatComposer";
import { ChatHead } from "./ChatHead";
import { ChatStream } from "./ChatStream";
import { useConfirm } from "./ConfirmSheet";

// A chat with a person on You (docs/spec/people-chatting.md §7, §8, §10.1,
// §11): the messages with their ticks, replies, reactions, stars, forwards,
// deleting, photos and files, the warnings on strangers' messages, a
// request's Accept, Delete and Block and report, cards shared live and
// "Your share" cards. It looks and behaves as a chat with a business does.

const POLL_MS = 4000;

const REACTION_NAMES: Record<string, string> = { "👍": "thumbs up", "❤️": "heart", "😂": "laughing", "😮": "surprise", "😢": "sad", "🙏": "thanks" };

const draftKey = (id: string) => `people:${id}`;

let localN = 0;

/** A photo as a data URL, for its preview while it is sent. */
const asDataURL = (f: File) =>
  new Promise<string | undefined>((done) => {
    if (!f.type.startsWith("image/")) return done(undefined);
    const r = new FileReader();
    r.onload = () => done(typeof r.result === "string" ? r.result : undefined);
    r.onerror = () => done(undefined);
    r.readAsDataURL(f);
  });

/** The ticks under a message of ours: one until it is delivered, two once
 * it is, coloured only when the answer says they read it. */
export function PeopleTicks({ m }: { m: Pick<PeopleMessage, "local" | "status"> }) {
  const t = tickState(m);
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
  const base = `/v1/me/people/${encodeURIComponent(id)}`;
  const chat = useLoad(() => get<PeopleChat>(base), [base]);
  const c = chat.data;
  const [messages, setMessages] = useState<PeopleMessage[]>();
  const [hasOlder, setHasOlder] = useState(false);
  const [pollError, setPollError] = useState<unknown>();
  const [problem, setProblem] = useState<string>();
  const [sendError, setSendError] = useState<unknown>();
  const kept = useRef<KeptSend | undefined>(undefined);
  const [sheet, setSheet] = useState<
    | { k: "info" | "report" | "share" | "split"; blockToo?: boolean }
    | { k: "forward"; m: PeopleMessage }
    | { k: "card"; m: PeopleMessage }
    | { k: "delete"; m: PeopleMessage }
    | undefined
  >();
  const act = useAction();
  const sure = useConfirm();
  const loadingOlder = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const thread = useThread({ items: messages, isMine: (m) => m.mine, hasOlder, loadOlder: olderCb });

  const pull = useCallback(
    async (first: boolean) => {
      const out = await get<{ messages: PeopleMessage[]; more: boolean }>(`${base}/messages`);
      setMessages((cur) => (first ? out.messages : withLatestPeople(cur ?? [], out.messages, out.more)));
      if (first) setHasOlder(out.more);
      setPollError(undefined);
      // Read marks: a request's reading tells the sender nothing (§7).
      if (document.visibilityState === "visible" && out.messages.some((m) => !m.mine && !m.read_at)) await post(`${base}/read`).catch(() => undefined);
    },
    [base],
  );
  const reloadChat = chat.reload;
  useEffect(() => {
    setMessages(undefined);
    thread.reset();
    pull(true).catch(setPollError);
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      pull(false).catch(setPollError);
      reloadChat();
    }, POLL_MS);
    return () => clearInterval(t);
  }, [base, pull]);
  loadOlderRef.current = () => {
    const oldest = messages?.find((m) => !m.local);
    if (!oldest || loadingOlder.current) return;
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
      if (c?.request) reloadChat();
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
      return true;
    } catch (e) {
      const said = sendProblem(e);
      if (said) setProblem(said);
      else throw e;
      return true;
    }
  };

  const closed = !c ? null : c.gone ? "This account was deleted, so this chat takes no more messages." : null;
  const composer = useComposer<PeopleMessage>({
    draftKey: draftKey(id),
    rules: BUSINESS_FILES,
    sources: { "@": [], "#": [] },
    replyLine: (m) => messageLine(m),
    onSend: (s) => {
      setSendError(undefined);
      void send(s.text, s.file, s.reply);
    },
    enabled: !!c && !c.blocked && !c.gone && !c.request,
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
  const seen = lastSeenWords(c?.person);
  const sub = c?.gone ? "" : [seen, c?.phone ? phoneWords(c.phone) : ""].filter(Boolean).join(" · ");

  return (
    <section className="chat-pane person-pane" aria-label={`Chat with ${name}`} data-testid="people-pane" onKeyDown={composer.onPaneKeyDown} {...composer.dropProps}>
      <h1 className="sr-only">{c ? `Chat with ${c.name}` : "Chat"}</h1>
      <ChatHead
        onBack={() => nav(c?.request ? "/you/people/requests" : "/you")}
        menuLabel="More about this chat"
        avatar={<ChatAvatar name={name} logo={photoOf(c?.person)} />}
        title={c?.name ?? "Chat"}
        subtitle={sub ? <span data-testid="people-head-line">{sub}</span> : undefined}
        items={
          c
            ? (close) => (
                <>
                  <button type="button" role="menuitem" onClick={() => (close(), setSheet({ k: "info" }))} data-testid="people-info">
                    Contact info
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
          <span className="grow">{problem}</span>
          <button type="button" className="small" onClick={() => setProblem(undefined)}>
            OK
          </button>
        </div>
      )}
      <ErrorBox error={sendError ?? act.error ?? pollError ?? chat.error} />
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
              className={`${card ? "card-bubble" : ""}${m.my_reaction || m.their_reaction ? " has-reactions" : ""}`.trim()}
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
                          {card?.type !== "split" && !c?.request && (
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
                    {m.mine && <PeopleTicks m={m} />}
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
                  onPaid={card.type === "split" && live && !c?.gone ? (paid) => void markPaid(m, paid) : undefined}
                  busy={act.busy}
                />
              )}
              {m.attachment && !m.deleted && <AppAttachmentView a={m.attachment} src={m.local ? undefined : peopleFileURL(id, m.id)} preview={m.localPreview} who={name} mine={m.mine} />}
              {m.location && !m.deleted && (
                <a className="chat-file" href={`https://www.openstreetmap.org/?mlat=${m.location.lat}&mlon=${m.location.lng}#map=17/${m.location.lat}/${m.location.lng}`} target="_blank" rel="noopener noreferrer">
                  <Icon name="pin" />
                  <span className="name">{m.location.label || "A place"}</span>
                </a>
              )}
              {caption && (
                <span className="text">
                  {caption}
                  {inline && <span className={`stamp-gap${m.mine ? " mine" : ""}`} aria-hidden="true" />}
                </span>
              )}
              {warnTransfer && (
                <span className="pc-transfer-warn" role="note" data-testid="people-transfer-warning">
                  <Icon name="warn" />
                  <span>{TRANSFER_WARNING}</span>
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
      {c?.waiting && !c.request && (
        <p className="pc-push-line" role="status" data-testid="people-waiting">
          Your message waits for {c.name} to accept it.
        </p>
      )}
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
      {c && sheet?.k === "forward" && <ForwardSheet chat={id} m={sheet.m} onClose={() => setSheet(undefined)} />}
      {c && sheet?.k === "card" && <CardSheet chat={id} m={sheet.m} onClose={() => setSheet(undefined)} />}
      {c && sheet?.k === "share" && <ShareSheet onClose={() => setSheet(undefined)} onSend={shareCard} onSplit={() => setSheet({ k: "split" })} />}
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
    </section>
  );
}

/** A card in a people chat: a product, a business, an order or a delivery
 * (opened live), or a "Your share" card with Mark as paid. */
export function PeopleCardView({ card, onOpen, onPaid, busy }: { card: PeopleCard; onOpen?: () => void; onPaid?: (paid: boolean) => void; busy?: boolean }) {
  const split = card.type === "split";
  const title = split ? splitTitle(card, naira) : card.title;
  const line = split
    ? card.pay_to
      ? `Pay to ${card.pay_to.account_name}, ${card.pay_to.bank} ${card.pay_to.account_number}`
      : ""
    : [card.business && card.business !== card.title ? card.business : "", card.price_kobo !== undefined ? naira(card.price_kobo) : "", card.reference ?? "", card.area ?? ""].filter(Boolean).join(" · ");
  const icon = { product: "tag", business: "store", order: "orders", delivery: "package", split: "split" }[card.type];
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
      {(onOpen || onPaid) && (
        <span className="tk-ref-foot">
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
function DeleteAsk({ m, onClose, onDelete }: { m: PeopleMessage; onClose: () => void; onDelete: (forEveryone: boolean) => void }) {
  const everyone = mayDeleteForEveryone(m);
  return (
    <Modal title="Delete this message?" onClose={onClose}>
      <p>{everyone ? "Deleting it for everyone takes it from their phone too. A copy or a screenshot they made before stays." : "It goes from your chat. They keep their copy."}</p>
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
          <Icon name="lock" /> {KEPT_WORDS}
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

/** Forward one message to up to five people chats (one for a message
 * forwarded many times). */
function ForwardSheet({ chat, m, onClose }: { chat: string; m: PeopleMessage; onClose: () => void }) {
  const list = useLoad(() => get<{ chats: PeopleChat[] }>("/v1/me/people"), []);
  const [picked, setPicked] = useState<string[]>([]);
  const [failed, setFailed] = useState<string[]>([]);
  const act = useAction();
  const cap = forwardCap(m);
  const chats = (list.data?.chats ?? []).filter((x) => x.id !== chat && canSendTo(x));
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
              const r = await post<{ sent: unknown[]; failed: { chat_id: string; error: string }[] }>("/v1/me/people/forward", { chat_id: chat, message_id: m.id, to: picked });
              if (r.failed.length) setFailed(r.failed.map((f) => `${chats.find((x) => x.id === f.chat_id)?.name ?? "A chat"}: ${f.error}`));
              else onClose();
            }, picked.length === 1 ? "Forwarded" : `Forwarded to ${picked.length} chats`)
          }
        >
          Forward
        </button>
      }
    >
      {!list.data && !list.error && <Skeleton rows={2} label="Loading your chats…" />}
      <ErrorBox error={list.error ?? act.error} />
      {failed.length > 0 && (
        <ul className="error" role="alert">
          {failed.map((f) => (
            <li key={f}>{f}</li>
          ))}
        </ul>
      )}
      {list.data && chats.length === 0 && <p className="hint">No other chats with people yet.</p>}
      <fieldset className="pc-choice">
        <legend className="sr-only">Chats</legend>
        {chats.map((x) => {
          const on = picked.includes(x.id);
          return (
            <label key={x.id} className="switch-line">
              <input
                type="checkbox"
                checked={on}
                disabled={!on && picked.length >= cap}
                onChange={(e) => setPicked((cur) => (e.target.checked ? [...cur, x.id] : cur.filter((y) => y !== x.id)))}
                data-testid="people-forward-pick"
              />{" "}
              <span>{x.name}</span>
            </label>
          );
        })}
      </fieldset>
    </Sheet>
  );
}

/** A shared card opened: read live, as the person opening it. */
function CardSheet({ chat, m, onClose }: { chat: string; m: PeopleMessage; onClose: () => void }) {
  const view = useLoad(() => get<OpenedCard>(`/v1/me/people/${encodeURIComponent(chat)}/messages/${encodeURIComponent(m.id)}/card`), [chat, m.id]);
  const o = view.data;
  const card = o?.card ?? m.card;
  const blocked = view.error instanceof ApiError && view.error.status === 403;
  const shopPath = card?.business_id ? `/you/chats/new?business=${encodeURIComponent(card.business_id)}&from=shop` : undefined;
  return (
    <Sheet title={card ? card.title : "Card"} onClose={onClose} testid="people-card-sheet">
      {!o && !view.error && <Skeleton rows={2} label="Loading what was shared…" />}
      {blocked ? <p role="status">This business is not taking chats from you, so it can't be opened.</p> : <ErrorBox error={view.error} />}
      {o && (
        <div className="pc-check" data-testid="people-card-live">
          {o.live.product && (
            <>
              <p>
                <strong>{o.live.product.name}</strong>
              </p>
              {o.live.product.price_kobo !== undefined && <p data-testid="people-card-price">{naira(o.live.product.price_kobo)} now</p>}
              <p className="hint">{o.live.product.available ? "Available" : "Not available right now"}</p>
            </>
          )}
          {o.live.business && (
            <>
              <p>
                <strong>{o.live.business.name}</strong>
              </p>
              {o.live.business.tagline && <p>{o.live.business.tagline}</p>}
              <p className="hint">{[o.live.business.area, o.live.business.open_now === undefined ? "" : o.live.business.open_now ? "Open now" : "Closed now"].filter(Boolean).join(" · ")}</p>
            </>
          )}
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
                {o.card.state ? ` · ${o.card.state}` : ""}. This is what was shared: only the person who ordered sees more.
              </p>
            )
          )}
          {o.live.delivery && <p>{o.live.delivery.line}</p>}
          {shopPath && (o.card.type === "product" || o.card.type === "business") && (o.live.business?.takes_chats ?? true) && (
            <Link className="button primary" to={shopPath} data-testid="people-card-chat">
              Chat with {o.card.business ?? o.live.business?.name ?? "them"}
            </Link>
          )}
        </div>
      )}
    </Sheet>
  );
}

/** Share into the chat: a business, a product, one of your orders, or
 * "Your share". */
function ShareSheet({ onClose, onSend, onSplit }: { onClose: () => void; onSend: (c: CardAsk) => Promise<boolean>; onSplit: () => void }) {
  const [step, setStep] = useState<"menu" | "business" | "product" | "order" | { items: string; business: string; name: string }>("menu");
  const act = useAction();
  const go = (card: CardAsk) => void act.run(async () => (await onSend(card)) && onClose(), "Shared");
  const chats = useLoad(() => (step === "business" || step === "product" ? get<ChatList>("/v1/me/conversations") : Promise.resolve(undefined)), [step === "business" || step === "product"]);
  const orders = useLoad(() => (step === "order" ? get<PersonOrders>("/v1/me/orders?limit=30") : Promise.resolve(undefined)), [step === "order"]);
  const items = useLoad(
    () => (typeof step === "object" ? get<{ items: ShopItem[] }>(`/v1/me/chats/${encodeURIComponent(step.items)}/shop`) : Promise.resolve(undefined)),
    [typeof step === "object" ? step.items : ""],
  );
  const businesses = (chats.data?.chats ?? []).filter((x) => x.kind === "business" && x.business_id);
  return (
    <Sheet title={typeof step === "object" ? step.name : "Share"} onClose={onClose} onBack={step === "menu" ? undefined : () => setStep("menu")} testid="people-share-sheet">
      <ErrorBox error={act.error ?? chats.error ?? orders.error ?? items.error} />
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
          <Row icon="split" title="Your share" sub={<span className="sub">Ask them to pay you back</span>}>
            <button type="button" onClick={onSplit} data-testid="share-split">
              Make one
            </button>
          </Row>
        </Items>
      )}
      {(step === "business" || step === "product") && (
        <>
          {!chats.data && !chats.error && <Skeleton rows={2} label="Loading your businesses…" />}
          {chats.data && businesses.length === 0 && <EmptyState icon="store">Businesses you chat with show here.</EmptyState>}
          <Items label="Businesses">
            {businesses.map((b) => (
              <Row key={b.id} letter={b.name} title={b.name} testid="share-business-row">
                <button
                  type="button"
                  disabled={act.busy}
                  onClick={() => (step === "business" ? go({ type: "business", business_id: b.business_id }) : setStep({ items: b.id, business: b.business_id ?? "", name: b.name }))}
                >
                  {step === "business" ? "Share" : "Their products"}
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
      {step === "order" && (
        <>
          {!orders.data && !orders.error && <Skeleton rows={2} label="Loading your orders…" />}
          {orders.data && orders.data.orders.filter((o) => !o.earlier).length === 0 && <EmptyState icon="orders">Your orders show here.</EmptyState>}
          <Items label="Your orders">
            {(orders.data?.orders ?? [])
              .filter((o) => !o.earlier)
              .map((o) => (
                <Row key={`${o.business_id}/${o.reference}`} icon="orders" title={`${o.business} · ${o.reference}`} sub={<span className="sub">{o.summary}</span>} testid="share-order-row">
                  <button type="button" disabled={act.busy} onClick={() => go({ type: "order", business_id: o.business_id, reference: o.reference })}>
                    Share<span className="sr-only"> {o.reference}</span>
                  </button>
                  <button type="button" disabled={act.busy} onClick={() => go({ type: "delivery", business_id: o.business_id, reference: o.reference })}>
                    Its delivery<span className="sr-only"> of {o.reference}</span>
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
function SplitSheet({ name, onClose, onSend }: { name: string; onClose: () => void; onSend: (c: CardAsk) => Promise<boolean> }) {
  const saved = useLoad(() => get<{ account: PayoutAccount | null; words: string }>("/v1/me/payout-account"), []);
  const [amount, setAmount] = useState("");
  const [what, setWhat] = useState("");
  const [other, setOther] = useState(false);
  const [acct, setAcct] = useState<PayoutAccount>({ bank: "", account_number: "", account_name: "" });
  const act = useAction();
  const kobo = toKobo(amount);
  const typed = other || (saved.data && !saved.data.account);
  const okAccount = !typed || (acct.bank.trim() && acct.account_name.trim() && accountDigits(acct.account_number));
  const ok = kobo !== undefined && !Number.isNaN(kobo) && kobo > 0 && !!okAccount;
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
              const card: CardAsk = { type: "split", amount_kobo: kobo, what: what.trim() || undefined };
              if (typed) card.pay_to = { bank: acct.bank.trim(), account_name: acct.account_name.trim(), account_number: accountDigits(acct.account_number) };
              if (await onSend(card)) onClose();
            }, "Sent")
          }
        >
          Send to {name}
        </button>
      }
    >
      <div className="pc-check">
        <p className="hint">Taskiem moves no money. {name} pays you with their bank app, and either of you marks it paid.</p>
        <KitField label="Amount (₦)">
          <input inputMode="decimal" value={amount} onChange={(e) => setAmount(e.target.value)} data-testid="split-amount" />
        </KitField>
        <KitField label="What for" hint="Optional">
          <input value={what} maxLength={120} onChange={(e) => setWhat(e.target.value)} data-testid="split-what" />
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
