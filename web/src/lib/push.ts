import { del, get, put } from "../api";

// Chat push on this device (docs/spec/chat-push.md §6): the service worker,
// the browser's own subscription, and telling the server about it. Every
// function that talks to the browser says no, quietly, where it cannot.

/** A device the server has: never its address or keys. */
export interface PushDevice {
  id: string;
  label: string;
  preview: boolean;
  /** A browser (webpush) or the phone app (fcm). */
  kind?: "webpush" | "fcm";
  platform?: "android" | "ios" | null;
  /** The kinds of notification it takes. */
  kinds?: PushKind[];
  created_at: string;
  last_sent_at: string | null;
  last_ok_at: string | null;
}

/** The kinds of notification a device may take (docs/spec/mobile-app-m4.md N3). */
export type PushKind =
  | "business"
  | "customers"
  | "orders"
  | "approvals"
  | "alerts";

export const KIND_WORDS: Record<PushKind, { title: string; sub: string }> = {
  business: {
    title: "Businesses you work with",
    sub: "Messages from linked businesses",
  },
  customers: {
    title: "Customers",
    sub: "When a customer writes",
  },
  orders: {
    title: "Orders and money",
    sub: "New orders, bills, payments and reminders",
  },
  approvals: {
    title: "Approvals",
    sub: "When something waits for your yes or no",
  },
  alerts: {
    title: "Alerts",
    sub: "Everything else the bell tells you",
  },
};

export interface PushView {
  devices: PushDevice[];
  max: number;
  /** The kinds a device may take, in order. */
  kinds?: PushKind[];
  /** The device a PUT just added or updated (this browser). */
  added?: string;
}

/** What this browser can do about notifications when Taskiem is closed. */
export type PushSupport = "yes" | "no" | "install-first";

export interface PushEnv {
  userAgent: string;
  platform?: string;
  maxTouchPoints?: number;
  /** Added to the Home Screen (an installed app). */
  standalone: boolean;
  hasWorker: boolean;
  hasPush: boolean;
  hasNotification: boolean;
}

/** iPhone, iPad and iPod, including an iPad that says it is a Mac. */
export function isApple(ua: string, platform = "", touch = 0): boolean {
  return /iPhone|iPad|iPod/.test(ua) || (platform === "MacIntel" && touch > 1);
}

/**
 * Whether this browser can take a push. On an iPhone or iPad it works only
 * for an app added to the Home Screen: until then the person is told to add
 * it (and no switch is offered).
 */
export function pushSupport(e: PushEnv): PushSupport {
  if (isApple(e.userAgent, e.platform, e.maxTouchPoints) && !e.standalone)
    return "install-first";
  return e.hasWorker && e.hasPush && e.hasNotification ? "yes" : "no";
}

/** A device's name in plain words, from the browser's own: "Chrome on Android". */
export function deviceLabel(ua: string): string {
  const os = /iPhone/.test(ua)
    ? "iPhone"
    : /iPad/.test(ua)
      ? "iPad"
      : /Android/.test(ua)
        ? "Android"
        : /Windows/.test(ua)
          ? "Windows"
          : /Mac OS X|Macintosh/.test(ua)
            ? "Mac"
            : /CrOS/.test(ua)
              ? "Chromebook"
              : /Linux/.test(ua)
                ? "Linux"
                : "";
  const browser = /EdgA?\/|Edg\//.test(ua)
    ? "Edge"
    : /OPR\/|Opera/.test(ua)
      ? "Opera"
      : /FxiOS|Firefox\//.test(ua)
        ? "Firefox"
        : /CriOS|Chrome\//.test(ua)
          ? "Chrome"
          : /Safari\//.test(ua)
            ? "Safari"
            : "";
  if (browser && os) return `${browser} on ${os}`;
  return browser || os || "A browser";
}

