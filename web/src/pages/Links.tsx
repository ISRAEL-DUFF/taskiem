import { useEffect, useRef, useState } from "react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import { del, get, getBlob, post, put, whenOff } from "../api";
import { areaOn } from "../lib/areas";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import {
  blindRequest,
  blindRowLine,
  cardAction,
  cardSub,
  findableLine,
  groupLinks,
  matchLinked,
  requestTo,
  roleLine,
  ROLE_CHOICES,
  stateWords,
  type BlindLinkRequest,
  type BusinessLink,
  type LinkAnswer,
  type LinkCard,
  type LinkRole,
  type LinkSearch,
  type LinkSettings,
  type LinksView,
} from "../lib/links";
import {
  listTime,
  logoSrc,
  matchChats,
  previewLine,
  type Conversation,
  type LinkMessage,
} from "../lib/linkchat";
import { ConversationPane } from "./Conversation";
import { TaskiemPane } from "./TaskiemChat";
import { ChatRow } from "./ChatRow";
import { useConfirm } from "./ConfirmSheet";
import { HeadMenu } from "./ChatHead";
import { useChatLive } from "./ChatLive";
import { useDrafts } from "../lib/drafts";
import { useTaskiemUnread } from "../lib/taskiemSeen";
import {
  isTyping,
  KEYS_EVENT,
  shortcutOf,
  SHORTCUTS,
  step,
} from "../lib/chatKeys";
import { Ticks } from "./BubbleShell";
import {
  EmptyState,
  ErrorBox,
  fmtTime,
  Items,
  Modal,
  PageHeader,
  Row,
  Skeleton,
  useAction,
  useLoad,
  useLiveRefresh,
} from "../ui";

// Business links (docs/spec/business-links.md §5.1, §5.5): the Chat page,
// its conversations with linked businesses (L3) beside the requests and
// the search, and Linked businesses under Secrets & settings.

function StateBadge({ state }: { state: Parameters<typeof stateWords>[0] }) {
  const w = stateWords(state);
  return w ? (
    <span className={`badge sf-status ${w.tone}`}>{w.label}</span>
  ) : null;
}

/** Who a request goes to: a business a search listed, one by the link
 * code it printed, or whoever uses a phone number or a storefront
 * address. */
type Asking =
  | { business: string; name: string }
  | { code: string; name: string }
  | { phone: string; shown: string }
  | { address: string; shown: string };

/** The sheet that asks a business to link: how we trade with them, then Send. */
function RequestSheet({
  asking,
  onClose,
  onSent,
}: {
  asking: Asking;
  onClose: () => void;
  onSent: () => void;
}) {
  const [role, setRole] = useState<LinkRole>("supplier");
  const act = useAction();
  const blind = "phone" in asking || "address" in asking;
  const send = () =>
    void act
      .run(
        async () => {
          const body =
            "phone" in asking
              ? { phone: asking.phone, role }
              : "address" in asking
                ? { address: asking.address, role }
                : "code" in asking
                  ? { code: asking.code, role }
                  : { business: asking.business, role };
          const out = await post<{ message: string }>("/v1/links", body);
          onSent();
          return out;
        },
        blind ? "Request sent" : `Asked ${asking.name} to link`,
      )
      .then((ok) => ok && onClose());
  return (
    <Modal
      title={
        blind ? `Ask ${asking.shown} to link` : `Ask ${asking.name} to link`
      }
      onClose={onClose}
    >
      <p className="hint">
        {blind
          ? `${blindRowLine("phone" in asking ? "phone" : "address")} You hear only if it accepts.`
          : "Linked, your invoices and orders reach each other by themselves. Nothing is shared until they accept."}
      </p>
      <fieldset className="choice">
        <legend>How do you trade with them?</legend>
        {ROLE_CHOICES.map((c) => (
          <label key={c.role} className="inline">
            <input
              type="radio"
              name="link-role"
              checked={role === c.role}
              onChange={() => setRole(c.role)}
            />
            {c.label}
          </label>
        ))}
      </fieldset>
      <ErrorBox error={act.error} />
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={act.busy}
          onClick={send}
        >
          Send request
        </button>
        <button type="button" onClick={onClose}>
          Not now
        </button>
      </div>
    </Modal>
  );
}

/** Accept, Decline and Block on a request made to us. A decline and a
 * block are never told to the business that asked. */
function AnswerButtons({
  link,
  answer,
  busy,
}: {
  link: BusinessLink;
  answer: (l: BusinessLink, a: LinkAnswer, done: string) => void;
  busy: boolean;
}) {
  const who = <span className="sr-only"> {link.name}</span>;
  const { confirm, node } = useConfirm();
  return (
    <>
      {node}
      <button
        type="button"
        className="primary small"
        disabled={busy}
        onClick={() => answer(link, "accept", `Linked with ${link.name}`)}
      >
        Accept{who}
      </button>
      <button
        type="button"
        className="small"
        disabled={busy}
        onClick={() => answer(link, "decline", "Declined. They are not told.")}
      >
        Decline{who}
      </button>
      <button
        type="button"
        className="small danger"
        disabled={busy}
        onClick={async () =>
          (await confirm({
            title: `Block ${link.name}?`,
            body: "Their requests will not reach you, and they are not told.",
            ok: "Block",
            danger: true,
          })) && answer(link, "block", "Blocked")
        }
      >
        Block{who}
      </button>
    </>
  );
}

