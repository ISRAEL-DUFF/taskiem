import { useCallback, useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { ApiError, del, get, post, put, upload } from "../api";
import { Icon } from "../icons";
import { Pill } from "../kit";
import { naira } from "../lib/billing";
import { appNote, appThreadLine, mayOffer, type CardAsk } from "../lib/customerlinks";
import { clientFor, erasedWords, mayHaveArrived, type AppCard, type KeptSend } from "../lib/personchat";
import {
  customerKey,
  customerLetter,
  type CustomerHit,
  customerTitle,
  LATE_REPLY_MAX,
  lateLength,
  lateTooLong,
  latestPage,
  mergeCustomer,
  owesWords,
  phoneWords,
  replyNote,
  type CustomerChat,
  type CustomerMessage,
} from "../lib/customerchat";
import { getDraft, setDraft } from "../lib/drafts";
import { clock, listTime } from "../lib/linkchat";
import { useAutoGrow } from "../lib/useAutoGrow";
import { useThread } from "../lib/useThread";
import { ErrorBox, fmtTime, useAction, useLoad } from "../ui";
import { AppAttachmentView, AppCardView } from "./AppCards";
import { BubbleShell, Ticks } from "./BubbleShell";
import { ChatAvatar } from "./ChatAvatar";
import { BUSINESS_FILES } from "./ChatComposer";
import { ChatHead } from "./ChatHead";
import { useConfirm } from "./ConfirmSheet";
import { CardSheet } from "./CustomerCards";
import { ChatRow } from "./ChatRow";
import { ChatStream } from "./ChatStream";
import { CustomerOrders } from "./CustomerOrders";

// Customers' conversations in the Chat page (docs/spec/chat-five-more-flows.md,
// phase 1): the list's Customers section and a customer's thread. A reply
// goes from the business's own WhatsApp number.

/** How often an open thread and the list look for what is new. */
const POLL_MS = 4000;

/** What the clip of an app thread offers: photos, PDFs and voice notes. */
const APP_FILES_ACCEPT = `${BUSINESS_FILES.accept},audio/*`;

/** A card's own button on the business's side: the order, invoice or
 * order of a delivery it is about. */
function cardLink(card: AppCard): string | null {
  if (!card.reference) return null;
  if (card.type === "invoice") return `/invoices/${encodeURIComponent(card.reference)}`;
  if (card.type === "order" || card.type === "pay" || card.type === "delivery" || card.type === "booking") return `/storefront/orders/${encodeURIComponent(card.reference)}`;
  return null;
}

/** The Customers section of the chat list: one row per customer's thread
 * (the archived ones under Archived), with an unsent reply as a draft. */
export function CustomerRows({
  chats,
  openKey,
  drafts,
  archived = false,
}: {
  chats: CustomerChat[];
  openKey: string | null;
  drafts: Record<string, string>;
  archived?: boolean;
}) {
  if (chats.length === 0) return null;
  const waiting = chats.filter((c) => c.unread > 0).length;
  const head = archived ? "Archived customers" : "Customers";
  return (
    <section aria-labelledby="customers-head">
      <div className="toolbar" style={{ marginBottom: 6 }}>
        <h2 id="customers-head" className="grow" style={{ margin: 0 }}>
          {head}
          {waiting > 0 && !archived && (
            <span className="customers-waiting" data-testid="customers-waiting">
              {" "}
              · {waiting === 1 ? "1 waiting" : `${waiting} waiting`}
            </span>
          )}
        </h2>
      </div>
      <ul className="chat-rows" aria-label={head} data-testid="customer-chats">
        {chats.map((c) => {
          const draft = drafts[`customer:${c.id}`];
          return (
            <ChatRow
              key={c.id}
              to={`/chat?with=${customerKey(c.id)}`}
              letter={customerLetter(c)}
              title={
                <>
                  {customerTitle(c)}
                  {c.line === "app" && (
                    <span className="line-mark" data-testid="in-the-app">
                      {" "}
                      · in the app
                    </span>
                  )}
                </>
              }
              time={listTime(c.last_at)}
              preview={
                draft ? (
                  <>
                    <span className="draft">Draft:</span> {draft}
                  </>
                ) : c.stopped ? (
                  c.line === "app" ? "Stopped messages" : "Sent STOP"
                ) : (
                  `${c.last_mine ? "You: " : ""}${c.last}`
                )
              }
              unread={c.unread}
              selected={openKey === customerKey(c.id)}
              testid="customer-chat"
            />
          );
        })}
      </ul>
    </section>
  );
}

/** What a search finds among customers: by name or number, and in the
 * latest of their messages. */
export function CustomerSearch({
  query,
  openKey,
}: {
  query: string;
  openKey: string | null;
}) {
  const found = useLoad(
    () =>
      query.length >= 2
        ? get<{ chats: CustomerChat[]; messages: CustomerHit[] }>(
            `/v1/customer-chats/search?q=${encodeURIComponent(query)}`,
          )
        : Promise.resolve({ chats: [], messages: [] }),
    [query],
  );
  const chats = found.data?.chats ?? [];
  const msgs = found.data?.messages ?? [];
  if (chats.length === 0 && msgs.length === 0) return null;
  return (
    <section aria-label="Customers found" data-testid="customer-results">
      {chats.length > 0 && (
        <>
          <h2>Customers</h2>
          <ul className="chat-rows" aria-label="Customers found">
            {chats.map((c) => (
              <ChatRow
                key={c.id}
                to={`/chat?with=${customerKey(c.id)}`}
                letter={customerLetter(c)}
                title={customerTitle(c)}
                time={listTime(c.last_at)}
                preview={c.last}
                unread={c.unread}
                selected={openKey === customerKey(c.id)}
                testid="customer-found"
              />
            ))}
          </ul>
        </>
      )}
      {msgs.length > 0 && (
        <>
          <h2>In customers' messages</h2>
          <ul className="chat-rows" aria-label="Customers' messages found">
            {msgs.map((m) => (
              <ChatRow
                key={m.message_id}
                to={`/chat?with=${customerKey(m.chat_id)}`}
                letter={m.name}
                title={/^\+\d/.test(m.name) ? phoneWords(m.name) : m.name}
                time={listTime(m.created_at)}
                preview={`${m.mine ? "You: " : ""}${m.body}`}
                testid="customer-message-found"
              />
            ))}
          </ul>
        </>
      )}
    </section>
  );
}

/** How many customers are waiting for an answer (the Chat tab's count),
 * for those who handle orders. */
export function useCustomerUnread(on: boolean): number {
  const [n, setN] = useState(0);
  useEffect(() => {
    if (!on) {
      setN(0);
      return;
    }
    let live = true;
    const look = () =>
      get<{ chats: number; messages: number }>(
        "/v1/customer-chats/unread",
      ).then(
        (r) => live && setN(r.chats),
        () => undefined,
      );
    void look();
    const t = setInterval(
      () => document.visibilityState === "visible" && void look(),
      15_000,
    );
    return () => {
      live = false;
      clearInterval(t);
    };
  }, [on]);
  return n;
}

/** The customers' threads, followed while the page is seen. */
export function useCustomerChats(on: boolean, archived = false) {
  const list = useLoad(
    () =>
      on
        ? get<{ chats: CustomerChat[] }>(
            `/v1/customer-chats${archived ? "?archived=1" : ""}`,
          ).then((x) => x.chats)
        : Promise.resolve([] as CustomerChat[]),
    [on, archived],
  );
  const reload = list.reload;
  useEffect(() => {
    if (!on) return;
    const t = setInterval(
      () => document.visibilityState === "visible" && reload(),
      POLL_MS * 2,
    );
    return () => clearInterval(t);
  }, [on, reload]);
  return list;
}

let localN = 0;

/** A customer's thread: what they wrote and the business's replies, and
 * the box to reply from. */
export function CustomerPane({
  id,
  onBack,
  onChanged,
}: {
  id: string;
  onBack: () => void;
  onChanged: () => void;
}) {
  const chat = useLoad(
    () => get<CustomerChat>(`/v1/customer-chats/${id}`),
    [id],
  );
  const [messages, setMessages] = useState<CustomerMessage[]>();
  const [hasOlder, setHasOlder] = useState(false);
  // What the last look for news ran into (gone once a look works), and
  // what a send ran into, apart.
  const [pollError, setPollError] = useState<unknown>();
  const [sendError, setSendError] = useState<unknown>();
  // The customer's details were erased: the conversation is closed.
  const [erased, setErased] = useState<string>();
  // A reply whose answer never came, its words back in the box: sent
  // again as they are, it keeps its client_id, so it is not sent twice.
  const kept = useRef<KeptSend | undefined>(undefined);
  const act = useAction();
  const loadingOlder = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const thread = useThread({
    items: messages,
    isMine: (m) => m.mine,
    hasOlder,
    loadOlder: olderCb,
  });
  const changed = useRef(onChanged);
  changed.current = onChanged;

  // The latest page, then what is new every few seconds; the customer's
  // messages are marked read as they are shown.
  const pull = useCallback(
    async (first: boolean) => {
      const out = await get<{ messages: CustomerMessage[]; more: boolean }>(
        `/v1/customer-chats/${id}/messages`,
      );
      // The latest page replaces what it covers (a message gone after STOP
      // goes from here too); older pages loaded above, and sends still
      // under way, stay.
      setMessages((cur) =>
        first ? out.messages : latestPage(cur ?? [], out.messages, out.more),
      );
      if (first) setHasOlder(out.more);
      setPollError(undefined);
      if (out.messages.some((m) => !m.mine && !m.read_at)) {
        await post(`/v1/customer-chats/${id}/read`).catch(() => undefined);
        changed.current();
      }
    },
    [id],
  );
  useEffect(() => {
    setMessages(undefined);
    thread.reset();
    pull(true).catch(setPollError);
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      pull(false).catch(setPollError);
      chat.reload();
    }, POLL_MS);
    return () => clearInterval(t);
    // The thread's reset and chat.reload are stable.
  }, [id, pull]);
  loadOlderRef.current = () => {
    const oldest = messages?.[0];
    if (!oldest || loadingOlder.current) return;
    loadingOlder.current = true;
    get<{ messages: CustomerMessage[]; more: boolean }>(
      `/v1/customer-chats/${id}/messages?before=${encodeURIComponent(oldest.id)}`,
    )
      .then((out) => {
        thread.keepPlace();
        setMessages((cur) => mergeCustomer(cur ?? [], out.messages));
        setHasOlder(out.more);
      })
      .catch(setPollError)
      .finally(() => (loadingOlder.current = false));
  };

  const draftKey = `customer:${id}`;
  const [text, setText] = useState(() => getDraft(draftKey));
  useEffect(() => setText(getDraft(draftKey)), [draftKey]);
  useEffect(() => setDraft(draftKey, text), [draftKey, text]);
  const box = useRef<HTMLTextAreaElement>(null);
  useAutoGrow(box, text);

  const c = chat.data;
  // A customer in the Taskiem app (taskiem-for-everyone §8.3): no 24-hour
  // window, cards, files, typing and blocks.
  const app = c?.line === "app";
  const note = erased ? { tone: "stop" as const, text: erased } : c ? (app ? appNote(c) : replyNote(c)) : null;
  const canSend = !!c && !c.stopped && !c.blocked && (app || !!c.phone) && !erased;
  const tooLong = !!c && !app && lateTooLong(text, c.window_open);
  const [cards, setCards] = useState(false);
  const [asOffer, setAsOffer] = useState(false);
  const filePick = useRef<HTMLInputElement>(null);
  const sure = useConfirm();
  const lastTyped = useRef(0);
  const typed = (v: string) => {
    setText(v);
    // Refused while either side blocked the other (403, 409): nothing to say.
    if (!app || !canSend || !v.trim() || Date.now() - lastTyped.current < 4000) return;
    lastTyped.current = Date.now();
    void post(`/v1/customer-chats/${id}/typing`).catch(() => undefined);
  };
  // Each send is its own (its own client_id): a card or a file chosen while
  // another message is on its way goes too, never dropped.
  const send = (extra?: { card?: CardAsk; cardWords?: string; file?: File }) => {
    const words = extra?.card ? (extra.cardWords ?? "") : text.trim();
    if ((!words && !extra?.file && !extra?.card) || !canSend) return;
    const plain = !extra?.card && !extra?.file;
    const fresh = () => `${Date.now()}-${++localN}`;
    const client = plain ? clientFor(kept.current, words, fresh) : fresh();
    if (plain) kept.current = undefined;
    const file = extra?.file;
    const offer = !extra?.card && !file && asOffer && mayOffer(c ?? {});
    const mine: CustomerMessage = {
      id: `local-${client}`,
      mine: true,
      kind: file ? "media" : extra?.card ? "card" : "text",
      body: words,
      attachment: file ? { kind: file.type.startsWith("image/") ? "photo" : file.type.startsWith("audio/") ? "voice" : "file", type: file.type, size: file.size, name: file.name } : undefined,
      offer,
      read_at: null,
      created_at: new Date().toISOString(),
      local: true,
    };
    setMessages((cur) => mergeCustomer(cur ?? [], [mine]));
    if (plain) setText("");
    setSendError(undefined);
    void (async () => {
      try {
        let out: CustomerMessage;
        if (file) {
          const form = new FormData();
          form.append("file", file);
          if (words) form.append("text", words);
          form.append("client_id", client);
          out = await upload<CustomerMessage>(`/v1/customer-chats/${id}/files`, form);
        } else
          out = await post<CustomerMessage>(
            `/v1/customer-chats/${id}/messages`,
            extra?.card
              ? { card: extra.card, client_id: client }
              : {
                  text: words,
                  client_id: client,
                  ...(offer ? { offer: true } : {}),
                },
          );
        if (offer) setAsOffer(false);
        setMessages((cur) =>
          mergeCustomer(
            (cur ?? []).filter((m) => m.id !== mine.id),
            [out],
          ),
        );
        changed.current();
      } catch (e) {
        // Not sent: the words come back to the box. When the answer never
        // came it may have got there: the same words again keep its client_id.
        setMessages((cur) => cur?.filter((m) => m.id !== mine.id));
        if (plain) {
          setText((cur) => (cur === "" ? words : cur));
          if (e instanceof ApiError && mayHaveArrived(e.status)) kept.current = { text: words, client };
        }
        const closedNow = e instanceof ApiError ? erasedWords(e.status, e.message, "business") : null;
        if (closedNow) setErased(closedNow);
        else setSendError(e);
        chat.reload();
      }
    })();
  };

  const owes = owesWords(c?.owes_kobo, c?.open_invoices, naira);
  return (
    <section
      className="chat-pane"
      aria-label={`Conversation with ${c ? customerTitle(c) : "a customer"}`}
      data-testid="customer-pane"
    >
      <ChatHead
        onBack={onBack}
        menuLabel="More about this conversation"
        avatar={<ChatAvatar name={c ? customerLetter(c) : "Customer"} />}
        title={c ? customerTitle(c) : "Customer"}
        subtitle={
          c
            ? [
                c.typing ? "" : c.phone && c.phone !== c.name ? phoneWords(c.phone) : "",
                c.typing ? "" : owes,
                appThreadLine(c),
              ]
                .filter(Boolean)
                .join(" · ")
            : undefined
        }
        items={
          c
            ? (close) => (
                <>
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      close();
                      void act.run(
                        async () => {
                          await put(`/v1/customer-chats/${id}`, {
                            archived: !c.archived,
                          });
                          changed.current();
                          if (!c.archived) onBack();
                          else chat.reload();
                        },
                        c.archived ? "Brought back to the chats" : "Archived",
                      );
                    }}
                  >
                    {c.archived ? "Unarchive" : "Archive"}
                  </button>
                  <button
                    type="button"
                    role="menuitem"
                    onClick={() => {
                      close();
                      // Muted: its messages tell nobody (no bell, no notification).
                      void act.run(
                        async () => {
                          await put(`/v1/customer-chats/${id}`, {
                            muted: !c.muted,
                          });
                          chat.reload();
                        },
                        c.muted
                          ? "Notifications on for this customer"
                          : "Muted",
                      );
                    }}
                    data-testid="customer-mute"
                  >
                    {c.muted ? "Unmute notifications" : "Mute notifications"}
                  </button>
                  {c.line === "app" && (
                    <button
                      type="button"
                      role="menuitem"
                      data-testid="customer-block"
                      onClick={async () => {
                        close();
                        if (!c.blocked) {
                          const yes = await sure.confirm({
                            title: `Block ${customerTitle(c)}?`,
                            body: "They will not be able to write to you, and you will not be able to write to them. Nothing is deleted.",
                            ok: "Block",
                            danger: true,
                          });
                          if (!yes) return;
                        }
                        void act.run(
                          async () => {
                            if (c.blocked) await del(`/v1/customer-chats/${id}/block`);
                            else await post(`/v1/customer-chats/${id}/block`);
                            chat.reload();
                            changed.current();
                          },
                          c.blocked ? "Unblocked" : "Blocked",
                        );
                      }}
                    >
                      {c.blocked ? "Unblock this customer" : "Block this customer"}
                    </button>
                  )}
                </>
              )
            : undefined
        }
      />
      {sure.node}
      <ErrorBox error={sendError ?? act.error ?? pollError ?? chat.error} />
      <ChatStream
        items={messages}
        thread={thread}
        hasOlder={hasOlder}
        prefix="customer"
        keyOf={(m) => m.id}
        startsRun={(prev, m) =>
          !prev || prev.mine !== m.mine || prev.sender_name !== m.sender_name
        }
        empty={
          app ? (
            <>Nothing here yet. Write first: they read it in the Taskiem app.</>
          ) : c?.stopped ? (
            <>What they wrote was deleted when they sent STOP.</>
          ) : (
            <>
              Nothing here yet. What this customer writes to your WhatsApp
              number shows here.
            </>
          )
        }
        render={(m, { first }) => (
          <BubbleShell
            id={m.id}
            mine={m.mine}
            first={first}
            testid="customer-message"
            className={m.card ? "card-bubble" : m.kind === "media" && !m.attachment ? "media-note" : ""}
            what={(m.card?.title ?? m.body).split("\n")[0]?.slice(0, 60) ?? "message"}
            after={
              <span className={m.card || m.attachment ? "stamp" : "stamp inline"}>
                {m.mine && m.sent_as === "template" && (
                  <span
                    className="channel-mark"
                    title="Sent as a WhatsApp message they answer to carry on, more than a day after they last wrote"
                  >
                    as a message to answer ·
                  </span>
                )}
                <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
                  {clock(m.created_at)}
                </time>
                {m.mine && <DeliveryMark m={m} />}
              </span>
            }
          >
            {m.mine && first && m.sender_name && (
              <span className="who">{m.sender_name}</span>
            )}
            {m.offer && (
              <span className="pc-offer-mark">
                <Pill tone="plain">Offer</Pill>
              </span>
            )}
            {m.card && (
              <AppCardView
                card={m.card}
                actions={
                  cardLink(m.card) ? (
                    <Link className="tk-btn soft" to={cardLink(m.card) ?? "/"}>
                      Open<span className="sr-only"> {m.card.title}</span>
                    </Link>
                  ) : undefined
                }
              />
            )}
            {m.attachment && (
              <AppAttachmentView
                a={m.attachment}
                src={m.local ? undefined : `/v1/customer-chats/${encodeURIComponent(id)}/files/${encodeURIComponent(m.id)}`}
                who={c ? customerTitle(c) : "the customer"}
                mine={m.mine}
              />
            )}
            {!m.card && !(m.attachment && ["Photo", "Voice note", "File", m.attachment.name].includes(m.body)) && (
              <span className="text">
                {m.kind === "media" && !m.attachment ? <em>{m.body}</em> : m.body}
              </span>
            )}
            {m.mine && m.delivery === "failed" && (
              <span className="customer-failed" role="status">
                Not delivered{m.failure ? `: ${m.failure}` : "."}
              </span>
            )}
          </BubbleShell>
        )}
      />
      {c && <CustomerOrders chatId={c.id} canSend={canSend} />}
      {note && (
        <p
          className={`customer-note ${note.tone}`}
          role="status"
          data-testid="customer-note"
        >
          {note.text}
        </p>
      )}
      {canSend && tooLong && (
        <p
          className="customer-note stop"
          role="alert"
          data-testid="customer-too-long"
        >
          Too long to send now: after a day, a reply can be up to{" "}
          {LATE_REPLY_MAX} characters. This one is {lateLength(text)}.
        </p>
      )}
      {canSend && app && mayOffer(c ?? {}) && (
        <label className="switch-line customer-offer-line">
          <input type="checkbox" checked={asOffer} onChange={(e) => setAsOffer(e.target.checked)} data-testid="customer-as-offer" />{" "}
          <span>Send as an offer (they said yes to offers; two a week at most)</span>
        </label>
      )}
      {canSend && (
        <form
          className="composer"
          onSubmit={(e) => {
            e.preventDefault();
            send();
          }}
        >
          {app && (
            <>
              <button
                type="button"
                className="composer-tools"
                aria-label="Send a card: an order, a pay link, a booking or a product"
                data-testid="customer-cards"
                onClick={() => setCards(true)}
              >
                <Icon name="grid" />
              </button>
              <input
                ref={filePick}
                type="file"
                hidden
                accept={APP_FILES_ACCEPT}
                data-testid="customer-file"
                onChange={(e) => {
                  const f = e.target.files?.[0];
                  e.target.value = "";
                  if (!f) return;
                  if (f.size > BUSINESS_FILES.max || !BUSINESS_FILES.types.test(f.type)) {
                    setSendError(new Error(f.size > BUSINESS_FILES.max ? `${f.name} is larger than ${BUSINESS_FILES.maxWords}.` : BUSINESS_FILES.typesMessage));
                    return;
                  }
                  send({ file: f });
                }}
              />
            </>
          )}
          <div className="composer-pill">
            <label className="sr-only" htmlFor="customer-text">
              Reply to {c ? customerTitle(c) : "the customer"}
            </label>
            <textarea
              id="customer-text"
              ref={box}
              data-chat-box=""
              rows={1}
              maxLength={2000}
              placeholder="Reply"
              value={text}
              onChange={(e) => typed(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  send();
                }
              }}
            />
            {app && (
              <button type="button" className="icon-button" aria-label="Send a photo, PDF or voice note" title="Send a photo or a PDF" onClick={() => filePick.current?.click()}>
                <Icon name="attach" />
              </button>
            )}
          </div>
          <button
            type="submit"
            className="primary icon-button send-button"
            disabled={!text.trim() || tooLong}
          >
            <Icon name="arrow-up" />
            <span className="sr-only">Send</span>
          </button>
        </form>
      )}
      {cards && c && (
        <CardSheet
          chat={id}
          name={customerTitle(c)}
          references={[...new Set((messages ?? []).map((m) => m.card?.reference).filter((r): r is string => !!r))]}
          onClose={() => setCards(false)}
          onSend={(card, words) => send({ card, cardWords: words })}
        />
      )}
    </section>
  );
}

/** How far a reply of ours has got: a clock while it goes, one tick once
 * WhatsApp has it, two when it reached their phone (blue once read), and a
 * mark when it could not be delivered. */
function DeliveryMark({ m }: { m: CustomerMessage }) {
  if (m.local || m.delivery === "sending")
    return (
      <span className="ticks pending" role="img" aria-label="Sending">
        <Icon name="clock" />
      </span>
    );
  switch (m.delivery) {
    case "failed":
      return (
        <span className="ticks failed" role="img" aria-label="Not delivered">
          <span aria-hidden="true">!</span>
        </span>
      );
    case "delivered":
      return <Ticks read={false} />;
    case "read":
      return <Ticks read />;
    default:
      return (
        <span className="ticks" role="img" aria-label="Sent">
          <Icon name="tick" />
        </span>
      );
  }
}
