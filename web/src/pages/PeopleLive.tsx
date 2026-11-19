import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { post } from "../api";
import type { LiveBasket } from "../lib/baskets";
import { typingByGroup, type LiveGroup, type LiveGroupInvite, type LiveGroupMessage, type LiveGroupReceipts, type LiveGroupTyping } from "../lib/groups";
import { NO_PRESENCE, readySays, shownPresence, withPresence, type LiveChat, type LiveGone, type LiveMessage, type LivePresence, type PeopleLiveEvent, type PresenceMap } from "../lib/peoplelive";

// You's live connection (docs/spec/people-chatting.md, "As built: stage 5a
// gaps, chat features (server)"): one EventSource to GET /v1/me/live for
// the person's own side, telling the people chats, the Chats list and
// Requests what changed as it happens (a message, a chat, something
// deleted elsewhere, who is typing, who is online). The pages poll slowly
// while it is open, as a fallback, and as before while it is not.
//
// Presence rides on it: the connection is in front while the tab is
// visible and away while it is hidden (POST /v1/me/presence {online,
// connection}), and away on leaving the page.
//
// A reconnection after the server ends the connection (every half hour) is
// the browser's own, which sends Last-Event-ID, so what was missed comes
// then; one the page makes itself (a silent connection, a refusal) asks
// the pages to read again once (`ready`).

/** How long the connection may be silent before the page stops trusting
 * it (the server pings at least every ten seconds), how often it looks,
 * and how long it waits after a refusal. A test may change them
 * (window.__taskiemPeopleLive). */
function timing() {
  const t = (window as unknown as { __taskiemPeopleLive?: Partial<Record<"stallMs" | "checkMs" | "retryMs", number>> }).__taskiemPeopleLive ?? {};
  return { stallMs: t.stallMs ?? 30_000, checkMs: t.checkMs ?? 5_000, retryMs: t.retryMs ?? 30_000 };
}

interface PeopleLive {
  /** The connection is open: the pages may poll less. */
  connected: boolean;
  /** The chats whose other person is typing now. */
  typing: ReadonlySet<string>;
  /** Who is typing in each group now (person ids, never the viewer). */
  groupTyping: ReadonlyMap<string, string[]>;
  /** Who is online and when last seen, by person, as the connection told. */
  presence: PresenceMap;
  /** Hears each event; returns how to stop. */
  subscribe: (fn: (e: PeopleLiveEvent) => void) => () => void;
}

const EMPTY = new Set<string>();
const NO_GROUP_TYPING: ReadonlyMap<string, string[]> = new Map();
const NONE: PeopleLive = { connected: false, typing: EMPTY, groupTyping: NO_GROUP_TYPING, presence: NO_PRESENCE, subscribe: () => () => undefined };
const Ctx = createContext<PeopleLive>(NONE);
export const usePeopleLive = () => useContext(Ctx);

export { livePoll } from "../lib/peoplelive";

