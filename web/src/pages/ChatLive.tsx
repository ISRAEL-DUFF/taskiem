import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { enablePush } from "../lib/push";
import { setAppBadge } from "../lib/badge";
import { get } from "../api";
import { isAfter } from "../lib/stamp";
import { useAuth } from "../auth";
import { brandFile } from "../brand";
import {
  claimMessage,
  decideNotify,
  isAway,
  notifyLine,
  onPrefs,
  playPing,
  readPrefs,
  unlockAudio,
  writePrefs,
} from "../lib/chatNotify";
import type { TaskiemMessage } from "../lib/taskiemchat";
import {
  addTaskiemUnread,
  beginSeen,
  markSeen,
  seenAt,
  useTaskiemUnread,
} from "../lib/taskiemSeen";
import type { Conversation, LinkMessage } from "../lib/linkchat";
import { stripBadge, withBadge } from "../lib/tabBadge";

// The Chat page's live stream, for the whole signed-in app (business links,
// L3): one EventSource to GET /v1/chat/stream tells the page what changed in
// the conversations (a message, a read mark, a reaction, a deletion, a
// setting, someone typing) as it happens. The pages keep their polling,
// slower, as a fallback and to reconcile; where the stream cannot be had
// (blocked, too many tabs open) they poll as before. Here too: the unread
// count in the tab's title and icon, and the notification and the ping for a
// message that comes while the tab is in the background.

/**
 * How long the stream may be silent before the page stops trusting it (the
 * server says something at least every ten seconds), how often it looks,
 * how long it waits to try again after a refusal, and how often it looks for
 * a first linked business. A page of the tests may change them
 * (window.__taskiemChatLive) so as not to wait for minutes.
 */
function timing() {
  const t =
    (
      window as unknown as {
        __taskiemChatLive?: Partial<
          Record<"stallMs" | "checkMs" | "retryMs" | "listMs", number>
        >;
      }
    ).__taskiemChatLive ?? {};
  return {
    stallMs: t.stallMs ?? 30_000,
    checkMs: t.checkMs ?? 5_000,
    retryMs: t.retryMs ?? 30_000,
    listMs: t.listMs ?? 60_000,
  };
}

export type LiveEvent =
  | { type: "taskiem"; messages: TaskiemMessage[] }
  | { type: "message"; message: LinkMessage }
  | { type: "gone"; id: string; link: string }
  | {
      type: "conversation";
      link: string;
      muted: boolean;
      archived: boolean;
      pinned: boolean;
    };

interface ChatLive {
  /** The stream is open: the pages may poll less. */
  connected: boolean;
  /** The conversations whose other side is typing now. */
  typing: ReadonlySet<string>;
  /** Unread messages, in conversations not muted or archived. */
  unread: number;
  /** Hears each event; returns how to stop. */
  subscribe: (fn: (e: LiveEvent) => void) => () => void;
  /** The person is to be asked about notifications (a message came while the tab was hidden). */
  asking: boolean;
  answer: (yes: boolean) => void;
}

const NONE: ChatLive = {
  connected: false,
  typing: new Set(),
  unread: 0,
  subscribe: () => () => undefined,
  asking: false,
  answer: () => undefined,
};
const Ctx = createContext<ChatLive>(NONE);
export const useChatLive = () => useContext(Ctx);

const EMPTY = new Set<string>();

