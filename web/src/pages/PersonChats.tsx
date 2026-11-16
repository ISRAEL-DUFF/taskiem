import { useCallback, useEffect, useRef, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { ApiError, get, post, put, upload, del } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { KitField, Pill, Sheet } from "../kit";
import { getDraft, setDraft } from "../lib/drafts";
import { clock, listTime } from "../lib/linkchat";
import {
  aboutOrderDraft,
  chatAboutWords,
  chatPreview,
  closedNote,
  headLine,
  mergeMessages,
  offersQuestion,
  personFileURL,
  REPORT_REASONS,
  startProblem,
  stateWords,
  stopOffersWords,
  withLatest,
  type DoorOrder,
  type PersonChat,
  type PersonMessage,
  type PhoneOrder,
} from "../lib/personchat";
import { formatPrice } from "../lib/storefront";
import { useThread } from "../lib/useThread";
import { EmptyState, ErrorBox, fmtTime, Items, Row, Skeleton, useAction, useLoad } from "../ui";
import { AppAttachmentView, AppCardView } from "./AppCards";
import { BubbleShell, Ticks } from "./BubbleShell";
import { ChatAvatar } from "./ChatAvatar";
import { BUSINESS_FILES, ComposerView, useComposer } from "./ChatComposer";
import { ChatHead } from "./ChatHead";
import { ChatRow } from "./ChatRow";
import { ChatStream } from "./ChatStream";
import { useConfirm } from "./ConfirmSheet";
import { BookSheet, OrdersSheet, PayButton, ShopSheet, ToolsMenu, type PersonTool } from "./PersonTools";

// A person's chats with businesses, on their own side (You;
// docs/spec/taskiem-for-everyone.md §7, §8.3, §9, §15): the list, a
// conversation with its tools and cards, starting a chat from a chat link
// or an order, and the storefront orders made with the person's phone.
// The browser chat (§9) is this same screen in a phone's browser.

/** How often an open conversation and the list look for what is new. */
const POLL_MS = 4000;

/** Where the person gets the Taskiem app: kept from the chat link's page,
 * the one place the server says. */
const APP_KEY = "taskiem.app-url";
export function rememberAppURL(url: string | undefined) {
  if (!url) return;
  try {
    localStorage.setItem(APP_KEY, url);
  } catch {
    // storage blocked: the line is not offered
  }
}
function appURL(): string | null {
  try {
    return localStorage.getItem(APP_KEY);
  } catch {
    return null;
  }
}

const draftKey = (id: string) => `person:${id}`;

/** The person's chats with businesses, latest first (archived ones apart). */
export function PersonChatList() {
  const [archived, setArchived] = useState(false);
  const list = useLoad(() => get<{ chats: PersonChat[]; unread: number }>(`/v1/me/chats${archived ? "?archived=1" : ""}`), [archived]);
  const reload = list.reload;
  useEffect(() => {
    const t = setInterval(() => document.visibilityState === "visible" && reload(), POLL_MS * 2);
    return () => clearInterval(t);
  }, [reload]);
  const chats = list.data?.chats ?? [];
  return (
    <section className="card person-chats" aria-labelledby="you-chats">
      <div className="toolbar">
        <h2 id="you-chats" className="grow" style={{ margin: 0 }}>
          {archived ? "Archived chats" : "Chats"}
        </h2>
        <button type="button" className="small" aria-pressed={archived} onClick={() => setArchived(!archived)} data-testid="person-archived">
          {archived ? "Back to chats" : "Archived"}
        </button>
      </div>
      <ErrorBox error={list.error} />
      {!list.data && !list.error && <Skeleton rows={2} label="Loading your chats…" />}
      {list.data && chats.length === 0 && (
        <EmptyState icon="chat">
          {archived ? "No archived chats." : "Chats with businesses will show here. Open a business's chat link, or chat about an order below."}
        </EmptyState>
      )}
      {chats.length > 0 && (
        <ul className="chat-rows" aria-label={archived ? "Archived chats" : "Your chats"} data-testid="person-chats">
          {chats.map((c) => {
            const draft = getDraft(draftKey(c.id));
            return (
              <ChatRow
                key={c.id}
                to={`/you/chats/${c.id}`}
                letter={c.name}
                title={c.name}
                time={listTime(c.last_at)}
                preview={
                  draft ? (
                    <>
                      <span className="draft">Draft:</span> {draft}
                    </>
                  ) : (
                    chatPreview(c)
                  )
                }
                unread={c.unread}
                muted={c.muted}
                testid="person-chat"
              />
            );
          })}
        </ul>
      )}
    </section>
  );
}

/** Orders made with the person's phone on businesses' shops (§13): each
 * opens a chat with its business; those made before the phone was on the
 * account wait for "These are mine". */
export function MyShopOrders() {
  const list = useLoad(() => get<{ orders: PhoneOrder[]; waiting: boolean }>("/v1/me/storefront-orders?limit=10"), []);
  const act = useAction();
  const nav = useNavigate();
  const orders = list.data?.orders ?? [];
  if (!list.data || orders.length === 0) return null;
  return (
    <section className="card" aria-labelledby="you-shop-orders" data-testid="shop-orders">
      <h2 id="you-shop-orders">Your orders from shops</h2>
      <p className="hint">Orders made with your phone number on businesses' shops.</p>
      {list.data.waiting && (
        <div className="notice" role="status" data-testid="these-are-mine">
          Some of these were made with your number before it was on your account. Are they yours?{" "}
          <button
            type="button"
            className="small"
            disabled={act.busy}
            onClick={() =>
              void act.run(async () => {
                await post("/v1/me/storefront-orders/confirm");
                list.reload();
              }, "Thank you. They are yours now.")
            }
          >
            These are mine
          </button>
        </div>
      )}
      <ErrorBox error={act.error} />
      <Items label="Your orders from shops">
        {orders.map((o) => {
          const st = stateWords(o.state);
          return (
            <Row
              key={`${o.business_id}-${o.reference}`}
              icon="orders"
              title={o.business}
              side={formatPrice(o.total_kobo)}
              sub={
                <span className="sub">
                  {o.reference} · {fmtTime(o.made_at)}
                  {st.label ? ` · ${st.label}` : ""}
                  {o.waiting && <span className="block">Waiting for you to say it's yours</span>}
                </span>
              }
              testid="shop-order"
            >
              {!o.waiting && (
                <button type="button" className="small" onClick={() => nav(`/you/chats/new?business=${encodeURIComponent(o.business_id)}&ref=${encodeURIComponent(o.reference)}`)}>
                  {chatAboutWords(o.business)}
                </button>
              )}
            </Row>
          );
        })}
      </Items>
    </section>
  );
}

/** /you/chats/new?code= (a chat link) or ?business=&ref= (an order): opens
 * the conversation, made or found, and goes to it. */
export function StartChat() {
  const loc = useLocation();
  const nav = useNavigate();
  const [problem, setProblem] = useState<string>();
  const tried = useRef(false);
  useEffect(() => {
    if (tried.current) return;
    tried.current = true;
    const q = new URLSearchParams(loc.search);
    const code = q.get("code");
    const business = q.get("business");
    const ref = q.get("ref");
    const body = code ? { code } : { business_id: business, from: ref ? "order" : "shop" };
    if (!code && !business) {
      setProblem("This page needs a business's chat link.");
      return;
    }
    post<PersonChat>("/v1/me/chats", body).then(
      (c) => {
        if (ref && !getDraft(draftKey(c.id))) setDraft(draftKey(c.id), aboutOrderDraft(ref));
        nav(`/you/chats/${c.id}`, { replace: true, state: { notice: c.notice } });
      },
      (e: unknown) => {
        if (e instanceof ApiError) setProblem(startProblem(e.status, e.body, e.message.replace(/^./, (x) => x.toUpperCase())));
        else setProblem("The chat could not be opened. Check your connection and try again.");
      },
    );
  }, []);
  return (
    <section className="card" data-testid="start-chat">
      <h1 className="sr-only">Opening the chat</h1>
      {problem ? (
        <>
          <p className="error" role="alert">
            {problem}
          </p>
          <Link to="/you">Back to your chats</Link>
        </>
      ) : (
        <Skeleton rows={2} label="Opening the chat…" />
      )}
    </section>
  );
}

let localN = 0;

/** A photo as a data URL, for its preview while it is sent (the app's
 * content policy allows no blob: images). */
const asDataURL = (f: File) =>
  new Promise<string | undefined>((done) => {
    if (!f.type.startsWith("image/")) return done(undefined);
    const r = new FileReader();
    r.onload = () => done(typeof r.result === "string" ? r.result : undefined);
    r.onerror = () => done(undefined);
    r.readAsDataURL(f);
  });

/** /you/chats/:id: one conversation with a business. */
export function PersonChatPage() {
  const { id = "" } = useParams();
  const loc = useLocation();
  const nav = useNavigate();
  const { logout } = useAuth();
  const chat = useLoad(() => get<PersonChat>(`/v1/me/chats/${encodeURIComponent(id)}`), [id]);
  const [messages, setMessages] = useState<PersonMessage[]>();
  const [hasOlder, setHasOlder] = useState(false);
  const [error, setError] = useState<unknown>();
  const [notice, setNotice] = useState<string | undefined>(() => (loc.state as { notice?: string } | null)?.notice);
  const [tool, setTool] = useState<{ key: PersonTool | "menu"; from?: DOMRect; item?: string; service?: string }>();
  const [reporting, setReporting] = useState(false);
  const [orders, setOrders] = useState<DoorOrder[]>();
  const act = useAction();
  const sure = useConfirm();
  const loadingOlder = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const thread = useThread({ items: messages, isMine: (m) => m.mine, hasOlder, loadOlder: olderCb });
  const base = `/v1/me/chats/${encodeURIComponent(id)}`;
  const c = chat.data;

  const pull = useCallback(
    async (first: boolean) => {
      const out = await get<{ messages: PersonMessage[]; more: boolean }>(`${base}/messages`);
      setMessages((cur) => (first ? out.messages : withLatest(cur ?? [], out.messages, out.more)));
      if (first) setHasOlder(out.more);
      if (document.visibilityState === "visible" && out.messages.some((m) => !m.mine && !m.read_at)) await post(`${base}/read`);
    },
    [base],
  );
  const reloadChat = chat.reload;
  useEffect(() => {
    setMessages(undefined);
    thread.reset();
    pull(true).catch(setError);
    const t = setInterval(() => {
      if (document.visibilityState !== "visible") return;
      pull(false).catch(setError);
      reloadChat();
    }, POLL_MS);
    return () => clearInterval(t);
  }, [id, pull]);
  loadOlderRef.current = () => {
    const oldest = messages?.find((m) => !m.local);
    if (!oldest || loadingOlder.current) return;
    loadingOlder.current = true;
    get<{ messages: PersonMessage[]; more: boolean }>(`${base}/messages?before=${encodeURIComponent(oldest.id)}`)
      .then((out) => {
        thread.keepPlace();
        setMessages((cur) => mergeMessages(cur ?? [], out.messages));
        setHasOlder(out.more);
      })
      .catch(setError)
      .finally(() => (loadingOlder.current = false));
  };

  // The orders the cards name, for their Pay: read once there are cards.
  const hasOrderCards = !!messages?.some((m) => m.card?.reference);
  const loadOrders = useCallback(() => {
    get<{ orders: DoorOrder[] }>(`${base}/orders`).then((r) => setOrders(r.orders), () => undefined);
  }, [base]);
  useEffect(() => {
    if (hasOrderCards && !orders) loadOrders();
  }, [hasOrderCards]);

  // Typing: told at most every few seconds while words are typed.
  const lastTyped = useRef(0);
  const typing = (v: string) => {
    if (!v.trim() || Date.now() - lastTyped.current < 4000 || !c || c.blocked || c.blocked_you) return;
    lastTyped.current = Date.now();
    void post(`${base}/typing`).catch(() => undefined);
  };

  const send = async (text: string, file?: File) => {
    const client = `${Date.now()}-${++localN}`;
    const preview = file ? await asDataURL(file) : undefined;
    const mine: PersonMessage = {
      id: `local-${client}`,
      mine: true,
      kind: file ? "media" : "text",
      body: text,
      attachment: file ? { kind: file.type.startsWith("image/") ? "photo" : file.type.startsWith("audio/") ? "voice" : "file", type: file.type, size: file.size, name: file.name } : undefined,
      read_at: null,
      created_at: new Date().toISOString(),
      local: true,
      localPreview: preview,
    };
    setMessages((cur) => mergeMessages(cur ?? [], [mine]));
    thread.toBottom?.();
    try {
      let out: PersonMessage;
      if (file) {
        const form = new FormData();
        form.append("file", file);
        if (text) form.append("text", text);
        form.append("client_id", client);
        out = await upload<PersonMessage>(`${base}/files`, form);
      } else out = await post<PersonMessage>(`${base}/messages`, { text, client_id: client });
      setMessages((cur) => mergeMessages((cur ?? []).filter((m) => m.id !== mine.id), [out]));
    } catch (e) {
      // Not sent: the words come back to the box, and why is said.
      setMessages((cur) => cur?.filter((m) => m.id !== mine.id));
      if (text && !file) composer.setText((cur) => (cur === "" ? text : cur));
      setError(e);
      reloadChat();
    }
  };

  const composer = useComposer<never>({
    draftKey: draftKey(id),
    rules: BUSINESS_FILES,
    sources: { "@": [], "#": [] },
    onType: typing,
    onSend: (s) => {
      setError(undefined);
      void send(s.text, s.file);
    },
    enabled: !!c && !c.blocked && !c.blocked_you,
    closes: [!!tool, reporting],
  });

  const change = (body: Record<string, boolean>, said: string) =>
    act.run(async () => {
      await put(base, body);
      reloadChat();
    }, said);
  const block = async () => {
    if (!c) return;
    if (c.blocked) {
      await act.run(async () => {
        await del(`${base}/block`);
        reloadChat();
      }, `${c.name} can write to you again`);
      return;
    }
    const yes = await sure.confirm({
      title: `Block ${c.name}?`,
      body: `${c.name} will not be able to write to you, and you will not be able to write to them. Nothing is deleted: they keep their copy of this chat for your orders.`,
      ok: "Block",
      danger: true,
    });
    if (!yes) return;
    await act.run(async () => {
      await post(`${base}/block`);
      reloadChat();
    }, `Blocked ${c.name}`);
  };
  const signOut = async () => {
    const yes = await sure.confirm({ title: "Sign out of Taskiem?", body: "On a phone others use, sign out when you are done. Your chats stay with your account.", ok: "Sign out" });
    if (yes) await logout();
  };

  const name = c?.name ?? "the business";
  const closed = c ? closedNote(c) : null;
  const app = appURL();
  const ordersByRef = new Map((orders ?? []).map((o) => [o.reference, o]));
  const openTool = (key: PersonTool, from?: DOMRect, extra?: { item?: string; service?: string }) => setTool({ key, from, ...extra });

  return (
    <section className="chat-pane person-pane" aria-label={`Conversation with ${name}`} data-testid="person-pane" onKeyDown={composer.onPaneKeyDown} {...composer.dropProps}>
      <h1 className="sr-only">{c ? `Chat with ${c.name}` : "Chat"}</h1>
      <ChatHead
        onBack={() => nav("/you")}
        menuLabel="More about this conversation"
        avatar={<ChatAvatar name={name} />}
        title={c?.name ?? "Chat"}
        subtitle={c ? <span data-testid="person-head-line">{headLine(c)}</span> : undefined}
        items={
          c
            ? (close) => (
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
                  {c.offers !== "unasked" && (
                    <button type="button" role="menuitem" onClick={() => (close(), void change({ offers: c.offers !== "yes" }, c.offers === "yes" ? `No more offers from ${c.name}` : `You'll get offers from ${c.name}`))}>
                      {c.offers === "yes" ? stopOffersWords(c.name) : `Get offers from ${c.name}`}
                    </button>
                  )}
                  <button type="button" role="menuitem" onClick={() => (close(), void block())} data-testid="person-block">
                    {c.blocked ? `Unblock ${c.name}` : `Block ${c.name}`}
                  </button>
                  <button type="button" role="menuitem" onClick={() => (close(), setReporting(true))}>
                    Report
                  </button>
                  <button type="button" role="menuitem" onClick={() => (close(), void signOut())} data-testid="person-sign-out">
                    Sign out
                  </button>
                </>
              )
            : undefined
        }
      />
      {notice && (
        <div className="notice pc-notice" role="status" data-testid="person-notice">
          <span className="grow">{notice}</span>
          <button type="button" className="small" onClick={() => setNotice(undefined)}>
            OK
          </button>
        </div>
      )}
      <ErrorBox error={error ?? chat.error ?? act.error} />
      <ChatStream
        items={messages}
        thread={thread}
        hasOlder={hasOlder}
        prefix="person"
        keyOf={(m) => m.id}
        startsRun={(prev, m) => !prev || prev.mine !== m.mine}
        empty={<>Say hello to {name}. They will answer here.</>}
        render={(m, { first }) => {
          const card = m.card;
          const caption = m.attachment ? (["Photo", "Voice note", "File", m.attachment.name].includes(m.body) ? "" : m.body) : card ? "" : m.body;
          const ord = card?.reference ? ordersByRef.get(card.reference) : undefined;
          // The time sits in the corner of the last line of words; under a card,
          // a file or Stop offers it has a row of its own.
          const inline = !!caption && !(m.offer && c?.offers === "yes");
          return (
            <BubbleShell
              id={m.id}
              mine={m.mine}
              first={first}
              testid="person-message"
              className={`${card ? "card-bubble" : ""}${m.offer ? " offer-bubble" : ""}`.trim()}
              what={(card?.title ?? m.body).split("\n")[0]?.slice(0, 60) ?? "message"}
              after={
                <span className={inline ? "stamp inline" : "stamp"}>
                  <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
                    {clock(m.created_at)}
                  </time>
                  {m.mine &&
                    (m.local ? (
                      <span className="ticks pending" role="img" aria-label="Sending">
                        <Icon name="clock" />
                      </span>
                    ) : (
                      <Ticks read={!!m.read_at} />
                    ))}
                </span>
              }
            >
              {!m.mine && first && m.sender_name && <span className="who">{m.sender_name}</span>}
              {m.offer && (
                <span className="pc-offer-mark">
                  <Pill tone="plain">Offer</Pill>
                </span>
              )}
              {card && (
                <AppCardView
                  card={card}
                  actions={
                    card.type === "pay" && card.url ? (
                      <a className="tk-btn primary" href={card.url} target="_blank" rel="noopener noreferrer">
                        Pay<span className="sr-only"> {card.title}</span>
                      </a>
                    ) : card.type === "order" && ord?.pay ? (
                      <PayButton chat={id} order={ord} />
                    ) : card.type === "booking" && !card.reference && card.service_id ? (
                      <button type="button" className="tk-btn primary" onClick={(e) => openTool("book", e.currentTarget.getBoundingClientRect(), { service: card.service_id })}>
                        Book<span className="sr-only"> {card.title}</span>
                      </button>
                    ) : card.type === "product" && card.item_id ? (
                      <button type="button" className="tk-btn primary" onClick={(e) => openTool("shop", e.currentTarget.getBoundingClientRect(), { item: card.item_id })}>
                        Order<span className="sr-only"> {card.title}</span>
                      </button>
                    ) : undefined
                  }
                />
              )}
              {m.attachment && <AppAttachmentView a={m.attachment} src={m.local ? undefined : personFileURL(id, m.id)} preview={m.localPreview} who={name} mine={m.mine} />}
              {caption && (
                <span className="text">
                  {caption}
                  {inline && <span className={`stamp-gap${m.mine ? " mine" : ""}`} aria-hidden="true" />}
                </span>
              )}
              {m.offer && c?.offers === "yes" && (
                <button type="button" className="link pc-stop-offers" onClick={() => void change({ offers: false }, `No more offers from ${name}. Messages about your orders still come.`)}>
                  {stopOffersWords(name)}
                </button>
              )}
            </BubbleShell>
          );
        }}
        tail={
          c?.ask_offers && !c.blocked && !c.blocked_you ? (
            <div className="tk-alert pc-offers" role="group" aria-label={offersQuestion(c.name)} data-testid="offers-question">
              <span className="tk-alert-words">
                <span className="tk-alert-title">{offersQuestion(c.name)}</span>
                <span className="tk-alert-line">News and offers, at most two a week. Messages about your orders come either way. You can change this in the menu.</span>
              </span>
              <span className="tk-alert-acts">
                <button type="button" className="tk-alert-go" disabled={act.busy} onClick={() => void change({ offers: true }, `You'll get offers from ${c.name}`)}>
                  Yes
                </button>
                <button type="button" className="tk-alert-soft" disabled={act.busy} onClick={() => void change({ offers: false }, "No offers")}>
                  No
                </button>
              </span>
            </div>
          ) : undefined
        }
      />
      {closed ? (
        <p className="customer-note stop" role="status" data-testid="person-closed">
          {closed}
        </p>
      ) : (
        c && (
          <ComposerView
            c={composer}
            busy={false}
            prefix="person"
            textId="person-text"
            textLabel={`Message ${c.name}`}
            lead={
              <button
                type="button"
                className="composer-tools"
                aria-label="Open tools"
                data-testid="person-tools-button"
                onClick={(e) => setTool({ key: "menu", from: e.currentTarget.getBoundingClientRect() })}
              >
                <Icon name="grid" />
              </button>
            }
          />
        )
      )}
      {app && (
        <p className="pc-app-line" data-testid="get-app-line">
          <a href={app} target="_blank" rel="noopener noreferrer">
            Get the Taskiem app for your chats with every business
          </a>
        </p>
      )}
      {c && tool?.key === "menu" && <ToolsMenu business={c.name} onClose={() => setTool(undefined)} onPick={(k, from) => setTool({ key: k, from })} />}
      {c && tool?.key === "shop" && (
        <ShopSheet
          chat={id}
          business={c.name}
          from={tool.from}
          startItem={tool.item}
          onClose={() => setTool(undefined)}
          onPlaced={() => {
            void pull(false);
            loadOrders();
          }}
        />
      )}
      {c && tool?.key === "book" && (
        <BookSheet
          chat={id}
          business={c.name}
          from={tool.from}
          startService={tool.service}
          onClose={() => setTool(undefined)}
          onBooked={() => {
            void pull(false);
            loadOrders();
          }}
        />
      )}
      {c && (tool?.key === "orders" || tool?.key === "pay") && <OrdersSheet chat={id} business={c.name} from={tool.from} only={tool.key === "pay" ? "pay" : undefined} onClose={() => setTool(undefined)} />}
      {c && reporting && (
        <ReportSheet
          chat={id}
          business={c.name}
          blocked={c.blocked}
          onClose={() => setReporting(false)}
          onBlock={() => {
            setReporting(false);
            void block();
          }}
        />
      )}
      {sure.node}
    </section>
  );
}

/** Report: why, a note, and consent to send the last 20 messages. */
function ReportSheet({ chat, business, blocked, onClose, onBlock }: { chat: string; business: string; blocked: boolean; onClose: () => void; onBlock: () => void }) {
  const [reason, setReason] = useState<(typeof REPORT_REASONS)[number]["key"]>();
  const [note, setNote] = useState("");
  const [consent, setConsent] = useState(false);
  const [said, setSaid] = useState<string>();
  const act = useAction();
  return (
    <Sheet
      title={said ? "Reported" : `Report ${business}`}
      onClose={onClose}
      testid="report-sheet"
      footer={
        said ? undefined : (
          <button
            type="button"
            className="tk-cta"
            disabled={!reason || !consent || act.busy}
            data-testid="report-send"
            onClick={() =>
              void act.run(async () => {
                const r = await post<{ said: string }>(`/v1/me/chats/${encodeURIComponent(chat)}/report`, { reason, note: note.trim(), consent });
                setSaid(r.said);
              }, false)
            }
          >
            {act.busy ? "Sending…" : "Send report"}
          </button>
        )
      }
    >
      {said ? (
        <div className="pc-done">
          <p role="status">{said}</p>
          {!blocked && (
            <button type="button" className="tk-btn soft" onClick={onBlock}>
              Block {business}
            </button>
          )}
        </div>
      ) : (
        <div className="pc-check">
          <fieldset className="pc-choice">
            <legend>What is wrong?</legend>
            {REPORT_REASONS.map((r) => (
              <label key={r.key} className="switch-line">
                <input type="radio" name="report-reason" checked={reason === r.key} onChange={() => setReason(r.key)} /> <span>{r.words}</span>
              </label>
            ))}
          </fieldset>
          <KitField label="Tell us more" hint="Optional">
            <textarea rows={3} maxLength={1000} value={note} onChange={(e) => setNote(e.target.value)} />
          </KitField>
          <label className="switch-line pc-consent">
            <input type="checkbox" checked={consent} onChange={(e) => setConsent(e.target.checked)} />{" "}
            <span>Send the last 20 messages of this chat to Taskiem's moderators, so they can see what happened.</span>
          </label>
          <ErrorBox error={act.error} />
        </div>
      )}
    </Sheet>
  );
}
