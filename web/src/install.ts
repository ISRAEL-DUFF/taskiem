// Installing the console on a phone's home screen (public/manifest.json):
// the browser offers it when it can (beforeinstallprompt, Android and
// desktop Chrome); the menu then shows "Install the app". On an iPhone
// it is Share, then Add to Home Screen (docs/guide/on-your-phone.md).
import { useEffect, useState } from "react";

type InstallPrompt = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: "accepted" | "dismissed" }> };

let waiting: InstallPrompt | null = null;
const watchers = new Set<() => void>();
const tell = () => watchers.forEach((f) => f());

if (typeof window !== "undefined") {
  window.addEventListener("beforeinstallprompt", (e) => {
    // Kept for the menu's button rather than the browser's own banner.
    e.preventDefault();
    waiting = e as InstallPrompt;
    tell();
  });
  window.addEventListener("appinstalled", () => {
    waiting = null;
    tell();
  });
}

/** Whether this page is running as the installed app (from the home screen). */
export function standalone(): boolean {
  if (typeof window === "undefined") return false;
  const ios = (navigator as Navigator & { standalone?: boolean }).standalone === true;
  return ios || (typeof window.matchMedia === "function" && window.matchMedia("(display-mode: standalone)").matches);
}

/** Installs the app, or null while the browser offers no install. */
export function useInstall(): (() => Promise<void>) | null {
  const [, redraw] = useState(0);
  useEffect(() => {
    const f = () => redraw((n) => n + 1);
    watchers.add(f);
    return () => {
      watchers.delete(f);
    };
  }, []);
  if (!waiting || standalone()) return null;
  const offer = waiting;
  return async () => {
    await offer.prompt();
    await offer.userChoice;
    waiting = null;
    tell();
  };
}