/** The person's side's live connection, for everything under it. */
export function PeopleLiveProvider({ on, children }: { on: boolean; children: ReactNode }) {
  const [connected, setConnected] = useState(false);
  const [typing, setTyping] = useState<ReadonlySet<string>>(EMPTY);
  const [groupTyping, setGroupTyping] = useState<ReadonlyMap<string, string[]>>(NO_GROUP_TYPING);
  const [presence, setPresence] = useState<PresenceMap>(NO_PRESENCE);
  const subs = useRef(new Set<(e: PeopleLiveEvent) => void>());
  const subscribe = useCallback((fn: (e: PeopleLiveEvent) => void) => {
    subs.current.add(fn);
    return () => void subs.current.delete(fn);
  }, []);

  useEffect(() => {
    if (!on || typeof EventSource === "undefined") return;
    let es: EventSource | undefined;
    let retry: number | undefined;
    let watch: number | undefined;
    let heard = Date.now();
    let stopped = false;
    let connection: string | undefined;
    // What the connection's address said when it was opened: the browser's
    // own reconnection reuses it, `?away=1` and all, so `ready` corrects it.
    let openedAway = false;
    const emit = (e: PeopleLiveEvent) => subs.current.forEach((fn) => fn(e));
    const hear = () => {
      heard = Date.now();
    };
    const data = <T,>(ev: Event) => JSON.parse((ev as MessageEvent<string>).data) as T;
    const front = () => document.visibilityState === "visible";
    // In front or away: said for this connection.
    const say = (online: boolean, keepalive = false) => {
      if (!connection) return;
      const body = { online, connection };
      if (keepalive) {
        // Leaving the page: the request outlives it.
        void fetch("/v1/me/presence", {
          method: "POST",
          keepalive: true,
          credentials: "same-origin",
          headers: { "Content-Type": "application/json", "X-Taskiem-Request": "1" },
          body: JSON.stringify(body),
        }).catch(() => undefined);
        return;
      }
      void post("/v1/me/presence", body).catch(() => undefined);
    };
    const close = () => {
      window.clearInterval(watch);
      es?.close();
      es = undefined;
      connection = undefined;
      setConnected(false);
      setTyping(EMPTY);
      setGroupTyping(NO_GROUP_TYPING);
      // Who is online is known only while connected.
      setPresence(NO_PRESENCE);
    };
    const open = () => {
      hear();
      // Opened while the tab is hidden: away from the start.
      openedAway = !front();
      es = new EventSource(openedAway ? "/v1/me/live?away=1" : "/v1/me/live");
      window.clearInterval(watch);
      watch = window.setInterval(() => {
        if (Date.now() - heard > timing().stallMs) {
          close();
          if (!stopped) open();
        }
      }, timing().checkMs);
      es.addEventListener("ready", (ev) => {
        hear();
        connection = data<{ connection: string }>(ev).connection;
        setConnected(true);
        // A new connection: what was told before of who is online is old.
        setPresence(NO_PRESENCE);
        // The tab went to the back, or came to the front, since the address
        // was made (the browser's reconnection reuses it): said for this one.
        const now = readySays(openedAway, front());
        if (now !== null) say(now);
        emit({ type: "ready" });
      });
      es.addEventListener("ping", () => hear());
      es.addEventListener("message", (ev) => {
        hear();
        emit({ type: "message", data: data<LiveMessage>(ev) });
      });
      es.addEventListener("chat", (ev) => {
        hear();
        emit({ type: "chat", data: data<LiveChat>(ev) });
      });
      es.addEventListener("gone", (ev) => {
        hear();
        emit({ type: "gone", data: data<LiveGone>(ev) });
      });
      es.addEventListener("typing", (ev) => {
        hear();
        const t = data<{ typing: string[] }>(ev).typing ?? [];
        setTyping(t.length > 0 ? new Set(t) : EMPTY);
      });
      // Groups (stage 5b).
      es.addEventListener("group_message", (ev) => {
        hear();
        emit({ type: "group_message", data: data<LiveGroupMessage>(ev) });
      });
      es.addEventListener("group", (ev) => {
        hear();
        emit({ type: "group", data: data<LiveGroup>(ev) });
      });
      es.addEventListener("group_receipts", (ev) => {
        hear();
        emit({ type: "group_receipts", data: data<LiveGroupReceipts>(ev) });
      });
      es.addEventListener("group_invite", (ev) => {
        hear();
        emit({ type: "group_invite", data: data<LiveGroupInvite>(ev) });
      });
      es.addEventListener("basket", (ev) => {
        hear();
        emit({ type: "basket", data: data<LiveBasket>(ev) });
      });
      es.addEventListener("group_typing", (ev) => {
        hear();
        const t = typingByGroup(data<LiveGroupTyping>(ev));
        setGroupTyping(t.size > 0 ? t : NO_GROUP_TYPING);
      });
      es.addEventListener("presence", (ev) => {
        hear();
        setPresence((cur) => withPresence(cur, data<LivePresence>(ev)));
      });
      es.onerror = () => {
        setConnected(false);
        setTyping(EMPTY);
        setGroupTyping(NO_GROUP_TYPING);
        // The browser reconnects by itself (with Last-Event-ID), but not
        // after a refusal (too many open, 429): try again later, polling
        // meanwhile.
        if (es?.readyState === EventSource.CLOSED && !stopped) {
          close();
          retry = window.setTimeout(open, timing().retryMs);
        }
      };
    };
    const seen = () => say(front());
    const leave = () => say(false, true);
    document.addEventListener("visibilitychange", seen);
    window.addEventListener("pagehide", leave);
    open();
    return () => {
      stopped = true;
      document.removeEventListener("visibilitychange", seen);
      window.removeEventListener("pagehide", leave);
      close();
      window.clearTimeout(retry);
    };
  }, [on]);

  // Presence is applied only while connected; the open chat's head is read
  // again on `ready`.
  return <Ctx.Provider value={{ connected, typing, groupTyping, presence: shownPresence(connected, presence), subscribe }}>{children}</Ctx.Provider>;
}