const NO_LINKS: LinksView = { links: [], sent: [] };

function useLinks() {
  // With Linked businesses turned off there is nothing to ask for.
  const { me } = useAuth();
  const on = areaOn(me?.areas, "links");
  const view = useLoad(
    () =>
      on
        ? whenOff(get<LinksView>("/v1/links"), NO_LINKS)
        : Promise.resolve(NO_LINKS),
    [on],
  );
  const act = useAction();
  const answer = (
    l: BusinessLink | BlindLinkRequest,
    a: LinkAnswer | "remove",
    done: string,
  ) =>
    void act.run(async () => {
      await (a === "remove"
        ? del(`/v1/links/sent/${l.id}`)
        : post(`/v1/links/${l.id}/${a}`));
      view.reload();
    }, done);
  return { view, act, answer };
}

/**
 * Chat (business links, L0): the businesses on Taskiem this one trades
 * with, and the search that finds others, as WhatsApp's search finds
 * contacts. A phone number typed is never searched: it offers the one row
 * that sends a request to it.
 */
export function ChatPage() {
  const { me, can } = useAuth();
  // Chat is also the person's own conversation with Taskiem: with Linked
  // businesses off it shows that alone, and asks for no business's chats.
  const linksOn = areaOn(me?.areas, "links");
  const manage = can("link.manage") && linksOn;
  const { view, act, answer } = useLinks();
  const [params, setParams] = useSearchParams();
  const convs = useLoad(
    () =>
      linksOn
        ? whenOff(get<{ conversations: Conversation[] }>("/v1/link-chats"), {
            conversations: [],
          })
        : Promise.resolve({ conversations: [] as Conversation[] }),
    [linksOn],
  );
  useLiveRefresh(convs.reload);
  const reloadConvs = convs.reload; // stable
  // The list follows the conversations every few seconds while the page
  // is seen (the typing line, the unread counts, the last message), as
  // the open conversation follows itself; 30 s otherwise (useLiveRefresh).
  const live = useChatLive();
  const connected = live.connected;
  useEffect(() => {
    if (!linksOn) return;
    const t = setInterval(
      () => document.visibilityState === "visible" && reloadConvs(),
      connected ? 20_000 : 5_000,
    );
    return () => clearInterval(t);
  }, [reloadConvs, connected, linksOn]);
  // The stream says when a conversation changed: the list follows at once.
  useEffect(() => {
    let soon: number | undefined;
    const off = live.subscribe(() => {
      window.clearTimeout(soon);
      soon = window.setTimeout(reloadConvs, 300);
    });
    return () => {
      off();
      window.clearTimeout(soon);
    };
  }, [live, reloadConvs]);
  // A link accepted or ended here changes the chats too.
  const linksSeen = view.data;
  useEffect(() => {
    if (linksSeen) reloadConvs();
  }, [linksSeen, reloadConvs]);
  const [filter, setFilter] = useState<
    "all" | "unread" | "starred" | "archived"
  >("all");
  // The messages this person starred, when that view is chosen (again: a
  // star may have changed since).
  const [starTick, setStarTick] = useState(0);
  const starredMsgs = useLoad(
    () =>
      filter === "starred"
        ? get<{ messages: LinkMessage[] }>("/v1/link-chats/starred").then(
            (r) => r.messages,
          )
        : Promise.resolve([] as LinkMessage[]),
    [filter, starTick],
  );
  const archived = filter === "archived";
  const [foundMessages, setFoundMessages] = useState<LinkMessage[]>();
  const settings = useLoad(
    () =>
      linksOn
        ? get<LinkSettings>("/v1/link-settings")
        : Promise.resolve(undefined),
    [linksOn],
  );
  const [q, setQ] = useState("");
  const [found, setFound] = useState<LinkSearch>();
  const [searchError, setSearchError] = useState<unknown>();
  const [asking, setAsking] = useState<Asking>();
  // Bumped when a request shown in the results is withdrawn, so the
  // search runs again and its cards read right.
  const [searchTick, setSearchTick] = useState(0);
  const query = q.trim();
  useEffect(() => {
    if (!linksOn || query.length < 2) {
      setFound(undefined);
      return;
    }
    let live = true;
    const t = setTimeout(() => {
      get<LinkSearch>(`/v1/links/search?q=${encodeURIComponent(query)}`).then(
        (r) => live && (setFound(r), setSearchError(undefined)),
        (e: unknown) => live && setSearchError(e),
      );
      get<{ messages: LinkMessage[] }>(
        `/v1/link-chats/search?q=${encodeURIComponent(query)}`,
      ).then(
        (r) => live && setFoundMessages(r.messages),
        () => undefined,
      );
    }, 150);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [query, searchTick, linksOn]);
  const g = groupLinks(view.data);
  // From the first letter the list narrows to what matches, as WhatsApp's
  // does; from two, other businesses and messages are looked for too.
  const searching = linksOn && query.length >= 2;
  const all = convs.data?.conversations ?? [];
  const drafts = useDrafts();
  const taskiemUnread = useTaskiemUnread();
  const openID = params.get("with");
  const open = all.find((c) => c.link === openID);
  // The person's own conversation with Taskiem, pinned at the top (§5.5).
  const taskiemOpen = openID === "taskiem";
  const unreadCount = all.filter((c) => !c.archived && c.unread > 0).length;
  const archivedCount = all.filter((c) => c.archived).length;
  const narrowed = linksOn && query.length === 1 ? matchChats(all, query) : all;
  const listed = narrowed.filter(
    (c) => c.archived === archived && (filter !== "unread" || c.unread > 0),
  );
  // The keyboard (a desktop reader's hands): shortcuts that are off while a
  // field has the focus, arrows through the list, and Esc, which closes the
  // chat after whatever panel is open has closed.
  const searchBox = useRef<HTMLInputElement>(null);
  const [keysOpen, setKeysOpen] = useState(false);
  const [hint, setHint] = useState("");
  const order = ["taskiem", ...listed.map((c) => c.link)];
  const keys = useRef({ order, openID: openID ?? undefined, setParams });
  keys.current = { order, openID: openID ?? undefined, setParams };
  // Asking a business to link starts at the search box (the "n" key, and the
  // round button of the list).
  const startNew = useRef<() => void>(() => undefined);
  useEffect(() => {
    const focusSearch = () =>
      requestAnimationFrame(() =>
        requestAnimationFrame(() => {
          searchBox.current?.focus();
          searchBox.current?.select();
        }),
      );
    startNew.current = () => {
      setHint(
        "Type the business's name or phone number, then choose Request link.",
      );
      keys.current.setParams({});
      focusSearch();
    };
    const onKey = (e: KeyboardEvent) => {
      const k = keys.current;
      if (e.key === "Escape") {
        // The panel, the menu, the sheet or the reply is closed first, by
        // whatever owns it (it says so by taking the key).
        if (e.defaultPrevented || !k.openID) return;
        if (
          document.querySelector(
            '[data-testid="chat-more"], [data-testid="chat-recording"], .chat-menu, .attach-menu, .chat-info, .modal, .mention-list, .emoji-panel',
          )
        )
          return;
        if (e.target === searchBox.current) return;
        const id = k.openID;
        k.setParams({});
        requestAnimationFrame(() =>
          document
            .querySelector<HTMLElement>(`.chat-list-pane a[href$="with=${id}"]`)
            ?.focus(),
        );
        return;
      }
      // Not under an open menu, sheet or panel: the letters are theirs.
      const covered = !!document.querySelector(
        '[data-testid="chat-more"], [data-testid="chat-recording"], .chat-menu, .attach-menu, .chat-info, .modal, .mention-list, .emoji-panel',
      );
      const what = shortcutOf(e, covered || isTyping(document.activeElement));
      if (!what || e.defaultPrevented) return;
      e.preventDefault();
      if (what === "help") setKeysOpen(true);
      else if (what === "search" || what === "new") {
        if (what === "new") return startNew.current();
        k.setParams({});
        focusSearch();
      } else {
        const to = step(k.order, k.openID, what === "next" ? 1 : -1);
        if (to) k.setParams({ with: to });
      }
    };
    const showKeys = () => setKeysOpen(true);
    document.addEventListener("keydown", onKey);
    window.addEventListener(KEYS_EVENT, showKeys);
    return () => {
      document.removeEventListener("keydown", onKey);
      window.removeEventListener(KEYS_EVENT, showKeys);
    };
  }, []);
  // Arrows in the list: down from the search box to the first chat, then
  // chat to chat; up from the first back to the box. Enter opens (a link).
  const onListKey = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
    const box = searchBox.current;
    // Everything that can be pressed below the search box and the chips: the
    // chats, and the results, requests and asks the lists hold.
    const rows = Array.from(
      e.currentTarget.querySelectorAll<HTMLElement>(
        ":is(section, ul.chat-rows) :is(a[href], button:not(:disabled))",
      ),
    ).filter((x) => !x.closest(".chat-chips, .chat-list-head, .card"));
    const at = rows.indexOf(e.target as HTMLElement);
    if (at < 0 && e.target !== box) return;
    const next =
      e.target === box
        ? e.key === "ArrowDown"
          ? rows[0]
          : undefined
        : e.key === "ArrowDown"
          ? rows[at + 1]
          : (rows[at - 1] ?? box ?? undefined);
    if (!next) return;
    e.preventDefault();
    next.focus();
  };
  const nameOf = (link: string) =>
    all.find((c) => c.link === link)?.name ?? "They";
  const roleOf = (link: string) => {
    const l = view.data?.links.find((x) => x.id === link);
    return l ? roleLine(l.role) : "";
  };
  // A chat is found by its name or by a word of its last message.
  const chatHits = searching ? matchChats(all, query) : [];
  const chats = searching
    ? (view.data?.links ?? []).filter(
        (l) =>
          matchLinked([l], query).length > 0 ||
          chatHits.some((c) => c.business === l.business),
      )
    : [];
  const blind = found?.blind;
  const others = (found?.results ?? []).filter(
    (c) => !chats.some((l) => l.business === c.business),
  );
  return (
    <>
      <h1 className="sr-only">Chat</h1>
      <div className={`chat-layout${open || taskiemOpen ? " open" : ""}`}>
        <div className="chat-list-pane" onKeyDown={onListKey}>
          <div className="chat-list-head">
            <h2>Chat</h2>
            {linksOn && (
              <button
                type="button"
                className="new-chat"
                data-testid="new-chat"
                aria-label="Find a business to link with"
                onClick={() => startNew.current()}
              >
                <Icon name="plus" />
                <span className="fab-label">Find a business</span>
              </button>
            )}
            <HeadMenu
              label="More about the chats"
              items={(close) => (
                <>
                  <Link
                    className="menu-link"
                    to="/settings/links"
                    onClick={close}
                  >
                    Linked businesses
                  </Link>
                  <Link
                    className="menu-link"
                    to="/settings/links/profile"
                    onClick={close}
                  >
                    Your business in Explore
                  </Link>
                </>
              )}
            />
          </div>
          {linksOn && (
            <div className="filters" role="search" aria-label="Find a business">
              <label className="search">
                <span className="sr-only">Find a business</span>
                <Icon name="search" />
                <input
                  type="search"
                  ref={searchBox}
                  placeholder="Search or find a business"
                  value={q}
                  onChange={(e) => (setQ(e.target.value), setHint(""))}
                  onKeyDown={(e) => {
                    // Esc empties the box; with it empty, the key goes on.
                    if (e.key === "Escape") {
                      e.preventDefault();
                      if (q) {
                        setQ("");
                        setHint("");
                      } else e.currentTarget.blur();
                    }
                  }}
                  data-testid="link-search"
                />
              </label>
            </div>
          )}
          <p className="sr-only" role="status">
            {hint}
          </p>
          {!searching && all.length > 0 && (
            <div className="chat-chips" role="group" aria-label="Show">
              {(
                [
                  ["all", "All"],
                  [
                    "unread",
                    unreadCount ? `Unread (${unreadCount})` : "Unread",
                  ],
                  ["starred", "Starred"],
                  ["archived", `Archived (${archivedCount})`],
                ] as const
              ).map(([key, label]) => (
                <button
                  key={key}
                  type="button"
                  className="chat-chip"
                  aria-pressed={filter === key}
                  onClick={() => {
                    setFilter(key);
                    if (key === "starred") setStarTick((n) => n + 1);
                  }}
                >
                  {label}
                </button>
              ))}
            </div>
          )}
          <ErrorBox error={view.error ?? act.error ?? searchError} />
          {!searching &&
            !archived &&
            filter !== "starred" &&
            (query.length === 0 ||
              "taskiem".startsWith(query.toLowerCase())) && (
              <ul
                className="chat-rows"
                aria-label="Your conversation with Taskiem"
              >
                <ChatRow
                  to="/chat?with=taskiem"
                  letter="T"
                  accent
                  title="Taskiem"
                  preview={
                    drafts.taskiem ? (
                      <>
                        <span className="draft">Draft:</span> {drafts.taskiem}
                      </>
                    ) : (
                      "Your own conversation with Taskiem, as on your phone"
                    )
                  }
                  selected={taskiemOpen}
                  unread={taskiemUnread}
                  testid="taskiem-chat"
                />
              </ul>
            )}
          {searching ? (
            <section aria-label="Search results" data-testid="link-results">
              {chats.length > 0 && (
                <>
                  <h2>Chats</h2>
                  <ul
                    className="chat-rows"
                    aria-label="Linked businesses found"
                  >
                    {chats.map((l) => {
                      const conv = all.find((c) => c.business === l.business);
                      return conv ? (
                        <ChatRow
                          key={l.id}
                          to={`/chat?with=${conv.link}`}
                          letter={conv.name}
                          logo={conv.logo ? logoSrc(conv.business) : undefined}
                          title={conv.name}
                          preview={roleLine(l.role)}
                          unread={conv.unread}
                          selected={openID === conv.link}
                          testid="link-chat"
                        />
                      ) : (
                        <Row
                          key={l.id}
                          letter={l.name}
                          title={l.name}
                          side={<StateBadge state={l.state} />}
                          sub={<span className="sub">{roleLine(l.role)}</span>}
                          testid="link-chat"
                        />
                      );
                    })}
                  </ul>
                </>
              )}
              {(foundMessages?.length ?? 0) > 0 && (
                <>
                  <h2>Messages</h2>
                  <ul className="chat-rows" aria-label="Messages found">
                    {foundMessages?.map((m) => (
                      <ChatRow
                        key={m.id}
                        to={`/chat?with=${m.link}&msg=${m.id}`}
                        letter={nameOf(m.link)}
                        title={nameOf(m.link)}
                        time={listTime(m.created_at)}
                        preview={previewLine(m, nameOf(m.link))}
                        testid="chat-found-message"
                      />
                    ))}
                  </ul>
                </>
              )}
              <h2>Businesses on Taskiem</h2>
              {blind ? (
                <Items label="Send a request">
                  <Row
                    icon="chat"
                    title={`Send a link request to ${blind.shown}`}
                    sub={
                      <span className="sub">{blindRowLine(blind.kind)}</span>
                    }
                    testid="link-blind"
                  >
                    {manage && (
                      <button
                        type="button"
                        className="small"
                        onClick={() => setAsking(blindRequest(blind, query))}
                      >
                        Send request
                      </button>
                    )}
                  </Row>
                </Items>
              ) : !found ? (
                <Skeleton rows={2} label="Searching businesses…" />
              ) : others.length === 0 ? (
                <EmptyState icon="search">
                  No business found for “{query}”. Only businesses that chose to be found are listed.
                </EmptyState>
              ) : (
                <Items label="Businesses on Taskiem">
                  {others.map((c) => {
                    const pending = requestTo(
                      view.data?.links ?? [],
                      c.business,
                    );
                    return (
                      <FoundRow
                        key={c.business}
                        card={c}
                        manage={manage}
                        busy={act.busy}
                        onAsk={() =>
                          setAsking({ business: c.business, name: c.name })
                        }
                        onCancel={
                          pending
                            ? () =>
                                void act.run(async () => {
                                  await post(`/v1/links/${pending.id}/cancel`);
                                  view.reload();
                                  setSearchTick((n) => n + 1);
                                }, "Request withdrawn")
                            : undefined
                        }
                      />
                    );
                  })}
                </Items>
              )}
            </section>
          ) : !view.data ? (
            <Skeleton rows={3} label="Loading your linked businesses…" />
          ) : g.asked.length +
              g.linked.length +
              g.waiting.length +
              g.sent.length ===
            0 ? (
            <EmptyState icon="chat">
              No linked businesses yet. Search for a supplier or a customer that
              is on Taskiem, and ask to link.
            </EmptyState>
          ) : (
            <>
              {g.asked.length > 0 && (
                <section aria-label="Asks to link">
                  <h2>Asks to link</h2>
                  <Items label="Asks to link">
                    {g.asked.map((l) => (
                      <Row
                        key={l.id}
                        letter={l.name}
                        tone="warn"
                        title={l.name}
                        side={<StateBadge state={l.state} />}
                        sub={
                          <span className="sub">
                            {roleLine(l.role)} · asked {fmtTime(l.requested_at)}
                          </span>
                        }
                        testid="link-asked"
                      >
                        {manage ? (
                          <AnswerButtons
                            link={l}
                            answer={answer}
                            busy={act.busy}
                          />
                        ) : (
                          <span className="hint">
                            An owner or admin answers this.
                          </span>
                        )}
                      </Row>
                    ))}
                  </Items>
                </section>
              )}
              {filter === "starred" && (
                <section aria-label="Starred messages">
                  <h2>Starred messages</h2>
                  {!starredMsgs.data ? (
                    <Skeleton rows={2} label="Loading your starred messages…" />
                  ) : starredMsgs.data.length === 0 ? (
                    <p className="hint">
                      Nothing starred yet. Open a message’s menu and choose Star
                      to keep it here.
                    </p>
                  ) : (
                    <ul className="chat-rows" aria-label="Starred messages">
                      {starredMsgs.data.map((m) => (
                        <ChatRow
                          key={m.id}
                          to={`/chat?with=${m.link}&msg=${m.id}`}
                          letter={nameOf(m.link)}
                          title={nameOf(m.link)}
                          time={listTime(m.created_at)}
                          preview={previewLine(m, nameOf(m.link))}
                          testid="starred-message"
                        />
                      ))}
                    </ul>
                  )}
                </section>
              )}
              {filter !== "starred" && all.length > 0 && (
                <section aria-label="Chats">
                  <div className="toolbar" style={{ marginBottom: 6 }}>
                    <h2
                      className={archived ? "grow" : "grow sr-only"}
                      style={{ margin: 0 }}
                    >
                      {archived ? "Archived" : "Chats"}
                    </h2>
                  </div>
                  {listed.length === 0 && (
                    <p className="hint">
                      {query.length === 1
                        ? `No chat matches “${query}”.`
                        : archived
                          ? "No archived chats."
                          : filter === "unread"
                            ? "Nothing unread."
                            : "No chats."}
                    </p>
                  )}
                  <ul
                    className="chat-rows"
                    aria-label={archived ? "Archived chats" : "Chats"}
                  >
                    {listed.map((c) => (
                      <ChatRow
                        key={c.link}
                        to={`/chat?with=${c.link}`}
                        letter={c.name}
                        logo={c.logo ? logoSrc(c.business) : undefined}
                        title={c.name}
                        time={c.last ? listTime(c.last.created_at) : undefined}
                        lead={
                          c.last?.mine && c.last.kind !== "card" ? (
                            <Ticks read={!!c.last.read_at} />
                          ) : undefined
                        }
                        preview={
                          drafts[c.link] ? (
                            <>
                              <span className="draft">Draft:</span>{" "}
                              {drafts[c.link]}
                            </>
                          ) : (
                              connected ? live.typing.has(c.link) : c.typing
                            ) ? (
                            "typing…"
                          ) : c.last ? (
                            previewLine(c.last, c.name).replace(
                              c.last.mine && c.last.kind !== "card"
                                ? /^You: /
                                : /^$/,
                              "",
                            )
                          ) : c.state === "active" ? (
                            `Linked · ${roleOf(c.link)}`
                          ) : (
                            "The link has ended"
                          )
                        }
                        typing={connected ? live.typing.has(c.link) : c.typing}
                        unread={c.unread}
                        pinned={c.pinned}
                        muted={c.muted}
                        selected={openID === c.link}
                        testid="link-linked"
                      />
                    ))}
                  </ul>
                </section>
              )}
              {g.waiting.length + g.sent.length > 0 && (
                <section aria-label="Waiting for an answer">
                  <h2>Waiting for an answer</h2>
                  <Items label="Waiting for an answer">
                    {g.waiting.map((l) => (
                      <Row
                        key={l.id}
                        letter={l.name}
                        tone="info"
                        title={l.name}
                        side={<StateBadge state={l.state} />}
                        sub={
                          <span className="sub">
                            Asked {fmtTime(l.requested_at)}
                          </span>
                        }
                        testid="link-waiting"
                      />
                    ))}
                    {g.sent.map((b) => (
                      <Row
                        key={b.id}
                        icon="chat"
                        tone="info"
                        title={b.shown}
                        side={<StateBadge state="sent" />}
                        sub={
                          <span className="sub">
                            Sent {fmtTime(b.created_at)}
                          </span>
                        }
                        testid="link-sent"
                      />
                    ))}
                  </Items>
                </section>
              )}
            </>
          )}
          {manage &&
            settings.data &&
            !settings.data.asked_at &&
            !searching &&
            filter !== "starred" && (
              <FindableQuestion
                settings={settings.data}
                busy={act.busy}
                answer={(findable) =>
                  void act.run(
                    async () => (
                      await put("/v1/link-settings", { findable }),
                      settings.reload()
                    ),
                    findable ? "Other businesses can find you" : false,
                  )
                }
              />
            )}
          {keysOpen && <KeysSheet onClose={() => setKeysOpen(false)} />}
          {asking && (
            <RequestSheet
              asking={asking}
              onClose={() => setAsking(undefined)}
              onSent={() => (view.reload(), setQ(""))}
            />
          )}
        </div>
        {taskiemOpen && <TaskiemPane onBack={() => setParams({})} />}
        {open && (
          <ConversationPane
            key={open.link}
            conv={open}
            others={all}
            onBack={() => setParams({})}
            onChanged={reloadConvs}
            focus={params.get("msg") ?? undefined}
            link={view.data?.links.find((l) => l.id === open.link)}
            onEndLink={
              manage
                ? (l) => {
                    answer(l, "end", "Link ended");
                    reloadConvs();
                  }
                : undefined
            }
          />
        )}
      </div>
    </>
  );
}

