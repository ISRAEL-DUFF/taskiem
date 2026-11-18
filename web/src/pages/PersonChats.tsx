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
  cardsKey,
  clientFor,
  closedNote,
  erasedWords,
  headLine,
  liveCard,
  mayHaveArrived,
  mergeMessages,
  offersQuestion,
  personFileURL,
  photoURL,
  REPORT_REASONS,
  startProblem,
  stopOffersWords,
  TRANSFER_WARNING,
  trustWords,
  withLatest,
  withoutTool,
  type DoorOrder,
  type KeptSend,
  type PersonChat,
  whatsappHere,
  type PersonMessage,
} from "../lib/personchat";
import { browserEnv, enableYouPush, pushSupport, serverKey, thisSubscription, youPushOn } from "../lib/push";
import { useThread } from "../lib/useThread";
import { EmptyState, ErrorBox, fmtTime, Skeleton, useAction, useLoad } from "../ui";
import { AppAttachmentView, AppCardView } from "./AppCards";
import { BubbleShell, Ticks } from "./BubbleShell";
import { ChatAvatar } from "./ChatAvatar";
import { BUSINESS_FILES, ComposerView, useComposer } from "./ChatComposer";
import { ChatHead } from "./ChatHead";
import { ChatRow } from "./ChatRow";
import { ChatStream } from "./ChatStream";
import { useConfirm } from "./ConfirmSheet";
import { BookSheet, OrdersSheet, PayButton, ShopSheet, ToolsMenu, type PersonTool } from "./PersonTools";
import { NewChatSheet, ShareToPerson } from "./People";
import { peopleDraft } from "./PeopleChat";
import { photoOf as peoplePhoto, requestsWords, rowKey, rowPath, rowPreview, type ChatList, type ChatListItem } from "../lib/people";
import { usePagedList } from "../lib/usePagedList";
import { chatsPoll, hasBusinessRow, rowsWithChat, typingWords } from "../lib/peoplelive";
import { usePeopleLive } from "./PeopleLive";
import { MoreRows } from "./MoreRows";

// A person's chats with businesses, on their own side (You;
// docs/spec/taskiem-for-everyone.md §7, §8.3, §9, §15): the list, a
// conversation with its tools and cards, starting a chat from a chat link
// or an order, and the storefront orders made with the person's phone.
// The browser chat (§9) is this same screen in a phone's browser.

/** How often an open conversation and the list look for what is new. */
const POLL_MS = 4000;
/** How often the orders an open conversation's cards name are read again. */
const ORDERS_MS = 15000;

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

/** The businesses' photos, as their conversations gave them (the list of
 * chats carries none): shown on their rows once a chat was opened here. */
const PHOTOS_KEY = "taskiem.chat-photos";
function photoOf(business: string): string | undefined {
  try {
    return (JSON.parse(sessionStorage.getItem(PHOTOS_KEY) ?? "{}") as Record<string, string>)[business];
  } catch {
    return undefined;
  }
}
function keepPhoto(business: string, url: string | undefined) {
  try {
    const all = JSON.parse(sessionStorage.getItem(PHOTOS_KEY) ?? "{}") as Record<string, string>;
    if ((all[business] ?? "") === (url ?? "")) return;
    const next = Object.fromEntries(Object.entries(all).filter(([k]) => k !== business));
    if (url) next[business] = url;
    sessionStorage.setItem(PHOTOS_KEY, JSON.stringify(next));
  } catch {
    // Storage blocked: the row shows the letter.
  }
}

/** You's one Chats list (people-chatting §7, §17 Q6): chats with
 * businesses and with people together, latest first, each marked by what
 * it is (archived ones apart), Requests at the top when some wait, and New
 * chat to find someone. */
