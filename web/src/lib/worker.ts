// Starts Taskiem's service worker (public/sw.js) for everyone, not only for
// people who turn on chat notifications: it is also what shows "You're
// offline" when a page of the app cannot be reached (docs/spec/offline.md).
// It keeps no cache, so starting it changes nothing a person sees online.

interface WorkerHost {
  serviceWorker?: { register: (url: string, options: { scope: string }) => Promise<unknown> };
}

/**
 * Registers the worker once the page has loaded, so it never competes with
 * the first screen. Not in development (Vite serves the app there, and a
 * worker would outlive a reload of the dev server). Any failure is ignored:
 * the app works without it, and turning notifications on registers it again.
 */
export function registerWorker(
  host: WorkerHost = navigator as unknown as WorkerHost,
  production: boolean = import.meta.env.PROD,
  onLoad: (f: () => void) => void = (f) =>
    document.readyState === "complete" ? f() : window.addEventListener("load", f, { once: true }),
): boolean {
  const sw = host.serviceWorker;
  if (!production || !sw) return false;
  onLoad(() => {
    sw.register("/sw.js", { scope: "/" }).catch(() => undefined);
  });
  return true;
}