function FoundRow({
  card,
  manage,
  busy,
  onAsk,
  onCancel,
}: {
  card: LinkCard;
  manage: boolean;
  busy?: boolean;
  onAsk: () => void;
  /** Withdraws the request we sent this business, when one waits. */
  onCancel?: () => void;
}) {
  const action = cardAction(card);
  return (
    <Row
      letter={card.name}
      title={card.name}
      side={<StateBadge state={card.state} />}
      sub={<span className="sub">{cardSub(card)}</span>}
      testid="link-found"
    >
      {action === "request" &&
        (manage ? (
          <button type="button" className="small" onClick={onAsk}>
            Request link<span className="sr-only"> with {card.name}</span>
          </button>
        ) : (
          <span className="hint">
            An owner or admin can link with this business.
          </span>
        ))}
      {card.state === "requested" && manage && onCancel && (
        <button
          type="button"
          className="small"
          disabled={busy}
          onClick={onCancel}
        >
          Cancel request<span className="sr-only"> to {card.name}</span>
        </button>
      )}
      {action === "answer" && (
        <span className="hint">They asked you: answer under Asks to link.</span>
      )}
    </Row>
  );
}

/**
 * Linked businesses (under Secrets & settings): whether other businesses
 * find this one, and every link and request with what can be done to it.
 */