export function PersonChatList() {
  const [archived, setArchived] = useState(false);
  const [finding, setFinding] = useState(false);
  const live = usePeopleLive();
  // A page at a time (200 a page), the first read again every little
  // while (slowly while the live connection tells what changes); each list
  // (archived or not) is its own, so a toggle never shows the other's rows
  // while its own are on their way.
  // Business rows change only by polling (the live connection tells of
  // people chats): every 8 seconds while the list has one.
  const [business, setBusiness] = useState(false);
  const list = usePagedList<ChatList, ChatListItem>(`/v1/me/conversations${archived ? "?archived=1" : ""}`, (r) => r.chats ?? [], rowKey, chatsPoll(live.connected, business, POLL_MS * 2));
  const { patch, reload } = list;
  const hasBusiness = hasBusinessRow(list.rows);
  useEffect(() => setBusiness(hasBusiness), [hasBusiness]);
  // A people chat's change is put in place at once; the list is read again
  // a moment later for what is the whole list's (unread, requests).
  const soon = useRef<number>(undefined);
  useEffect(
    () =>
      live.subscribe((e) => {
        if (e.type === "chat") patch((rows) => rowsWithChat(rows, e.data, archived));
        if (e.type === "chat" || e.type === "ready" || (e.type === "gone" && e.data.id === null)) {
          window.clearTimeout(soon.current);
          soon.current = window.setTimeout(reload, 500);
        }
      }),
    [live.subscribe, patch, reload, archived],
  );
  useEffect(() => () => window.clearTimeout(soon.current), []);
  const data = list.head;
  const chats = list.rows ?? [];
  const requests = data?.requests ?? 0;
  return (
    <section className="card person-chats" aria-labelledby="you-chats">
      <div className="toolbar">
        <h2 id="you-chats" className="grow" style={{ margin: 0 }}>
          {archived ? "Archived chats" : "Chats"}
        </h2>
        {!archived && (
          <button type="button" className="small primary" onClick={() => setFinding(true)} data-testid="people-new-chat-open">
            <Icon name="plus" /> New chat
          </button>
        )}
        <button type="button" className="small" aria-pressed={archived} onClick={() => setArchived(!archived)} data-testid="person-archived">
          {archived ? "Back to chats" : "Archived"}
        </button>
      </div>
      {!archived && (
        <div className="people-list-tools">
          <Link className="button small" to="/you/people/search" data-testid="people-search-open">
            <Icon name="search" /> Search messages
          </Link>
          <Link className="button small" to="/you/people/starred" data-testid="people-starred-open">
            <Icon name="star" /> Starred
          </Link>
        </div>
      )}
      <ErrorBox error={list.error} />
      {!archived && requests > 0 && (
        <Link className="people-requests-entry" to="/you/people/requests" data-testid="people-requests-entry">
          <Icon name="inbox" />
          <span className="grow">Message requests</span>
          <span className="unread-count">
            <span aria-hidden="true">{requests}</span>
            <span className="sr-only">{requestsWords(requests)} waiting</span>
          </span>
        </Link>
      )}
      {!data && !list.error && <Skeleton rows={2} label="Loading your chats…" />}
      {data && list.rows && chats.length === 0 && (
        <EmptyState icon="chat">
          {archived ? "No archived chats." : "Your chats with businesses and people will show here. Find a business in Discover, or start a New chat with someone."}
        </EmptyState>
      )}
      {/* Who is typing, said politely to a screen reader (the rows say "typing…"). */}
      <p className="sr-only" aria-live="polite" data-testid="people-typing-live">
        {typingWords(chats.filter((c) => c.kind === "person" && live.typing.has(c.id)).map((c) => c.name))}
      </p>
      {chats.length > 0 && (
        <ul className="chat-rows" aria-label={archived ? "Archived chats" : "Your chats"} data-testid="person-chats">
          {chats.map((c) => {
            const person = c.kind === "person";
            const draft = person ? peopleDraft(c.id) : getDraft(draftKey(c.id));
            const typing = person && live.typing.has(c.id);
            return (
              <ChatRow
                key={`${c.kind}:${c.id}`}
                to={rowPath(c)}
                letter={c.name}
                logo={person ? peoplePhoto(c.person) : (photoURL(c.photo) ?? (c.business_id ? photoOf(c.business_id) : undefined))}
                title={
                  <>
                    {c.name}
                    <span className="sr-only">{person ? " (a person)" : " (a business)"}</span>
                  </>
                }
                mark={
                  person ? undefined : (
                    <span className="chat-kind" data-testid="chat-kind" aria-hidden="true">
                      Business
                    </span>
                  )
                }
                time={listTime(c.last_at)}
                preview={
                  typing ? (
                    <span data-testid="people-row-typing">typing…</span>
                  ) : draft ? (
                    <>
                      <span className="draft">Draft:</span> {draft}
                    </>
                  ) : (
                    rowPreview(c)
                  )
                }
                typing={typing}
                trust={person ? undefined : trustWords(c.verified) || undefined}
                unread={c.unread}
                muted={c.muted}
                testid={person ? "people-chat" : "person-chat"}
              />
            );
          })}
        </ul>
      )}
      <MoreRows more={list.more} busy={list.loadingMore} onMore={list.loadMore} count={list.rows?.length ?? 0} label="Show more chats" testid="person-chats-more" />
      {finding && <NewChatSheet onClose={() => setFinding(false)} />}
    </section>
  );
}