export function ChatLiveProvider({ children }: { children: ReactNode }) {
  const { me } = useAuth();
  const navigate = useNavigate();
  const loc = useLocation();
  const [convs, setConvs] = useState<Conversation[]>();
  const [connected, setConnected] = useState(false);
  const [typing, setTyping] = useState<ReadonlySet<string>>(EMPTY);
  const [asking, setAsking] = useState(false);
  const subs = useRef(new Set<(e: LiveEvent) => void>());
  const here = useRef(loc);
  here.current = loc;
  const conv = useRef(convs);
  conv.current = convs;
  const askLater = useRef(false);
  const handled = useRef(new Set<string>());
  const signedIn = !!me?.tenant_id;

  // The conversations: for the unread count and what is muted; again after
  // each event, and now and then.
  const load = useCallback(() => {
    get<{ conversations: Conversation[] }>("/v1/link-chats").then(
      (r) => setConvs(r.conversations),
      () => undefined,
    );
  }, []);
  const loadSoon = useRef<number>(undefined);
  const reloadSoon = useCallback(() => {
    window.clearTimeout(loadSoon.current);
    loadSoon.current = window.setTimeout(load, 400);
  }, [load]);
  useEffect(() => {
    if (!signedIn) return;
    load();
    const t = setInterval(
      () => document.visibilityState === "visible" && load(),
      timing().listMs,
    );
    return () => clearInterval(t);
  }, [signedIn, load]);

  // A first linked business (accepted, or found on the Chat page): look again
  // when the person comes to where they are, not only every minute.
  const where = loc.pathname;
  useEffect(() => {
    if (signedIn && (where === "/chat" || where === "/settings/links")) load();
  }, [signedIn, where, load]);

  // A business with no linked business has nothing to hear.
  // (Except on the Chat page itself: the person's own conversation with
  // Taskiem is there, on every channel, and follows the stream too.)
  const worthOpening = (convs?.length ?? 0) > 0 || where === "/chat";
  useEffect(() => {
    if (!signedIn || !worthOpening || typeof EventSource === "undefined")
      return;
    let es: EventSource | undefined;
    let retry: number | undefined;
    let watch: number | undefined;
    let heard = Date.now();
    let stopped = false;
    const emit = (e: LiveEvent) => subs.current.forEach((fn) => fn(e));
    const hear = () => {
      heard = Date.now();
    };
    const close = () => {
      window.clearInterval(watch);
      es?.close();
      es = undefined;
      setConnected(false);
      setTyping(EMPTY);
    };
    // What Taskiem says that the person is not looking at is unread (a
    // message told again, by the stream's look-back, is the same one).
    const taskiemHeard = (messages: TaskiemMessage[]) => {
      const seen = seenAt();
      if (!seen) return;
      const viewing =
        !isAway() &&
        here.current.pathname === "/chat" &&
        new URLSearchParams(here.current.search).get("with") === "taskiem";
      const said = messages.filter(
        (m) => !m.inbound && isAfter(m.created_at, seen),
      );
      const newest = said[said.length - 1];
      if (!newest) return;
      if (viewing) return markSeen(newest.created_at);
      addTaskiemUnread(said);
    };
    const open = () => {
      hear();
      es = new EventSource("/v1/chat/stream");
      // A stream that says nothing (a proxy holding it, a network that has
      // gone) is not a stream: after a silence the page closes it and opens
      // another, polling as before in the meantime.
      window.clearInterval(watch);
      watch = window.setInterval(() => {
        if (Date.now() - heard > timing().stallMs) {
          close();
          if (!stopped) open();
        }
      }, timing().checkMs);
      es.addEventListener("ready", () => (hear(), setConnected(true)));
      es.addEventListener("ping", hear);
      es.addEventListener("message", (ev) => {
        hear();
        const { message } = JSON.parse((ev as MessageEvent<string>).data) as {
          message: LinkMessage;
        };
        emit({ type: "message", message });
        void incoming(message);
        reloadSoon();
      });
      es.addEventListener("gone", (ev) => {
        hear();
        const d = JSON.parse((ev as MessageEvent<string>).data) as {
          id: string;
          link: string;
        };
        emit({ type: "gone", id: d.id, link: d.link });
        reloadSoon();
      });
      es.addEventListener("conversation", (ev) => {
        hear();
        emit({
          type: "conversation",
          ...(JSON.parse((ev as MessageEvent<string>).data) as {
            link: string;
            muted: boolean;
            archived: boolean;
            pinned: boolean;
          }),
        });
        reloadSoon();
      });
      es.addEventListener("taskiem", (ev) => {
        hear();
        const messages = (
          JSON.parse((ev as MessageEvent<string>).data) as {
            messages: TaskiemMessage[];
          }
        ).messages;
        emit({ type: "taskiem", messages });
        taskiemHeard(messages);
      });
      es.addEventListener("typing", (ev) => {
        hear();
        const d = JSON.parse((ev as MessageEvent<string>).data) as {
          typing: string[];
        };
        setTyping(d.typing.length > 0 ? new Set(d.typing) : EMPTY);
      });
      es.onerror = () => {
        setConnected(false);
        setTyping(EMPTY);
        // The browser reconnects by itself, but not after a refusal (too
        // many streams open, a proxy that will not stream): try again later.
        if (es?.readyState === EventSource.CLOSED && !stopped) {
          es.close();
          window.clearInterval(watch);
          retry = window.setTimeout(open, timing().retryMs);
        }
      };
    };
    open();
    return () => {
      stopped = true;
      close();
      window.clearTimeout(retry);
    };
    // incoming reads refs only; the stream is opened once while it is worth it.
  }, [signedIn, worthOpening, reloadSoon]);

  // A message came: a notification, a ping, or a question about them.
  const incoming = async (m: LinkMessage) => {
    if (m.mine || handled.current.has(m.id)) return;
    handled.current.add(m.id);
    if (handled.current.size > 500)
      handled.current = new Set([...handled.current].slice(-200));
    let c = conv.current?.find((x) => x.link === m.link);
    if (!c) {
      // A conversation the list has not seen yet: its name, and whether it is muted.
      const r = await get<{ conversations: Conversation[] }>(
        "/v1/link-chats",
      ).catch(() => undefined);
      if (r) {
        setConvs(r.conversations);
        c = r.conversations.find((x) => x.link === m.link);
      }
    }
    const away = isAway();
    const viewing =
      !away &&
      here.current.pathname === "/chat" &&
      new URLSearchParams(here.current.search).get("with") === m.link;
    const d = decideNotify({
      m,
      muted: !!c?.muted,
      viewing,
      hidden: away,
      prefs: readPrefs(),
      permission:
        typeof Notification === "undefined"
          ? "unsupported"
          : Notification.permission,
      now: Date.now(),
    });
    if (d.ask) askLater.current = true;
    if (!d.sound && !d.notify) return;
    // One tab of this browser tells, once (not each tab, not again after a reload).
    if (!(await claimMessage(m.id))) return;
    if (d.sound) playPing();
    if (d.notify) {
      const n = new Notification(c?.name ?? "New message", {
        body: notifyLine(m),
        tag: m.link,
        icon: brandFile("mark.svg"),
      });
      n.onclick = () => {
        window.focus();
        navigate(`/chat?with=${m.link}`);
        n.close();
      };
    }
  };

  // The question is asked when the person is looking at the tab again (in
  // front, and the window they are using), once.
  useEffect(() => {
    const seen = () => {
      if (!isAway() && askLater.current) {
        askLater.current = false;
        if (!readPrefs().asked) setAsking(true);
      }
    };
    document.addEventListener("visibilitychange", seen);
    window.addEventListener("focus", seen);
    return () => {
      document.removeEventListener("visibilitychange", seen);
      window.removeEventListener("focus", seen);
    };
  }, []);
  useEffect(() => unlockAudio(), []);
  const answer = useCallback((yes: boolean) => {
    setAsking(false);
    const prefs = readPrefs();
    if (!yes) return writePrefs({ ...prefs, asked: true });
    void Notification.requestPermission().then((p) => {
      writePrefs({ ...readPrefs(), browser: p === "granted", asked: true });
      // Where the server has the keys, the same yes also turns on a
      // notification for when Taskiem is closed (it says nothing if it cannot).
      if (p === "granted") void enablePush(true);
    });
  }, []);
  // The prefs changing elsewhere (the settings page) closes the question.
  useEffect(() => onPrefs(() => readPrefs().asked && setAsking(false)), []);

  // What Taskiem said while the person was away (from the phone, say).
  useEffect(() => {
    if (!signedIn) return;
    beginSeen();
    const seen = seenAt();
    if (!seen) return;
    let live = true;
    get<{ messages: TaskiemMessage[] }>(
      `/v1/taskiem-chat?after=${encodeURIComponent(seen)}`,
    ).then(
      (r) => {
        if (!live || seenAt() !== seen) return;
        const said = r.messages.filter((m) => !m.inbound);
        const viewing =
          here.current.pathname === "/chat" &&
          new URLSearchParams(here.current.search).get("with") === "taskiem";
        // Added to what the stream may already have told (it is the same set).
        if (!viewing) addTaskiemUnread(said);
      },
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [signedIn]);
  const taskiemUnread = useTaskiemUnread();
  const unread = useMemo(
    () =>
      (convs ?? [])
        .filter((c) => !c.muted && !c.archived)
        .reduce((n, c) => n + c.unread, 0),
    [convs],
  );
  useTabBadge(unread + taskiemUnread);

  const value = useMemo<ChatLive>(
    () => ({
      connected,
      typing,
      unread,
      subscribe: (fn) => {
        subs.current.add(fn);
        return () => subs.current.delete(fn);
      },
      asking,
      answer,
    }),
    [connected, typing, unread, asking, answer],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

/** "(3) Taskiem" in the tab's title, and a dot on its icon, while there is something unread. */
function useTabBadge(n: number) {
  // The same count on the app's icon, where the device has one.
  useEffect(() => setAppBadge(n), [n]);
  useEffect(() => {
    const apply = () => {
      const next = withBadge(document.title, n);
      if (next !== document.title) document.title = next;
    };
    apply();
    // A page that changes the title takes the count off: put it back.
    const el = document.querySelector("title");
    const mo = el ? new MutationObserver(apply) : undefined;
    if (el)
      mo?.observe(el, { childList: true, characterData: true, subtree: true });
    return () => {
      mo?.disconnect();
      document.title = stripBadge(document.title);
    };
  }, [n]);
  useEffect(() => {
    // The icon the browser prefers is the SVG one; while there is a count it
    // is a PNG with a red dot, drawn from the PNG icon, and then put back.
    const link =
      document.querySelector<HTMLLinkElement>("#icon-svg") ??
      document.querySelector<HTMLLinkElement>('link[rel~="icon"]');
    const base = document.querySelector<HTMLLinkElement>("#icon-png");
    if (!link) return;
    link.dataset.href ??= link.href;
    link.dataset.type ??= link.type;
    const restore = () => {
      link.href = link.dataset.href ?? link.href;
      link.type = link.dataset.type ?? link.type;
    };
    if (n <= 0) {
      restore();
      return;
    }
    let live = true;
    const img = new Image();
    img.onload = () => {
      if (!live) return;
      const c = document.createElement("canvas");
      c.width = c.height = 64;
      const g = c.getContext("2d");
      if (!g) return;
      g.drawImage(img, 0, 0, 64, 64);
      g.fillStyle = "#d92d20";
      g.beginPath();
      g.arc(46, 18, 16, 0, Math.PI * 2);
      g.fill();
      link.type = "image/png";
      link.href = c.toDataURL("image/png");
    };
    img.src = base?.dataset.href ?? base?.href ?? link.dataset.href ?? "";
    return () => {
      live = false;
    };
  }, [n]);
}

/** The question about notifications, asked once, when the person has come back to the tab. */
export function NotifyBanner() {
  const { asking, answer } = useChatLive();
  if (!asking) return null;
  return (
    <div
      className="notify-ask"
      role="region"
      aria-label="Notifications"
      data-testid="notify-ask"
    >
      <span>
        A business wrote to you while this tab was in the background. Hear about
        new messages when it is?
      </span>
      <span className="row-actions">
        <button
          type="button"
          className="primary small"
          onClick={() => answer(true)}
        >
          Turn on notifications
        </button>
        <button type="button" className="small" onClick={() => answer(false)}>
          Not now
        </button>
      </span>
    </div>
  );
}