export function LinkedBusinessesPage() {
  const { can } = useAuth();
  const manage = can("link.manage");
  const { view, act, answer } = useLinks();
  const settings = useLoad(
    () => get<LinkSettings & { url?: string }>("/v1/link-settings"),
    [],
  );
  const g = groupLinks(view.data);
  const setFindable = (on: boolean) =>
    void act.run(
      async () => (
        await put("/v1/link-settings", { findable: on }),
        settings.reload()
      ),
      "Saved",
    );
  const all = [...g.asked, ...g.linked, ...g.waiting, ...g.past];
  return (
    <>
      <p className="crumbs">
        <Link to="/chat" aria-label="Back to Chat">
          Chat
        </Link>
      </p>
      <PageHeader
        title="Linked businesses"
        description="Businesses on Taskiem you have linked with, and requests either way. Each side keeps its own records."
      />
      <ErrorBox error={view.error ?? settings.error ?? act.error} />
      <section
        className="card"
        aria-label="Being found"
        data-testid="link-findable"
      >
        <h2>Let other businesses find us</h2>
        {settings.data ? (
          <>
            <label className="switch-line">
              <input
                type="checkbox"
                aria-label="Let other businesses find us"
                checked={settings.data.findable_now}
                disabled={!manage || act.busy}
                onChange={(e) => setFindable(e.target.checked)}
              />
              <span>{findableLine(settings.data)}</span>
            </label>
            <p className="hint">
              A search shows only what your storefront already shows the public.
              Someone with your phone number or page address can always ask.
            </p>
          </>
        ) : (
          <Skeleton rows={1} label="Loading the setting…" />
        )}
      </section>
      <section
        className="card"
        aria-labelledby="link-profile"
        data-testid="link-profile-card"
      >
        <h2 id="link-profile">How other businesses see you</h2>
        <p className="hint">
          Your name, a line about what you do, your area, a logo and up to 12
          photos.
        </p>
        <Link className="button" to="/settings/links/profile">
          Edit your business in Explore
        </Link>
      </section>
      {settings.data?.url && <CodeCard url={settings.data.url} />}
      <section aria-label="Links">
        {!view.data ? (
          <Skeleton rows={3} label="Loading your linked businesses…" />
        ) : all.length + g.sent.length === 0 ? (
          <EmptyState
            icon="chat"
            action={
              <Link className="button" to="/chat">
                Find a business
              </Link>
            }
          >
            No links yet.
          </EmptyState>
        ) : (
          <Items label="Linked businesses and requests">
            {all.map((l) => (
              <Row
                key={l.id}
                letter={l.name}
                tone={stateWords(l.state)?.tone}
                title={l.name}
                side={<StateBadge state={l.state} />}
                sub={
                  <span className="sub">
                    {roleLine(l.role)}
                    <span className="block">
                      {l.state === "active" && l.answered_at
                        ? `Linked ${fmtTime(l.answered_at)}`
                        : `Asked ${fmtTime(l.requested_at)}`}
                    </span>
                  </span>
                }
                testid="link-row"
                attrs={{ "data-state": l.state }}
              >
                {manage && (
                  <LinkActions link={l} answer={answer} busy={act.busy} />
                )}
              </Row>
            ))}
            {g.sent.map((b) => (
              <Row
                key={b.id}
                icon="chat"
                tone="info"
                title={b.shown}
                side={<StateBadge state="sent" />}
                sub={<span className="sub">Sent {fmtTime(b.created_at)}</span>}
                testid="link-row"
                attrs={{ "data-state": "sent" }}
              >
                {manage && (
                  <button
                    type="button"
                    className="small"
                    disabled={act.busy}
                    onClick={() => answer(b, "remove", "Taken off the list")}
                  >
                    Take off the list<span className="sr-only"> {b.shown}</span>
                  </button>
                )}
              </Row>
            ))}
          </Items>
        )}
      </section>
    </>
  );
}