/** The server's key as the bytes a browser subscribes with (base64url). */
export function keyBytes(b64: string): Uint8Array {
  const pad = "=".repeat((4 - (b64.length % 4)) % 4);
  const raw = atob((b64 + pad).replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(raw, (c) => c.charCodeAt(0));
}

const KEY = "taskiem-push-device";

/** The business the page is in: a browser is a different device in each. */
let scope = "";
export function setPushScope(tenant: string | null) {
  scope = tenant ?? "";
  // The id kept before it was per business: this business's, once.
  try {
    const old = localStorage.getItem(KEY);
    if (old && scope) {
      if (!localStorage.getItem(`${KEY}:${scope}`))
        localStorage.setItem(`${KEY}:${scope}`, old);
      localStorage.removeItem(KEY);
    }
  } catch {
    // Storage blocked: found again by the server (findMyDevice).
  }
}
const key = () => (scope ? `${KEY}:${scope}` : KEY);

/** The id the server gave this browser in this business, kept here so the list can say "This device". */
export function myDevice(): string | null {
  try {
    return localStorage.getItem(key());
  } catch {
    return null;
  }
}

function keepDevice(id: string | null) {
  try {
    if (id) localStorage.setItem(key(), id);
    else localStorage.removeItem(key());
  } catch {
    // Not kept: the list does not say which is this one.
  }
}

/** This browser's environment, for `pushSupport`. */
export function browserEnv(): PushEnv {
  const nav = navigator as Navigator & { standalone?: boolean };
  return {
    userAgent: nav.userAgent,
    platform: nav.platform,
    maxTouchPoints: nav.maxTouchPoints,
    standalone:
      nav.standalone === true ||
      (typeof matchMedia === "function" &&
        matchMedia("(display-mode: standalone)").matches),
    hasWorker: "serviceWorker" in nav,
    hasPush: typeof window !== "undefined" && "PushManager" in window,
    hasNotification: typeof Notification !== "undefined",
  };
}

/** The server's key, or null when it has none (the feature is off). */
export async function serverKey(): Promise<string | null> {
  try {
    return (await get<{ public_key: string }>("/v1/me/push/key")).public_key;
  } catch {
    return null;
  }
}

async function registration(): Promise<ServiceWorkerRegistration | undefined> {
  if (!("serviceWorker" in navigator)) return undefined;
  return navigator.serviceWorker.getRegistration("/");
}

/** This browser's subscription, if it has one (it does not start the worker). */
export async function thisSubscription(): Promise<PushSubscription | null> {
  try {
    const r = await registration();
    return (await r?.pushManager.getSubscription()) ?? null;
  } catch {
    return null;
  }
}

/** Why turning it on did not work. */
export type PushWhy = "denied" | "unsupported" | "off" | "failed";

/**
 * Turns notifications on for this browser: asks the browser's permission,
 * starts the worker, subscribes with the server's key and tells the server
 * (which keeps it for this person in this business).
 */
export async function enablePush(
  preview: boolean,
): Promise<{ ok: true; view: PushView } | { ok: false; why: PushWhy }> {
  if (pushSupport(browserEnv()) !== "yes")
    return { ok: false, why: "unsupported" };
  const key = await serverKey();
  if (!key) return { ok: false, why: "off" };
  try {
    if (Notification.permission === "default")
      await Notification.requestPermission();
    if (Notification.permission !== "granted")
      return { ok: false, why: "denied" };
    await navigator.serviceWorker.register("/sw.js", { scope: "/" });
    const reg = await navigator.serviceWorker.ready;
    const sub =
      (await reg.pushManager.getSubscription()) ??
      (await reg.pushManager.subscribe({
        userVisibleOnly: true,
        applicationServerKey: keyBytes(key) as BufferSource,
      }));
    const json = sub.toJSON();
    const view = await put<PushView>("/v1/me/push", {
      endpoint: json.endpoint,
      keys: json.keys,
      label: deviceLabel(navigator.userAgent),
      preview,
    });
    keepDevice(view.added ?? null);
    return { ok: true, view };
  } catch {
    return { ok: false, why: "failed" };
  }
}

/**
 * Which device of the list is this browser, when the page lost it (the
 * worker renewed the subscription, or storage was cleared): the server finds
 * it by its address and never adds one it does not have.
 */
export async function findMyDevice(): Promise<PushView | undefined> {
  const sub = await thisSubscription();
  if (!sub) return undefined;
  const json = sub.toJSON();
  try {
    const view = await put<PushView>("/v1/me/push", {
      endpoint: json.endpoint,
      keys: json.keys,
      known: true,
    });
    keepDevice(view.added ?? null);
    return view;
  } catch {
    return undefined;
  }
}

/**
 * Turns notifications off for this browser in this business: tells the
 * server. The browser's own subscription is kept (the person's other
 * businesses may use it) unless [signingOut]: then the browser forgets it,
 * and every business's record of it goes when the push service says so.
 */
export async function disablePush(
  signingOut = false,
): Promise<PushView | undefined> {
  const sub = await thisSubscription();
  keepDevice(null);
  if (!sub) return undefined;
  let view: PushView | undefined;
  try {
    view = await del<PushView>("/v1/me/push", { endpoint: sub.endpoint });
  } catch {
    // Signed out already, or offline: the server drops it when the push service says it is gone.
  }
  if (signingOut) await sub.unsubscribe().catch(() => false);
  return view;
}
