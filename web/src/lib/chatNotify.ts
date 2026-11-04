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

/** Hears of a change of the prefs (from the settings page or the banner). */
export function onPrefs(fn: () => void): () => void {
  window.addEventListener(KEY, fn);
  return () => window.removeEventListener(KEY, fn);
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

/** A short two-note ping (no sound file: nothing to load). */
export function playPing(): void {
  try {
    const Ctx =
      window.AudioContext ??
      (window as unknown as { webkitAudioContext?: typeof AudioContext })
        .webkitAudioContext;
    if (!Ctx) return;
    const ctx = new Ctx();
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
    setTimeout(() => void ctx.close(), 600);
  } catch {
    // Blocked until the person has used the page: then it is silent.
  }
}