function LinkActions({
  link,
  answer,
  busy,
}: {
  link: BusinessLink;
  answer: (l: BusinessLink, a: LinkAnswer, done: string) => void;
  busy: boolean;
}) {
  const who = <span className="sr-only"> {link.name}</span>;
  const { confirm, node } = useConfirm();
  switch (link.state) {
    case "asked_us":
      return <AnswerButtons link={link} answer={answer} busy={busy} />;
    case "active":
      return (
        <>
          {node}
          <button
            type="button"
            className="small danger"
            disabled={busy}
            onClick={async () =>
              (await confirm({
                title: `End the link with ${link.name}?`,
                body: "What passed between you stays on both sides.",
                ok: "End link",
                danger: true,
              })) && answer(link, "end", "Link ended")
            }
          >
            End link{who}
          </button>
        </>
      );
    case "requested":
    case "not_accepted":
      return (
        <button
          type="button"
          className="small"
          disabled={busy}
          onClick={() => answer(link, "cancel", "Request withdrawn")}
        >
          {link.state === "requested"
            ? "Withdraw request"
            : "Take off the list"}
          {who}
        </button>
      );
    case "blocked":
      return (
        <button
          type="button"
          className="small"
          disabled={busy}
          onClick={() => answer(link, "unblock", "Unblocked")}
        >
          Unblock{who}
        </button>
      );
    default:
      return null;
  }
}

