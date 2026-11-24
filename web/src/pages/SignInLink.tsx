import { useEffect, useState } from "react";
import { Link } from "react-router-dom";
import { post } from "../api";
import { useAuth } from "../auth";
import { ErrorBox, useAction } from "../ui";

/** The page a "sign in" link from chat opens (docs/spec/chat-sign-in.md).
 * The token is read from the fragment, which is never sent to a server,
 * and cleared from the address bar. Only the button's POST uses it, so a
 * chat app or browser fetching the link ahead of time signs no one in. */
export function SignInLink() {
  const { refresh } = useAuth();
  const [token] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "",
  );
  const [good, setGood] = useState<boolean | null>(token ? null : false);
  const act = useAction();
  useEffect(() => {
    if (window.location.hash)
      window.history.replaceState(null, "", window.location.pathname);
    if (!token) return;
    post("/v1/auth/chat/link/peek", { token }).then(
      () => setGood(true),
      () => setGood(false),
    );
  }, [token]);
  return (
    <div className="login card">
      <h1>Sign in to Taskiem</h1>
      {good === false ? (
        <>
          <p className="error">
            This link has been used or has expired. Send *sign in* to Taskiem in
            your chat for a new one.
          </p>
          <p>
            <Link to="/login">Sign in another way</Link>
          </p>
        </>
      ) : (
        <>
          <p className="hint">
            You asked for this link in your chat with Taskiem. It works once.
          </p>
          <ErrorBox error={act.error} />
          <button
            className="primary"
            type="button"
            disabled={act.busy || good === null}
            style={{ width: "100%" }}
            onClick={() =>
              void act.run(async () => {
                await post("/v1/auth/chat/link", { token });
                await refresh();
                window.location.assign("/");
              }, false)
            }
          >
            {act.busy ? "Signing in…" : "Sign in"}
          </button>
        </>
      )}
    </div>
  );
}
