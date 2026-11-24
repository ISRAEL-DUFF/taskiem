// The unread count on the app's icon, where the device has one (an installed
// app on Chrome, Edge and Safari: the Badging API). The page sets it from the
// count it already shows in the tab's title; the service worker sets it from a
// push while no page is open (docs/spec/chat-push.md §6.5). Where there is no
// such API nothing happens.

interface Badging {
  setAppBadge?: (count?: number) => Promise<void>;
  clearAppBadge?: () => Promise<void>;
}

/** Shows `n` on the app's icon, or takes the count off when there is none. */
export function setAppBadge(
  n: number,
  nav: object = typeof navigator === "undefined" ? {} : navigator,
): void {
  const b = nav as Badging;
  try {
    const p = n > 0 ? b.setAppBadge?.(Math.floor(n)) : b.clearAppBadge?.();
    void p?.catch(() => undefined);
  } catch {
    // A device that refuses a badge shows none.
  }
}
