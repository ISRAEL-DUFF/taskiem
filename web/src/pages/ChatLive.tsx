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
import { get } from "../api";
import { useAuth } from "../auth";
import { brandFile } from "../brand";
import {
  decideNotify,
  notifyLine,
  onPrefs,
  playPing,
  readPrefs,
  writePrefs,
} from "../lib/chatNotify";
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

export type LiveEvent =
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
      60_000,
    );
    return () => clearInterval(t);
  }, [signedIn, load]);

  // A business with no linked business has nothing to hear.
  const worthOpening = (convs?.length ?? 0) > 0;
  useEffect(() => {
    if (!signedIn || !worthOpening || typeof EventSource === "undefined")
      return;
    let es: EventSource | undefined;
    let retry: number | undefined;
    let stopped = false;
    const emit = (e: LiveEvent) => subs.current.forEach((fn) => fn(e));
    const open = () => {
      es = new EventSource("/v1/chat/stream");
      es.addEventListener("ready", () => setConnected(true));
      es.addEventListener("message", (ev) => {
        const { message } = JSON.parse((ev as MessageEvent<string>).data) as {
          message: LinkMessage;
        };
        emit({ type: "message", message });
        incoming(message);
        reloadSoon();
      });
      es.addEventListener("gone", (ev) => {
        const d = JSON.parse((ev as MessageEvent<string>).data) as {
          id: string;
          link: string;
        };
        emit({ type: "gone", id: d.id, link: d.link });
        reloadSoon();
      });
      es.addEventListener("conversation", (ev) => {
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
      es.addEventListener("typing", (ev) => {
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
          retry = window.setTimeout(open, 30_000);
        }
      };
    };
    open();
    return () => {
      stopped = true;
      es?.close();
      window.clearTimeout(retry);
      setConnected(false);
      setTyping(EMPTY);
    };
    // incoming reads refs only; the stream is opened once while it is worth it.
  }, [signedIn, worthOpening, reloadSoon]);

  // A message came: a notification, a ping, or a question about them.
  const incoming = (m: LinkMessage) => {
    if (m.mine || handled.current.has(m.id)) return;
    handled.current.add(m.id);
    if (handled.current.size > 500)
      handled.current = new Set([...handled.current].slice(-200));
    const c = conv.current?.find((x) => x.link === m.link);
    const viewing =
      document.visibilityState === "visible" &&
      here.current.pathname === "/chat" &&
      new URLSearchParams(here.current.search).get("with") === m.link;
    const d = decideNotify({
      m,
      muted: !!c?.muted,
      viewing,
      hidden: document.visibilityState !== "visible",
      prefs: readPrefs(),
      permission:
        typeof Notification === "undefined"
          ? "unsupported"
          : Notification.permission,
      now: Date.now(),
    });
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
    if (d.ask) askLater.current = true;
  };

  // The question is asked when the person is looking at the tab again.
  useEffect(() => {
    const seen = () => {
      if (document.visibilityState === "visible" && askLater.current) {
        askLater.current = false;
        setAsking(true);
      }
    };
    document.addEventListener("visibilitychange", seen);
    return () => document.removeEventListener("visibilitychange", seen);
  }, []);
  const answer = useCallback((yes: boolean) => {
    setAsking(false);
    const prefs = readPrefs();
    if (!yes) return writePrefs({ ...prefs, asked: true });
    void Notification.requestPermission().then((p) =>
      writePrefs({ ...readPrefs(), browser: p === "granted", asked: true }),
    );
  }, []);
  // The prefs changing elsewhere (the settings page) closes the question.
  useEffect(() => onPrefs(() => readPrefs().asked && setAsking(false)), []);

  const unread = useMemo(
    () =>
      (convs ?? [])
        .filter((c) => !c.muted && !c.archived)
        .reduce((n, c) => n + c.unread, 0),
    [convs],
  );
  useTabBadge(unread);

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
    const link = document.querySelector<HTMLLinkElement>("#icon-svg") ?? document.querySelector<HTMLLinkElement>('link[rel~="icon"]');
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