// A data: URL, not a blob: one: the console's Content Security Policy allows only data: images besides its own.
function dataURL(b: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result));
    r.onerror = () =>
      reject(r.error ?? new Error("could not read the QR code"));
    r.readAsDataURL(b);
  });
}

/** The business's link code: the address that opens a request to it, and
 * its QR code, to print on a counter card or share. */
function CodeCard({ url }: { url: string }) {
  const [qr, setQr] = useState<string>();
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    let live = true;
    getBlob("/v1/link-settings/qr")
      .then(dataURL)
      .then(
        (u) => live && setQr(u),
        () => undefined,
      );
    return () => {
      live = false;
    };
  }, [url]);
  return (
    <section
      className="card"
      aria-labelledby="link-code"
      data-testid="link-code"
    >
      <h2 id="link-code">Link with us on Taskiem</h2>
      <p className="hint">
        A business on Taskiem that opens this or scans the code can ask to link
        with you, whether or not others find you by search.
      </p>
      <div className="sf-share">
        {qr ? (
          <img
            className="sf-qr"
            src={qr}
            alt={`QR code for ${url}`}
            width={160}
            height={160}
          />
        ) : (
          <Skeleton rows={1} label="Loading the QR code…" />
        )}
        <div>
          <p className="mono" data-testid="link-code-url">
            {url}
          </p>
          <div className="toolbar">
            <button
              type="button"
              onClick={() =>
                void navigator.clipboard
                  ?.writeText(url)
                  .then(() => setCopied(true))
              }
            >
              {copied ? "Copied" : "Copy the link"}
            </button>
            <a
              className="button"
              href="/v1/link-settings/qr?format=png"
              download="link-with-us-qr.png"
            >
              Download the QR code
            </a>
          </div>
        </div>
      </div>
    </section>
  );
}

