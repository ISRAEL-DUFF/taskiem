// Signing out is only done when the server says so. The session is an
// HttpOnly cookie this page cannot remove: if the request never arrives (a
// dropped connection, a proxy that cannot reach Taskiem), the cookie still
// signs in whoever reloads the page next. So the app does not show the
// sign-in page until the server has answered, and keeps trying meanwhile.

/** What to tell someone whose sign-out has not reached Taskiem. */
export const NOT_SIGNED_OUT =
  "You are not signed out yet. Check your connection and try again";

/** Asks the server to end the session. True once it has: it answered that
 * the session is over (2xx), or that there was none (401). Anything else
 * (no answer, or an error on the way) leaves the session as it was. */
export async function endSession(
  send: typeof fetch = (input, init) => fetch(input, init),
): Promise<boolean> {
  try {
    const res = await send("/v1/auth/logout", {
      method: "POST",
      credentials: "same-origin",
      headers: { "X-Taskiem-Request": "1" },
    });
    return res.ok || res.status === 401;
  } catch {
    return false;
  }
}

interface Retrier {
  addEventListener(type: "online", fn: () => void): void;
  removeEventListener(type: "online", fn: () => void): void;
  setTimeout(fn: () => void, ms: number): unknown;
  clearTimeout(id: never): void;
}

/** Keeps trying to sign out until `attempt` succeeds: at once when the
 * connection returns, and every `everyMs` otherwise. Calls `done` once,
 * then stops. Returns a function that stops it early. */
export function keepSigningOut(
  attempt: () => Promise<boolean>,
  done: () => void,
  win: Retrier = window as unknown as Retrier,
  everyMs = 5000,
): () => void {
  let stopped = false;
  let trying = false;
  let timer: unknown;
  const stop = () => {
    stopped = true;
    win.removeEventListener("online", go);
    win.clearTimeout(timer as never);
  };
  function go() {
    if (stopped || trying) return;
    trying = true;
    win.clearTimeout(timer as never);
    void attempt()
      .catch(() => false)
      .then((ok) => {
        trying = false;
        if (stopped) return;
        if (ok) {
          stop();
          done();
        } else timer = win.setTimeout(go, everyMs);
      });
  }
  win.addEventListener("online", go);
  timer = win.setTimeout(go, everyMs);
  return stop;
}
