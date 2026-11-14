import { useEffect, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { ApiError, post } from "../api";
import { useAuth } from "../auth";

// A tapped notification (docs/spec/mobile-app-m4.md N3): the worker opens the
// page with ?business=<the business it is for>, or tells an open window where
// to go. A person in more than one business is moved to that one first, so the
// conversation or the order is there when the page opens.

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

/** The page to open and the business it is in, from the address a tap opened. */
export function splitBusiness(
  pathname: string,
  search: string,
): { to: string; business: string | null } {
  const q = new URLSearchParams(search);
  const b = q.get("business");
  q.delete("business");
  const rest = q.toString();
  return {
    to: pathname + (rest ? `?${rest}` : ""),
    business: b && UUID.test(b) ? b : null,
  };
}

/** Whether a link a worker posted is a page of this app (not another site). */
export function appLink(link: unknown): link is string {
  return (
    typeof link === "string" &&
    /^\/(?!\/)[A-Za-z0-9\-_/]{0,120}(\?[A-Za-z0-9\-_=&]{1,200})?$/.test(link)
  );
}

export const OTHER_BUSINESS =
  "This notification is for another of your businesses, and Taskiem could not open it here. Sign in to that business to see it.";

/**
 * Follows a tapped notification: switches business when the address names
 * another one, and goes where an open window's notification says. Returns
 * what to tell the person when the switch could not be done.
 */
export function useOpenFromPush(): string | null {
  const { me } = useAuth();
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const [problem, setProblem] = useState<string | null>(null);
  const here = me?.tenant_id ?? null;
  // Said once: gone when the person moves to another page.
  useEffect(() => setProblem(null), [pathname]);

  useEffect(() => {
    const { to, business } = splitBusiness(pathname, search);
    if (!new URLSearchParams(search).has("business")) return;
    if (!business || business === here) {
      navigate(to, { replace: true });
      return;
    }
    let live = true;
    post("/v1/me/switch", { tenant_id: business }).then(
      // A fresh start in the other business: nothing of this one stays on the page.
      () => live && window.location.replace(to),
      (e: unknown) => {
        if (!live) return;
        setProblem(
          e instanceof ApiError && e.status !== 404 && e.message
            ? e.message
            : OTHER_BUSINESS,
        );
        navigate(to, { replace: true });
      },
    );
    return () => {
      live = false;
    };
  }, [pathname, search, here, navigate]);

  // A tap while a window is open: the worker says where to go.
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    const go = (e: MessageEvent) => {
      const d = e.data as { type?: string; link?: unknown } | null;
      if (d?.type === "taskiem-go" && appLink(d.link)) navigate(d.link);
    };
    navigator.serviceWorker.addEventListener("message", go);
    return () => navigator.serviceWorker.removeEventListener("message", go);
  }, [navigate]);

  return problem;
}
