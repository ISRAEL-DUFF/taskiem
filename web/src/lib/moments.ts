// The chat's moments (docs/spec/chat-tools.md §3): a sound for a new order
// or payment, which the owner can switch off, and a short burst for a good
// moment. Neither gets in the way; with motion turned down there is no
// burst.

import type { DocCard } from "./linkchat";

const KEY = "taskiem-chat-sound";

/** Whether new orders and payments make a sound here (on until turned off). */
export function soundOn(): boolean {
  try {
    return localStorage.getItem(KEY) !== "off";
  } catch {
    return false;
  }
}

export function setSound(on: boolean) {
  try {
    localStorage.setItem(KEY, on ? "on" : "off");
  } catch {
    // Storage blocked: no sound kept either way.
  }
}

/** A card that is news: an order or a payment, from the other side. */
export function isNews(card: DocCard, mine: boolean): boolean {
  return (
    !mine &&
    ["ordered", "auto_accepted", "bill", "declared"].includes(card.event)
  );
}

/** A good moment: a payment, or the first order of the day. */
export function isGoodMoment(
  card: DocCard,
  mine: boolean,
  firstOrderToday: boolean,
): boolean {
  if (mine) return false;
  return (
    card.event === "declared" ||
    ((card.event === "ordered" || card.event === "auto_accepted") &&
      firstOrderToday)
  );
}

let audio: AudioContext | undefined;

/** Browsers let a page make a sound only after a tap: the first one
 * opens the way, so the first new order is heard. */
export function primeSound() {
  if (typeof window === "undefined" || audio) return;
  const open = () => {
    try {
      const Ctx =
        window.AudioContext ??
        (window as unknown as { webkitAudioContext?: typeof AudioContext })
          .webkitAudioContext;
      if (Ctx && !audio) audio = new Ctx();
      void audio?.resume();
    } catch {
      // No sound: nothing else changes.
    }
  };
  window.addEventListener("pointerdown", open, { once: true, capture: true });
  window.addEventListener("keydown", open, { once: true, capture: true });
}

/** Two soft notes, rising. */
export function chime() {
  try {
    const Ctx =
      window.AudioContext ??
      (window as unknown as { webkitAudioContext?: typeof AudioContext })
        .webkitAudioContext;
    if (!Ctx) return;
    audio ??= new Ctx();
    if (audio.state === "suspended") void audio.resume();
    const now = audio.currentTime;
    for (const [i, f] of [660, 880].entries()) {
      const o = audio.createOscillator();
      const g = audio.createGain();
      o.type = "sine";
      o.frequency.value = f;
      g.gain.setValueAtTime(0, now + i * 0.12);
      g.gain.linearRampToValueAtTime(0.08, now + i * 0.12 + 0.02);
      g.gain.exponentialRampToValueAtTime(0.0001, now + i * 0.12 + 0.35);
      o.connect(g).connect(audio.destination);
      o.start(now + i * 0.12);
      o.stop(now + i * 0.12 + 0.4);
    }
  } catch {
    // No sound: nothing else changes.
  }
}