/**
 * The page a link code opens (/l/<code>): the business behind it, our
 * link with it, and Request link. A code printed on an invoice or a
 * counter card reaches a business whether or not it lets search find it.
 */
export function LinkCodePage() {
  const { code = "" } = useParams();
  const { can } = useAuth();
  const navigate = useNavigate();
  const card = useLoad(
    () =>
      get<{ business: string; name: string; state: LinkCard["state"] }>(
        `/v1/links/code/${encodeURIComponent(code)}`,
      ),
    [code],
  );
  const [asking, setAsking] = useState(false);
  const c = card.data;
  return (
    <>
      <p className="crumbs">
        <Link to="/chat" aria-label="Back to Chat">
          Chat
        </Link>
      </p>
      <PageHeader title={c ? c.name : "Link with a business"} />
      {card.error ? (
        <EmptyState
          icon="chat"
          action={
            <Link className="button" to="/chat">
              Find a business
            </Link>
          }
        >
          This link does not open a business you can link with. It may be your
          own, or mistyped.
        </EmptyState>
      ) : !c ? (
        <Skeleton rows={2} label="Loading the business…" />
      ) : (
        <section className="card" data-testid="link-code-card">
          <h2>
            {c.name} <StateBadge state={c.state} />
          </h2>
          {cardAction({ ...c, items: [] }) === "request" ? (
            can("link.manage") ? (
              <>
                <p className="hint">
                  Linked, your invoices and orders reach each other by
                  themselves. Nothing is shared until they accept.
                </p>
                <button
                  type="button"
                  className="primary"
                  onClick={() => setAsking(true)}
                >
                  Request link
                </button>
              </>
            ) : (
              <p className="hint">
                An owner or admin can link with this business.
              </p>
            )
          ) : c.state === "asked_us" ? (
            <p className="hint">
              They asked you already: answer under <Link to="/chat">Chat</Link>.
            </p>
          ) : (
            <p className="hint">
              Nothing to do here. Your linked businesses are under{" "}
              <Link to="/settings/links">Linked businesses</Link>.
            </p>
          )}
        </section>
      )}
      {asking && c && (
        <RequestSheet
          asking={{ code, name: c.name }}
          onClose={() => setAsking(false)}
          onSent={() => navigate("/chat")}
        />
      )}
    </>
  );
}

