import { useCallback, useEffect, useRef, useState } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { ApiError, get, post } from "../api";
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

const TAP_CACHE = "taskiem-push-tap";
const TAP_KEY = "/__taskiem/push-tap";

/**
 * Whether a notification for [business] was just tapped (the worker wrote it
 * down): read once, then gone. A link from anywhere else is not one.
 */
export async function tappedFor(business: string): Promise<boolean> {
  try {
    if (typeof caches === "undefined") return false;
    const c = await caches.open(TAP_CACHE);
    const r = await c.match(TAP_KEY);
    if (!r) return false;
    await c.delete(TAP_KEY);
    const t = (await r.json()) as { business?: string; at?: number };
    return t.business === business && Date.now() - (t.at ?? 0) < 120_000;
  } catch {
    return false;
  }
}

/** What the page says about a business in its address. */
export interface PushOpen {
  /** Why the switch could not be done. */
  problem: string | null;
  /** A link (not a tap) for another business: asked before switching. */
  ask: { business: string; name: string; to: string } | null;
  accept: () => void;
  decline: () => void;
}

/**
 * Follows a tapped notification: switches business when the address names
 * another one and a notification for it was just tapped, asks first when it
 * came any other way, and goes where an open window's notification says.
 */
export function useOpenFromPush(): PushOpen {
  const { me } = useAuth();
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const [problem, setProblem] = useState<string | null>(null);
  const [ask, setAsk] = useState<PushOpen["ask"]>(null);
  const here = me?.tenant_id ?? null;
  // One switch at a time: a second run of the effect (StrictMode, a fresh
  // me) must not send another, which the first would have ended.
  const switching = useRef<string | null>(null);
  // Said once: gone when the person moves to another page.
  useEffect(() => {
    setProblem(null);
    setAsk(null);
  }, [pathname]);

  const doSwitch = useCallback(
    (business: string, to: string) => {
      if (switching.current) return;
      switching.current = business;
      post("/v1/me/switch", { tenant_id: business }).then(
        // A fresh start in the other business: nothing of this one stays on the page.
        () => window.location.replace(to),
        (e: unknown) => {
          switching.current = null;
          setProblem(
            e instanceof ApiError && e.status !== 404 && e.message
              ? e.message
              : OTHER_BUSINESS,
          );
          navigate(to, { replace: true });
        },
      );
    },
    [navigate],
  );

  useEffect(() => {
    if (!new URLSearchParams(search).has("business")) return;
    const { to, business } = splitBusiness(pathname, search);
    if (!business || business === here) {
      navigate(to, { replace: true });
      return;
    }
    if (switching.current) return;
    let live = true;
    void tappedFor(business).then(async (tapped) => {
      if (!live) return;
      if (tapped) return doSwitch(business, to);
      // Not a tap: ask, naming the business, or say it is not one of theirs.
      let name = "";
      try {
        const r = await get<{ businesses: { id: string; name: string }[] }>(
          "/v1/me/businesses",
        );
        name = r.businesses.find((b) => b.id === business)?.name ?? "";
      } catch {
        // Not named: asked all the same.
      }
      if (!live) return;
      navigate(to, { replace: true });
      if (name) setAsk({ business, name, to });
      else setProblem(OTHER_BUSINESS);
    });
    return () => {
      live = false;
    };
  }, [pathname, search, here, navigate, doSwitch]);

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

  return {
    problem,
    ask,
    accept: () => {
      if (ask) doSwitch(ask.business, ask.to);
      setAsk(null);
    },
    decline: () => setAsk(null),
  };
}
