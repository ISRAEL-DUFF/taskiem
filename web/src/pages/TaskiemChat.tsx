import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { TOOLS, type ToolKey } from "../lib/tools";
import { DiaryLine } from "../kit";
import { useChatTools } from "../lib/useChatTools";
import { useThread } from "../lib/useThread";
import { ThreadJump } from "./ThreadParts";
import { ChatStream } from "./ChatStream";
import { ChatHead } from "./ChatHead";
import { ReadMore, useFold } from "./ReadMore";
import { ComposerView, TASKIEM_FILES, useComposer } from "./ChatComposer";
import { AlertCards, useChatAlerts } from "./ChatAlerts";
import { BriefingCard, ChartCard } from "./ChatStaff";
import { useAuth } from "../auth";
import { asksBriefing, showBriefing, type SalesChart } from "../lib/staff";

/** A chart message's numbers, or undefined when they cannot be read. */
function parseChart(text: string): SalesChart | undefined {
  try {
    const v = JSON.parse(text) as SalesChart;
    return Array.isArray(v.bars) ? v : undefined;
  } catch {
    return undefined;
  }
}
import { BubbleShell, CopyItem, Ticks } from "./BubbleShell";
import { useChatLive } from "./ChatLive";
import { get, post, upload } from "../api";
import type { Conversation } from "../lib/linkchat";
import { categoriesOf } from "../lib/mentions";
import type { StockItem, StockView } from "../lib/stock";
import { priceWords, type SupplierPrices } from "../lib/pricelists";
import { Icon, TaskiemMark } from "../icons";
import { clock } from "../lib/linkchat";
import {
  boldParts,
  inflight,
  inflightOutcome,
  keepInflight,
  mergeTaskiem,
  NIL,
  type TaskiemMessage,
} from "../lib/taskiemchat";
import { markSeen, seenAt, unreadAfter } from "../lib/taskiemSeen";
import { setDraft } from "../lib/drafts";
import { toast } from "../toast";
import { ErrorBox, fmtTime, useAction, useLoad } from "../ui";

// The Taskiem conversation on the web (business links L3, §5.5): the
// person's own conversation with Taskiem, the one they have on WhatsApp,
// with the same words (status, orders, cash in, invoice …) and the same
// history from every channel. It is the same chat as a business's, in what
// it scrolls, sends and looks like (ChatStream, ComposerView); what differs
// is its content: Taskiem's answers carry reply buttons, a message may have
// come from WhatsApp, Telegram or Slack, and "typing…" is Taskiem preparing
// an answer.

const EVERY_MS = 5000;
/** The newest page, and a page older, as the API gives them. */
const PAGE = 50;
const CHANNEL: Record<string, string> = {
  whatsapp: "WhatsApp",
  telegram: "Telegram",
  slack: "Slack",
};

/** The page is being left (a reload, a closed tab): its requests fail. */
let leaving = false;
if (typeof window !== "undefined") {
  window.addEventListener("beforeunload", () => (leaving = true));
  window.addEventListener("pagehide", () => (leaving = true));
  window.addEventListener("pageshow", () => (leaving = false));
}

/** Taskiem's words, *bold* marked, a long answer folded. */
function TaskiemText({ text, gap }: { text: string; gap: boolean }) {
  const fold = useFold(text);
  return (
    <span className="text">
      {boldParts(fold.shown).map((p, k) =>
        p.bold ? <strong key={k}>{p.text}</strong> : p.text,
      )}
      {fold.folded && <ReadMore onClick={fold.more} />}
      {gap && <span className="stamp-gap" aria-hidden="true" />}
    </span>
  );
}

const startsRun = (
  prev: TaskiemMessage | undefined,
  m: TaskiemMessage,
): boolean => !prev || prev.inbound !== m.inbound;