/** A link code opened signed out: sign in, or sign the business up first. */
export function LinkCodeSignedOut() {
  const next = encodeURIComponent(window.location.pathname);
  return (
    <div className="login card">
      <h1>Link with a business on Taskiem</h1>
      <p>
        A business on Taskiem shared this so you can link: your invoices and
        orders then reach each other by themselves. Sign in to your business to
        ask.
      </p>
      <p>
        <Link className="button primary" to={`/login?next=${next}`}>
          Sign in
        </Link>
      </p>
      <p className="hint">
        Not on Taskiem yet? <Link to="/signup">Sign up your business</Link>,
        then open the link again.
      </p>
    </div>
  );
}

/** Asked once, of whoever may answer for the business's links: whether
 * other businesses find it by search. Not now keeps the default (on with a
 * published storefront). */
function FindableQuestion({
  settings,
  busy,
  answer,
}: {
  settings: LinkSettings;
  busy: boolean;
  answer: (findable: boolean | null) => void;
}) {
  return (
    <section
      className="card"
      aria-labelledby="link-findable-ask"
      data-testid="link-findable-ask"
    >
      <h2 id="link-findable-ask">Let other businesses on Taskiem find you?</h2>
      <p className="hint">
        They would find you by your name, what you sell and your area, from what
        your storefront already shows, or from your{" "}
        <Link to="/settings/links/profile">business profile</Link>.{" "}
        {findableLine(settings)}
      </p>
      <div className="row-actions">
        <button
          type="button"
          className="primary"
          disabled={busy}
          onClick={() => answer(true)}
        >
          Yes, let them find us
        </button>
        <button type="button" disabled={busy} onClick={() => answer(null)}>
          Not now
        </button>
      </div>
    </section>
  );
}

/** The keys of the Chat page, for a reader who has a keyboard. */
function KeysSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard" onClose={onClose}>
      <dl className="keys" data-testid="keys-sheet">
        {SHORTCUTS.map((s) => (
          <div key={s.does} className="keys-row">
            <dt>
              {s.keys.map((k, i) => (
                <span key={k}>
                  {i > 0 && <span className="sr-only"> then </span>}
                  <kbd>{k}</kbd>{" "}
                </span>
              ))}
            </dt>
            <dd>{s.does}</dd>
          </div>
        ))}
      </dl>
      <p className="hint">The letters work when no box is being typed in.</p>
      <div className="row-actions">
        <button type="button" onClick={onClose}>
          Close
        </button>
      </div>
    </Modal>
  );
}
