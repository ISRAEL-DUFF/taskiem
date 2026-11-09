import { useCallback, useEffect, useRef, useState } from "react";
import { get, post, put } from "../api";
import { Icon } from "../icons";
import { naira } from "../lib/billing";
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
import { BubbleShell, Ticks } from "./BubbleShell";
import { ChatAvatar } from "./ChatAvatar";
import { ChatHead } from "./ChatHead";
import { ChatRow } from "./ChatRow";
import { ChatStream } from "./ChatStream";

// Customers' conversations in the Chat page (docs/spec/chat-five-more-flows.md,
// phase 1): the list's Customers section and a customer's thread. A reply
// goes from the business's own WhatsApp number.

/** How often an open thread and the list look for what is new. */
const POLL_MS = 4000;

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
              title={customerTitle(c)}
              time={listTime(c.last_at)}
              preview={
                draft ? (
                  <>
                    <span className="draft">Draft:</span> {draft}
                  </>
                ) : c.stopped ? (
                  "Sent STOP"
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
  const [error, setError] = useState<unknown>();
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
      if (out.messages.some((m) => !m.mine && !m.read_at)) {
        await post(`/v1/customer-chats/${id}/read`);
        changed.current();
      }
    },
    [id],
  );
  useEffect(() => {
    setMessages(undefined);
    thread.reset();
    pull(true).catch(setError);
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      pull(false).catch(setError);
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
      .catch(setError)
      .finally(() => (loadingOlder.current = false));
  };

  const draftKey = `customer:${id}`;
  const [text, setText] = useState(() => getDraft(draftKey));
  useEffect(() => setText(getDraft(draftKey)), [draftKey]);
  useEffect(() => setDraft(draftKey, text), [draftKey, text]);
  const box = useRef<HTMLTextAreaElement>(null);
  useAutoGrow(box, text);

  const c = chat.data;
  const note = c ? replyNote(c) : null;
  const canSend = !!c && !c.stopped && !!c.phone;
  const tooLong = !!c && lateTooLong(text, c.window_open);
  const send = () => {
    const words = text.trim();
    if (!words || !canSend || act.busy) return;
    const client = `${Date.now()}-${++localN}`;
    const mine: CustomerMessage = {
      id: `local-${client}`,
      mine: true,
      kind: "text",
      body: words,
      read_at: null,
      created_at: new Date().toISOString(),
      local: true,
    };
    setMessages((cur) => mergeCustomer(cur ?? [], [mine]));
    setText("");
    void act
      .run(async () => {
        const out = await post<CustomerMessage>(
          `/v1/customer-chats/${id}/messages`,
          {
            text: words,
            client_id: client,
          },
        );
        setMessages((cur) =>
          mergeCustomer(
            (cur ?? []).filter((m) => m.id !== mine.id),
            [out],
          ),
        );
        changed.current();
      }, false)
      .then((ok) => {
        if (ok) return;
        // Not sent: the words come back to the box.
        setMessages((cur) => cur?.filter((m) => m.id !== mine.id));
        setText((cur) => (cur === "" ? words : cur));
        chat.reload();
      });
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
                c.phone && c.phone !== c.name ? phoneWords(c.phone) : "",
                owes,
                "Customer on WhatsApp",
              ]
                .filter(Boolean)
                .join(" · ")
            : undefined
        }
        items={
          c
            ? (close) => (
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
              )
            : undefined
        }
      />
      <ErrorBox error={error ?? chat.error ?? act.error} />
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
          c?.stopped ? (
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
            className={m.kind === "media" ? "media-note" : ""}
            what={m.body.split("\n")[0]?.slice(0, 60) ?? "message"}
            after={
              <span className="stamp inline">
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
            <span className="text">
              {m.kind === "media" ? <em>{m.body}</em> : m.body}
            </span>
            {m.mine && m.delivery === "failed" && (
              <span className="customer-failed" role="status">
                Not delivered{m.failure ? `: ${m.failure}` : "."}
              </span>
            )}
          </BubbleShell>
        )}
      />
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
      {canSend && (
        <form
          className="composer"
          onSubmit={(e) => {
            e.preventDefault();
            send();
          }}
        >
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
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey) {
                  e.preventDefault();
                  send();
                }
              }}
            />
          </div>
          <button
            type="submit"
            className="primary icon-button send-button"
            disabled={act.busy || !text.trim() || tooLong}
          >
            <Icon name="arrow-up" />
            <span className="sr-only">Send</span>
          </button>
        </form>
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