export function TaskiemPane({ onBack }: { onBack: () => void }) {
  const [messages, setMessages] = useState<TaskiemMessage[]>();
  const [error, setError] = useState<unknown>();
  // "@" a linked business, "#" an item of a supplier's price list.
  const convs = useLoad(
    () =>
      get<{ conversations: Conversation[] }>("/v1/link-chats").then(
        (r) => r.conversations,
        () => [] as Conversation[],
      ),
    [],
  );
  const prices = useLoad(
    () =>
      get<{ suppliers: SupplierPrices[] }>("/v1/supplier-prices").then(
        (r) => r.suppliers,
        () => [] as SupplierPrices[],
      ),
    [],
  );
  const act = useAction();
  const chatTools = useChatTools();
  const { can, me } = useAuth();
  // The business's own categories: "@sneakers" offers its Sneakers, to
  // open in Products (docs/spec/chat-categories-and-variants.md).
  const ownStock = useLoad(
    () =>
      chatTools && can("table.read")
        ? get<StockView>("/v1/stock").then(
            (r) => r.items,
            () => [] as StockItem[],
          )
        : Promise.resolve([] as StockItem[]),
    [chatTools],
  );
  // Alerts you can act on, as cards at the end (chat-tools.md §6).
  const alerts = useChatAlerts(!!chatTools);
  const afterDiary = useRef<() => void>(() => undefined);
  const localN = useRef(0);
  // The briefing card is here for those who sell or bill; the tools menu
  // and "briefing" typed bring it back.
  const briefs = !!chatTools && (can("storefront.manage") || can("invoice.manage"));
  const c = useComposer<never>({
    draftKey: "taskiem",
    rules: TASKIEM_FILES,
    tools: chatTools,
    // With chat tools, "@" tags things and businesses in groups, and finds
    // businesses not yet linked (their card, with Connect); Taskiem is
    // who is being talked to, so it is not offered.
    grouped: chatTools ? { taskiem: false } : undefined,
    placeholder: chatTools ? "Ask Taskiem, or @ to tag" : undefined,
    diaryWhere: "taskiem",
    // A tool saved: the server's diary line is pulled in.
    // A tool saved a change: its diary line, and the cards it settles.
    onDiary: () => (afterDiary.current(), alerts.reload()),
    extras: briefs
      ? [
          {
            key: "briefing",
            title: "Briefing",
            icon: "sparkle",
            line: "Today's numbers",
            onPick: showBriefing,
          },
        ]
      : undefined,
    sources: {
      "@": (convs.data ?? [])
        .filter((x) => x.state === "active")
        .map((x) => ({ kind: "business" as const, id: x.link, name: x.name })),
      "#": (prices.data ?? []).flatMap((p) =>
        p.items
          .filter((it) => it.available)
          .map((it) => ({
            kind: "item" as const,
            id: `${p.link}:${it.item_id}`,
            name: it.name,
            sub: `${p.business} · ${priceWords(it)}`,
            category: it.category,
            words: it.description,
          })),
      ),
      categories: categoriesOf(ownStock.data ?? []),
    },
    onSend: (s) => {
      if (!s.file && briefs && asksBriefing(s.text)) showBriefing();
      else if (s.file) sendFile(s.file, s.text);
      else say({ text: s.text });
    },
  });
  const last = messages
    ?.filter((m) => !m.local)
    .reduce<string | undefined>((_, m) => m.created_at, undefined);
  // Scrolls as the other conversations do (lib/useThread): older messages
  // load near the top, new ones are followed only at the bottom.
  const [hasOlder, setHasOlder] = useState(false);
  const loadingOlder = useRef(false);
  const loadOlderRef = useRef<() => void>(() => undefined);
  const olderCb = useCallback(() => loadOlderRef.current(), []);
  const thread = useThread({
    items: messages,
    isMine: (m) => m.inbound,
    hasOlder,
    loadOlder: olderCb,
  });
  // Where what Taskiem said while the person was away begins.
  const [unread, setUnread] = useState<{ id: string; count: number }>();

  const pull = useCallback(async (after?: string) => {
    const out = await get<{ messages: TaskiemMessage[] }>(
      `/v1/taskiem-chat${after ? `?after=${encodeURIComponent(after)}` : ""}`,
    );
    if (after && out.messages.length === 0) return;
    setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
    // A full page of what is newer: there may be more (the reader was away).
    const newest = out.messages[out.messages.length - 1];
    if (after && out.messages.length >= 200 && newest)
      await pull(newest.created_at);
  }, []);
  afterDiary.current = () => void pull(messages?.[messages.length - 1]?.created_at).catch(setError);
  // A link from the chat apps ("tools", a typed request) opens its tool,
  // at its form, filled in (chat_tool_requests.go); the link's words go.
  const [params, setParams] = useSearchParams();
  useEffect(() => {
    const tool = params.get("tool") as ToolKey | null;
    if (!chatTools || !tool || !TOOLS.some((t) => t.key === tool)) return;
    const num = (k: string) => {
      const v = params.get(k);
      return v && /^\d+$/.test(v) ? Number(v) : undefined;
    };
    c.openTool(tool, undefined, {
      rowId: params.get("row") ?? undefined,
      add: params.get("add") === "1",
      prefill: { priceKobo: num("price"), count: num("count"), name: params.get("name") ?? undefined, minutes: num("minutes") },
    });
    setParams({ with: "taskiem" }, { replace: true });
    // Once, when the switch is known.
  }, [chatTools]);
  // The newest page: the view is placed at its end.
  useEffect(() => {
    get<{ messages: TaskiemMessage[] }>("/v1/taskiem-chat")
      .then((out) => {
        thread.reset();
        setHasOlder(out.messages.length >= PAGE);
        setUnread(unreadAfter(out.messages, seenAt()));
        setMessages(mergeTaskiem([], out.messages));
        // A message that was on its way when the page went away: Taskiem
        // has it, or the words come back to the box, for the person to check.
        const f = inflight();
        if (f) {
          keepInflight(undefined);
          if (inflightOutcome(out.messages, f) === "not-sent") {
            // Kept as the draft too: the pane may be shown again (a page
            // that is drawn twice as it loads) before this is read.
            setDraft("taskiem", f.text);
            c.setText((cur) => (cur === "" ? f.text : cur));
            toast(
              "Your last message may not have been sent. It is back in the box: look above, then send it again if it is not there.",
              "error",
            );
          }
        }
      })
      .catch(setError);
    // The thread's reset is stable.
  }, []);
  const loadOlder = async () => {
    const first = messages?.[0];
    if (!first || loadingOlder.current) return;
    loadingOlder.current = true;
    try {
      const out = await get<{ messages: TaskiemMessage[] }>(
        `/v1/taskiem-chat?before=${encodeURIComponent(first.created_at)}`,
      );
      if (out.messages.length < PAGE) setHasOlder(false);
      if (out.messages.length > 0) {
        thread.keepPlace();
        setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
      }
    } catch (e) {
      setError(e);
    } finally {
      loadingOlder.current = false;
    }
  };
  loadOlderRef.current = () => void loadOlder();
  // What the live stream brings (a message by another channel, the answer to
  // one sent from another tab) joins the conversation as it comes; with it
  // open the polling only reconciles.
  const chat = useChatLive();
  const connected = chat.connected;
  useEffect(
    () =>
      chat.subscribe((e) => {
        if (e.type === "taskiem")
          setMessages((cur) => mergeTaskiem(cur ?? [], e.messages));
      }),
    [chat],
  );
  useEffect(() => {
    const t = setInterval(
      () => {
        if (document.visibilityState === "visible")
          pull(last).catch(() => undefined);
      },
      connected ? EVERY_MS * 4 : EVERY_MS,
    );
    return () => clearInterval(t);
  }, [pull, last, connected]);
  // Seen: what is at the screen, in a window in use, at the newest end.
  useEffect(() => {
    const see = () => {
      if (
        !last ||
        thread.away ||
        document.visibilityState !== "visible" ||
        !document.hasFocus()
      )
        return;
      markSeen(last);
    };
    see();
    window.addEventListener("focus", see);
    return () => window.removeEventListener("focus", see);
  }, [last, thread.away]);

  /** Shown at once with a clock, then what the server says. */
  const say = (body: { text?: string; reply?: string }) => {
    const mine: TaskiemMessage | undefined = body.text
      ? {
          id: `local-${++localN.current}`,
          channel: "web",
          inbound: true,
          kind: "text",
          text: body.text,
          created_at: new Date().toISOString(),
          local: true,
        }
      : undefined;
    if (mine) {
      setMessages((cur) => mergeTaskiem(cur ?? [], [mine]));
      // If the page goes away before the answer, it is known on coming back.
      keepInflight({ text: mine.text, at: mine.created_at });
    }
    void act
      .run(async () => {
        const out = await post<{ messages: TaskiemMessage[] }>(
          "/v1/taskiem-chat",
          body,
        );
        setMessages((cur) =>
          mergeTaskiem(
            (cur ?? []).filter((x) => x.id !== mine?.id),
            out.messages,
          ),
        );
      }, false)
      .then((ok) => {
        // The page going away fails the request: that is not a message that
        // did not go, and what is kept for the next page stays.
        if (leaving) return;
        keepInflight(undefined);
        // Not sent: what was typed comes back to the box to be sent again.
        if (ok || !mine) return;
        setMessages((cur) => cur?.filter((x) => x.id !== mine.id));
        if (body.reply === undefined)
          c.setText((cur) => (cur === "" ? mine.text : cur));
      });
  };
  // A voice note or a photo: read as one sent on WhatsApp.
  const sendFile = (f: File, caption = "") =>
    void act
      .run(async () => {
        const form = new FormData();
        form.append("file", f, f.name);
        if (caption) form.append("text", caption);
        const out = await upload<{ messages: TaskiemMessage[] }>(
          "/v1/taskiem-chat/files",
          form,
        );
        setMessages((cur) => mergeTaskiem(cur ?? [], out.messages));
      }, false)
      .then(() => undefined);

  // For each of Taskiem's answers with buttons: the one pressed (the
  // person's next message, when it is the button's title); and which of the
  // person's messages Taskiem has answered (their ticks go blue).
  const { pressed, answered, pressableId } = useMemo(() => {
    const pressed = new Map<string, string>();
    const answered = new Set<string>();
    const list = messages ?? [];
    list.forEach((m, i) => {
      const next = list[i + 1];
      if (!m.inbound && m.buttons?.length && next?.inbound)
        pressed.set(m.id, next.text);
      if (m.inbound && list.slice(i + 1).some((x) => !x.inbound))
        answered.add(m.id);
    });
    // The buttons that press: those of the newest answer that has their ids
    // (the server gives them for an answer not yet answered, even after a
    // reload), if the person has said nothing since.
    let pressableId: string | undefined;
    for (let i = list.length - 1; i >= 0; i--) {
      const m = list[i];
      if (!m) continue;
      if (m.inbound) break;
      if (m.buttons?.some((b) => b.id)) {
        pressableId = m.id;
        break;
      }
    }
    return { pressed, answered, pressableId };
  }, [messages]);

  return (
    <section
      className="chat-pane"
      aria-label="Conversation with Taskiem"
      data-testid="taskiem-pane"
      onKeyDown={c.onPaneKeyDown}
      {...c.dropProps}
    >
      <ChatHead
        onBack={onBack}
        menuLabel="More about this conversation"
        avatar={
          <span className="chat-avatar accent" aria-hidden="true">
            <TaskiemMark />
          </span>
        }
        title="Taskiem"
        subtitle={
          act.busy ? (
            <span className="typing" data-testid="taskiem-typing">
              typing…
            </span>
          ) : (
            (me?.tenant_name ?? "Your assistant")
          )
        }
      />
      <ErrorBox error={error ?? act.error ?? c.fileError} />
      <ChatStream
        items={messages}
        thread={thread}
        hasOlder={hasOlder}
        prefix="taskiem"
        keyOf={(m) => (m.id === NIL ? `${m.id}-${m.created_at}` : m.id)}
        startsRun={startsRun}
        unread={unread}
        empty={
          <>
            This is the conversation you have with Taskiem on your phone. Say{" "}
            <strong>help</strong> to see what it can do, or{" "}
            <strong>status</strong> to see how the business is doing.
          </>
        }
        render={(m, { first }) => {
          // "How did we do?" answered: its chart, as a card.
          if (m.kind === "chart") {
            const chart = parseChart(m.text);
            if (chart)
              return (
                <div className="tk-chart-row" data-msg={m.id}>
                  <ChartCard chart={chart} />
                </div>
              );
          }
          // A change made with a chat tool: a line, not a bubble.
          if (m.kind === "diary")
            return (
              <DiaryLine
                msg={m.id}
                fresh={Date.now() - Date.parse(m.created_at) < 60_000}
                when={
                  <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
                    You, {clock(m.created_at)}
                  </time>
                }
              >
                {m.text}
              </DiaryLine>
            );
          const choice = pressed.get(m.id);
          const pressable = m.id === pressableId;
          // The time sits in the corner of the last line when there are no
          // buttons and it came from here.
          const inline =
            m.channel === "web" && !(m.buttons && m.buttons.length > 0);
          return (
            <BubbleShell
              id={m.id}
              mine={m.inbound}
              first={first}
              testid="taskiem-message"
              what={m.text.split("\n")[0]?.slice(0, 60) ?? "message"}
              menu={
                !m.local && m.text
                  ? () => ({ actions: <CopyItem text={m.text} /> })
                  : undefined
              }
              after={
                <span className={`stamp${inline ? " inline" : ""}`}>
                  {m.channel !== "web" && (
                    <span
                      className="channel-mark"
                      data-testid="taskiem-channel"
                      title={`Sent on ${CHANNEL[m.channel] ?? m.channel}`}
                    >
                      <span className="sr-only">Sent </span>
                      on {CHANNEL[m.channel] ?? m.channel} ·
                    </span>
                  )}
                  <time dateTime={m.created_at} title={fmtTime(m.created_at)}>
                    {clock(m.created_at)}
                  </time>
                  {m.inbound &&
                    (m.local ? (
                      <span
                        className="ticks pending"
                        role="img"
                        aria-label="Sending"
                      >
                        <Icon name="clock" />
                      </span>
                    ) : (
                      <Ticks read={answered.has(m.id)} />
                    ))}
                </span>
              }
            >
              {!m.inbound && first && <span className="who">Taskiem</span>}
              <TaskiemText text={m.text} gap={inline} />
              {m.buttons && m.buttons.length > 0 && (
                <span
                  className="reply-buttons"
                  role="group"
                  aria-label="Replies"
                >
                  {m.buttons.map((b, j) =>
                    pressable && b.id ? (
                      <button
                        key={j}
                        type="button"
                        className="reply-button"
                        disabled={act.busy}
                        onClick={() => say({ reply: b.id, text: b.title })}
                      >
                        {b.title}
                      </button>
                    ) : (
                      <span
                        key={j}
                        className={`reply-button${choice === b.title ? " pressed" : ""}`}
                      >
                        {choice === b.title && (
                          <>
                            <Icon name="tick" />
                            <span className="sr-only">You chose </span>
                          </>
                        )}
                        {b.title}
                      </span>
                    ),
                  )}
                </span>
              )}
            </BubbleShell>
          );
        }}
        overlay={
          thread.away ? (
            <ThreadJump count={thread.newCount} onClick={thread.toBottom} />
          ) : undefined
        }
        tail={
          chatTools ? (
            <>
              {briefs && (
                <BriefingCard openTool={c.openTool} onAsk={(text) => say({ text })} />
              )}
              <AlertCards
              alerts={alerts.list}
              onReload={alerts.reload}
              onDiary={() => afterDiary.current()}
              openTool={c.openTool}
              />
            </>
          ) : undefined
        }
      />
      <ComposerView
        c={c}
        busy={act.busy}
        prefix="taskiem"
        textId="taskiem-text"
        textLabel="Message to Taskiem"
      />
    </section>
  );
}