/** /you/chats/new?code= (a chat link), ?business=&ref= (an order) or
 * ?business=&from=discover (Discover, with &tool= for Their shop or Book):
 * opens the conversation, made or found, and goes to it, with the tool
 * open when one was asked for. */
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
    // Where the chat starts from: an order, Discover, a card a friend shared
    // (people-chatting §10.2: the business sees a new customer, not who
    // shared it), else its shop.
    const asked = q.get("from");
    const from = ref ? "order" : asked === "discover" || asked === "shared" ? asked : "shop";
    const tool = q.get("tool");
    const body = code ? { code } : { business_id: business, from };
    if (!code && !business) {
      setProblem("This page needs a business's chat link.");
      return;
    }
    post<PersonChat>("/v1/me/chats", body).then(
      (c) => {
        if (ref && !getDraft(draftKey(c.id))) setDraft(draftKey(c.id), aboutOrderDraft(ref));
        nav(`/you/chats/${c.id}`, { replace: true, state: { notice: c.notice, tool: tool === "shop" || tool === "book" ? tool : undefined } });
      },
      (e: unknown) => {
        if (e instanceof ApiError && !code && e.status === 404) setProblem("This business can't be reached by chat in Taskiem right now.");
        else if (e instanceof ApiError) setProblem(startProblem(e.status, e.body, e.message.replace(/^./, (x) => x.toUpperCase())));
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
  // What the last look for news ran into (gone once a look works), and
  // what a send ran into, apart.
  const [pollError, setPollError] = useState<unknown>();
  const [sendError, setSendError] = useState<unknown>();
  // The business erased the person's details: the conversation is closed.
  const [erased, setErased] = useState<string>();
  // A send whose answer never came, its words back in the box: sent again
  // as they are, it keeps its client_id, so it is not there twice.
  const kept = useRef<KeptSend | undefined>(undefined);
  const [notice, setNotice] = useState<string | undefined>(() => (loc.state as { notice?: string } | null)?.notice);
  // A tool asked for on the way in (Their shop or Book from Discover).
  const [tool, setTool] = useState<{ key: PersonTool | "menu"; from?: DOMRect; item?: string; service?: string } | undefined>(() => {
    const t = (loc.state as { tool?: string } | null)?.tool;
    return t === "shop" || t === "book" ? { key: t } : undefined;
  });
  // Once opened, the tool leaves the history's state: a reload, or Back
  // to this chat later, does not open it again.
  useEffect(() => {
    const left = withoutTool(loc.state);
    if (left === undefined) return;
    nav(`${loc.pathname}${loc.search}`, { replace: true, state: left });
  }, []);
  const [reporting, setReporting] = useState(false);
  // Share the business, or one of its products, with someone (§10.1).
  const [sharing, setSharing] = useState<{ item?: { id: string; name: string } }>();
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
      setPollError(undefined);
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
      .catch(setPollError)
      .finally(() => (loadingOlder.current = false));
  };

  // The orders the cards name, for their state, their rider and their
  // Pay: read when a card names another order (or a new card comes), again
  // every little while as long as there are such cards, and soon again
  // when a read fails.
  const ordersKey = cardsKey(messages);
  const loadOrders = useCallback(() => {
    get<{ orders: DoorOrder[] }>(`${base}/orders`).then(
      (r) => setOrders(r.orders),
      () => undefined,
    );
  }, [base]);
  useEffect(() => {
    if (!ordersKey) return;
    let live = true;
    let retry: ReturnType<typeof setTimeout> | undefined;
    const look = (tries: number) =>
      get<{ orders: DoorOrder[] }>(`${base}/orders`).then(
        (r) => live && setOrders(r.orders),
        () => {
          if (live && tries < 3) retry = setTimeout(() => void look(tries + 1), 3000 * (tries + 1));
        },
      );
    void look(0);
    const t = setInterval(() => document.visibilityState === "visible" && void look(3), ORDERS_MS);
    return () => {
      live = false;
      clearInterval(t);
      if (retry) clearTimeout(retry);
    };
  }, [ordersKey, base]);

  // Typing: told at most every few seconds while words are typed. Refused
  // while either side blocked the other (403, 409): nothing to say.
  const lastTyped = useRef(0);
  const typing = (v: string) => {
    if (!v.trim() || Date.now() - lastTyped.current < 4000 || !c || c.blocked || c.blocked_you || erased) return;
    lastTyped.current = Date.now();
    void post(`${base}/typing`).catch(() => undefined);
  };

  const send = async (text: string, file?: File) => {
    const client = file ? `${Date.now()}-${++localN}` : clientFor(kept.current, text, () => `${Date.now()}-${++localN}`);
    kept.current = undefined;
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
      // Not sent: the words come back to the box, and why is said. When
      // the answer never came it may have got there: the same words sent
      // again keep its client_id.
      setMessages((cur) => cur?.filter((m) => m.id !== mine.id));
      if (text && !file) {
        composer.setText((cur) => (cur === "" ? text : cur));
        if (e instanceof ApiError && mayHaveArrived(e.status)) kept.current = { text, client };
      }
      const closedNow = e instanceof ApiError ? erasedWords(e.status, e.message, "person") : null;
      if (closedNow) setErased(closedNow);
      else setSendError(e);
      reloadChat();
    }
  };

  const composer = useComposer<never>({
    draftKey: draftKey(id),
    rules: BUSINESS_FILES,
    sources: { "@": [], "#": [] },
    onType: typing,
    onSend: (s) => {
      setSendError(undefined);
      void send(s.text, s.file);
    },
    enabled: !!c && !c.blocked && !c.blocked_you && !erased,
    closes: [!!tool, reporting, !!sharing],
  });

  // The offers question is asked once: answered, or shown and passed over
  // (the person closes it, or leaves the chat without answering).
  const offersShown = useRef(false);
  const offersDone = useRef(false);
  const askingOffers = !!c?.ask_offers && !c.blocked && !c.blocked_you;
  if (askingOffers) offersShown.current = true;
  useEffect(
    () => () => {
      if (offersShown.current && !offersDone.current) void put(base, { offers_asked: true }).catch(() => undefined);
    },
    [base],
  );

  const change = (body: Record<string, boolean>, said: string | false) =>
    act.run(async () => {
      if ("offers" in body || "offers_asked" in body) offersDone.current = true;
      const r = await put<PersonChat | undefined>(base, body);
      if (r?.offers_refused) setNotice(`${c?.name ?? "This business"} is not taking messages from you, so your answer about offers was not kept.`);
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
  const closed = erased ?? (c ? closedNote(c) : null);
  const app = appURL();
  const ordersByRef = new Map((orders ?? []).map((o) => [o.reference, o]));
  const photo = photoURL(c?.photo);
  const trust = trustWords(c?.verified);
  useEffect(() => {
    if (c?.business) keepPhoto(c.business, photo);
  }, [c?.business, photo]);
  const openTool = (key: PersonTool, from?: DOMRect, extra?: { item?: string; service?: string }) => setTool({ key, from, ...extra });

  return (
    <section className="chat-pane person-pane" aria-label={`Conversation with ${name}`} data-testid="person-pane" onKeyDown={composer.onPaneKeyDown} {...composer.dropProps}>
      <h1 className="sr-only">{c ? `Chat with ${c.name}` : "Chat"}</h1>
      <ChatHead
        onBack={() => nav("/you")}
        menuLabel="More about this conversation"
        avatar={<ChatAvatar name={name} logo={photo} />}
        title={c?.name ?? "Chat"}
        subtitle={
          c ? (
            <>
              <span data-testid="person-head-line">{headLine(c)}</span>
              {trust && (
                <span className="pc-trust" data-testid="person-trust" title={trust}>
                  {trust}
                </span>
              )}
              {whatsappHere(c) && (
                <span className="pc-wa-here" data-testid="person-whatsapp-here">
                  {whatsappHere(c)}
                </span>
              )}
            </>
          ) : undefined
        }
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
                  <button type="button" role="menuitem" onClick={() => (close(), setSharing({}))} data-testid="person-share">
                    Share {c.name} with someone
                  </button>
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
      <ErrorBox error={sendError ?? act.error ?? pollError ?? chat.error} />
      <ChatStream
        items={messages}
        thread={thread}
        hasOlder={hasOlder}
        prefix="person"
        keyOf={(m) => m.id}
        startsRun={(prev, m) => !prev || prev.mine !== m.mine}
        empty={<>Say hello to {name}. They will answer here.</>}
        render={(m, { first }) => {
          const ord = m.card?.reference && m.card.type !== "invoice" ? ordersByRef.get(m.card.reference) : undefined;
          // An order's or a delivery's card shows where the order is now.
          const card = m.card ? liveCard(m.card, ord) : undefined;
          const caption = m.attachment ? (["Photo", "Voice note", "File", m.attachment.name].includes(m.body) ? "" : m.body) : card ? "" : m.body;
          const warn = !m.mine && !!m.warn_transfer;
          // The time sits in the corner of the last line of words; under a card,
          // a file, a warning or Stop offers it has a row of its own.
          const inline = !!caption && !(m.offer && c?.offers === "yes") && !warn;
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
              {m.line === "whatsapp" && (
                <span className="line-of" data-testid="person-on-whatsapp">
                  on WhatsApp
                </span>
              )}
              {m.offer && (
                <span className="pc-offer-mark">
                  <Pill tone="plain">Offer</Pill>
                </span>
              )}
              {card && (
                <AppCardView
                  card={card}
                  actions={
                    (card.type === "pay" || card.type === "invoice") && card.url ? (
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
              {warn && (
                <span className="pc-transfer-warn" role="note" data-testid="transfer-warning">
                  <Icon name="warn" />
                  <span>{TRANSFER_WARNING}</span>
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
          c && askingOffers ? (
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
                <button type="button" className="link" disabled={act.busy} onClick={() => void change({ offers_asked: true }, false)} data-testid="offers-not-now">
                  Not now
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
      {c && !closed && <TellMe />}
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
          onShare={(it) => {
            setTool(undefined);
            setSharing({ item: it });
          }}
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
      {c && sharing && (
        <ShareToPerson
          card={sharing.item ? { type: "product", business_id: c.business, item_id: sharing.item.id } : { type: "business", business_id: c.business }}
          what={sharing.item ? sharing.item.name : c.name}
          onClose={() => setSharing(undefined)}
        />
      )}
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

/** "Tell me when they answer" (§9): this browser is registered as the
 * person's own device, so a business's answer is pushed here (and not
 * texted). Where the browser cannot take a push (an iPhone's browser not
 * on the Home Screen, or the server has no keys), they are texted. */
export function TellMe({ people = false }: { people?: boolean }) {
  const [state, setState] = useState<"look" | "ask" | "busy" | "on" | "text" | "denied" | "failed">("look");
  useEffect(() => {
    let live = true;
    void (async () => {
      if (pushSupport(browserEnv()) !== "yes") return live && setState("text");
      if (!(await serverKey())) return live && setState("text");
      const on = youPushOn() && !!(await thisSubscription());
      if (live) setState(on ? "on" : "ask");
    })();
    return () => {
      live = false;
    };
  }, []);
  if (state === "look") return null;
  // A person's answer is never texted: where this browser can't take a
  // push, nothing is offered (people-chatting §7).
  if (people && (state === "text" || state === "denied")) return null;
  return (
    <p className="pc-push-line" data-testid="tell-me-line" role="status">
      {state === "ask" || state === "busy" || state === "failed" ? (
        <>
          {state === "failed" && "That did not work. "}
          <button
            type="button"
            className="link"
            disabled={state === "busy"}
            data-testid="tell-me"
            onClick={() => {
              setState("busy");
              void enableYouPush().then((r) => setState(r.ok ? "on" : r.why === "denied" ? "denied" : r.why === "failed" ? "failed" : "text"));
            }}
          >
            Tell me when they answer
          </button>
        </>
      ) : state === "on" ? (
        "We'll tell you on this device when they answer."
      ) : state === "denied" ? (
        "Notifications are blocked in this browser, so we'll text you when they answer."
      ) : (
        "We'll text you when they answer."
      )}
    </p>
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
