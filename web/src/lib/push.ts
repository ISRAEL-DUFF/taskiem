import { del, get, put } from "../api";

// Chat push on this device (docs/spec/chat-push.md §6): the service worker,
// the browser's own subscription, and telling the server about it. Every
// function that talks to the browser says no, quietly, where it cannot.

/** A device the server has: never its address or keys. */
export interface PushDevice {
  id: string;
  label: string;
  preview: boolean;
  created_at: string;
  last_sent_at: string | null;
  last_ok_at: string | null;
}

export interface PushView {
  devices: PushDevice[];
  max: number;
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

/** The id the server gave this browser, kept here so the list can say "This device". */
export function myDevice(): string | null {
  try {
    return localStorage.getItem(KEY);
  } catch {
    return null;
  }
}

function keepDevice(id: string | null) {
  try {
    if (id) localStorage.setItem(KEY, id);
    else localStorage.removeItem(KEY);
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

/** Turns notifications off for this browser: tells the server, then lets the browser forget it. */
export async function disablePush(): Promise<PushView | undefined> {
  const sub = await thisSubscription();
  keepDevice(null);
  if (!sub) return undefined;
  let view: PushView | undefined;
  try {
    view = await del<PushView>("/v1/me/push", { endpoint: sub.endpoint });
  } catch {
    // Signed out already, or offline: the server drops it when the push service says it is gone.
  }
  await sub.unsubscribe().catch(() => false);
  return view;
}
