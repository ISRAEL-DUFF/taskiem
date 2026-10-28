// Warning before unsaved work is lost: closing or reloading the tab (the
// browser asks), and following a link to another page of the console (we
// ask). The console uses a plain router, which cannot block navigation, so
// links are caught as they are clicked.
import { useEffect } from "react";

/** Whether following href from the page at here leaves it: another page of
 * the same site, opened in this tab. A change of the query or hash alone
 * (a tab, a version) stays on the page; the page handles those itself.
 * Another site unloads the page, so the browser asks (beforeunload). */
export function leavesPage(href: string, here: string, target = ""): boolean {
  if (target && target !== "_self") return false;
  let to: URL, at: URL;
  try {
    to = new URL(href, here);
    at = new URL(here);
  } catch {
    return false;
  }
  return to.origin === at.origin && to.pathname !== at.pathname;
}

/** While when is true, asks before the page is left. */
export function useLeaveWarning(when: boolean, message = "Leave without saving? Your unsaved changes will be lost.") {
  useEffect(() => {
    if (!when) return;
    const unload = (e: BeforeUnloadEvent) => e.preventDefault();
    const click = (e: MouseEvent) => {
      if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
      const a = e.target instanceof Element ? e.target.closest("a[href]") : null;
      if (!(a instanceof HTMLAnchorElement) || a.hasAttribute("download") || !leavesPage(a.href, window.location.href, a.target)) return;
      if (!window.confirm(message)) {
        e.preventDefault();
        e.stopPropagation();
      }
    };
    window.addEventListener("beforeunload", unload);
    document.addEventListener("click", click, true);
    return () => {
      window.removeEventListener("beforeunload", unload);
      document.removeEventListener("click", click, true);
    };
  }, [when, message]);
}
