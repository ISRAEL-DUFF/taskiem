// Hearing about a new message when the tab is in the background (WhatsApp
// Web's): a desktop notification and a short sound, each a choice of this
// browser (off until chosen), never for a conversation that is muted or one
// that is open in front of the person. What a notification says is the
// business and the message's first line; a document card says only what it
// is (no references, amounts or names: they stay behind the app's
// permissions, as in every message Taskiem sends outside).

import type { LinkMessage } from "./linkchat";

export interface NotifyPrefs {
  /** Show a notification when a message comes and the tab is hidden. */
  browser: boolean;
  /** Play a short sound for a new message. */
  sound: boolean;
  /** The person has been asked, and said (yes or not now): do not ask again. */
  asked: boolean;
}

const KEY = "taskiem-chat-notify";
const NONE: NotifyPrefs = { browser: false, sound: false, asked: false };

export function readPrefs(): NotifyPrefs {
  try {
    const v = JSON.parse(
      localStorage.getItem(KEY) ?? "{}",
    ) as Partial<NotifyPrefs>;
    return {
      browser: v.browser === true,
      sound: v.sound === true,
      asked: v.asked === true,
    };
  } catch {
    return NONE;
  }
}

export function writePrefs(p: NotifyPrefs): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(p));
  } catch {
    // Not kept: it holds for this page only.
  }
  window.dispatchEvent(new Event(KEY));
}

/** Hears of a change of the prefs (from the settings page or the banner), in this tab or another. */
export function onPrefs(fn: () => void): () => void {
  const other = (e: StorageEvent) => e.key === KEY && fn();
  window.addEventListener(KEY, fn);
  window.addEventListener("storage", other);
  return () => {
    window.removeEventListener(KEY, fn);
    window.removeEventListener("storage", other);
  };
}

/**
 * Whether the person is not looking at this tab: it is in the background, or
 * its window is not the one they are using (another monitor, another window
 * in front). A tab that is visible but unfocused is away.
 */
export function isAway(): boolean {
  return document.visibilityState !== "visible" || !document.hasFocus();
}

const HANDLED = "taskiem-chat-handled";

/**
 * Takes a message to be told of (a notification, a ping), once across every
 * tab of this browser and every page load: the first to ask gets it. Tabs
 * share a record in localStorage, and the Web Locks API makes the asking one
 * at a time where the browser has it.
 */
export async function claimMessage(
  id: string,
  now = Date.now(),
): Promise<boolean> {
  const take = (): boolean => {
    try {
      const seen = JSON.parse(localStorage.getItem(HANDLED) ?? "{}") as Record<
        string,
        number
      >;
      if (seen[id] && now - seen[id] < 10 * 60_000) return false;
      seen[id] = now;
      const fresh = Object.entries(seen)
        .filter(([, at]) => now - at < 10 * 60_000)
        .sort((a, b) => b[1] - a[1])
        .slice(0, 200);
      localStorage.setItem(HANDLED, JSON.stringify(Object.fromEntries(fresh)));
      return true;
    } catch {
      return true; // no storage: this tab tells
    }
  };
  const locks = (navigator as Navigator & { locks?: LockManager }).locks;
  if (locks?.request)
    return locks.request("taskiem-chat-handled", () => take());
  return take();
}

const CARD_WORDS: Record<string, string> = {
  order: "An order",
  purchase: "A purchase order",
  invoice: "An invoice",
  bill: "A bill",
};

/** The notification's line: the first line of the words, or what it is. */
export function notifyLine(
  m: Pick<LinkMessage, "kind" | "body" | "card" | "attachment">,
): string {
  if (m.kind === "card") return CARD_WORDS[m.card?.doc ?? ""] ?? "A document";
  const first =
    (m.body ?? "")
      .split("\n")
      .map((l) => l.trim())
      .find(Boolean) ?? "";
  const clip = first.length > 100 ? `${first.slice(0, 99)}…` : first;
  if (m.attachment) {
    const what =
      m.attachment.kind === "photo"
        ? "Photo"
        : m.attachment.kind === "voice"
          ? "Voice note"
          : (m.attachment.name ?? "A file");
    return clip ? `${what}: ${clip}` : what;
  }
  return clip || "New message";
}

export interface NotifyInput {
  m: Pick<LinkMessage, "mine" | "read_at" | "created_at">;
  muted: boolean;
  /** This conversation is open and the tab is in front. */
  viewing: boolean;
  hidden: boolean;
  prefs: NotifyPrefs;
  permission: NotificationPermission | "unsupported";
  /** Now, in milliseconds. */
  now: number;
}

export interface NotifyDecision {
  notify: boolean;
  sound: boolean;
  /** Ask for permission when the person is next looking at the tab. */
  ask: boolean;
}

/**
 * What to do about a message the stream brought: nothing for our own, one
 * already read, an old one (the stream replays the last few seconds), a
 * muted conversation, or one the person is looking at.
 */
export function decideNotify(i: NotifyInput): NotifyDecision {
  const none = { notify: false, sound: false, ask: false };
  if (i.m.mine || i.m.read_at || i.muted || i.viewing) return none;
  if (i.now - new Date(i.m.created_at).getTime() > 2 * 60_000) return none;
  return {
    sound: i.prefs.sound,
    notify: i.prefs.browser && i.permission === "granted" && i.hidden,
    // Only a message that comes while the tab is hidden asks (never on load),
    // and only once, and only where asking can work.
    ask:
      i.hidden &&
      i.permission === "default" &&
      !i.prefs.asked &&
      !i.prefs.browser,
  };
}

let audio: AudioContext | undefined;
function context(): AudioContext | undefined {
  try {
    const Ctx =
      window.AudioContext ??
      (window as unknown as { webkitAudioContext?: typeof AudioContext })
        .webkitAudioContext;
    if (!Ctx) return undefined;
    audio ??= new Ctx();
    if (audio.state === "suspended") void audio.resume();
    return audio;
  } catch {
    return undefined;
  }
}

/**
 * Browsers keep sound off until the person has used the page: the first
 * click or key makes (and resumes) the audio context, so the ping that
 * comes later can be heard.
 */
export function unlockAudio(): () => void {
  const once = () => void context();
  const ev = ["pointerdown", "keydown"] as const;
  ev.forEach((e) => window.addEventListener(e, once, { once: true }));
  return () => ev.forEach((e) => window.removeEventListener(e, once));
}

/** A short two-note ping (no sound file: nothing to load). */
export function playPing(): void {
  try {
    const ctx = context();
    if (!ctx) return;
    const at = ctx.currentTime;
    for (const [i, hz] of [660, 880].entries()) {
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.frequency.value = hz;
      gain.gain.setValueAtTime(0.0001, at + i * 0.12);
      gain.gain.exponentialRampToValueAtTime(0.15, at + i * 0.12 + 0.02);
      gain.gain.exponentialRampToValueAtTime(0.0001, at + i * 0.12 + 0.11);
      osc.connect(gain).connect(ctx.destination);
      osc.start(at + i * 0.12);
      osc.stop(at + i * 0.12 + 0.12);
    }
  } catch {
    // Blocked: it is silent.
  }
}
