import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { post } from "../api";
import { useAuth } from "../auth";
import { useOnboarding } from "../onboarding";
import { ErrorBox, useAction } from "../ui";
import { WayIn } from "./WayIn";

/** /signup: the same way in as /login (taskiem-for-everyone §17). The
 * business form is in Start something now (StartSomething.tsx). */
export function Signup() {
  return <WayIn />;
}

/** Confirms the signed-in person's email with the link's token, read from
 * the fragment (never sent to a server) and then dropped from the address
 * bar. */
export function VerifyEmail() {
  const [token] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "",
  );
  const { refresh } = useAuth();
  const onboarding = useOnboarding();
  const [done, setDone] = useState(false);
  const act = useAction();
  const tried = useRef(false);
  useEffect(() => {
    if (window.location.hash)
      window.history.replaceState(null, "", window.location.pathname);
    if (!token || tried.current) return;
    tried.current = true;
    void act.run(async () => {
      await post("/v1/me/email/verify", { token });
      setDone(true);
      onboarding.reload();
      await refresh();
    }, false);
  }, [token]);
  return (
    <div className="card" style={{ maxWidth: 560 }}>
      <h1>Confirm your email</h1>
      {!token && (
        <p className="error">This page needs the link from your email.</p>
      )}
      {act.busy && <p>Confirming…</p>}
      {done && (
        <p role="status">
          Your email is confirmed. You can now invite people and make API keys.
        </p>
      )}
      <ErrorBox error={act.error} />
      <p>
        <Link to="/start">Back to Get started</Link>
      </p>
    </div>
  );
}

/** Terms the signed-in person has not answered yet (an account from
 * before them): asked once, at the top of every page, until they agree or
 * say not now. */
export function TermsBanner() {
  const { me } = useAuth();
  const [gone, setGone] = useState(false);
  const act = useAction();
  if (!me?.terms || gone) return null;
  const answer = (accept: boolean) =>
    void act.run(async () => {
      await post("/v1/me/terms", { accept });
      setGone(true);
    }, false);
  return (
    <div className="warning" role="status" data-testid="terms-banner">
      Taskiem has terms and a privacy notice you have not agreed to yet:{" "}
      <a href={me.terms.url} target="_blank" rel="noreferrer">
        read them
      </a>
      .{" "}
      <button
        className="primary"
        onClick={() => answer(true)}
        disabled={act.busy}
      >
        I agree
      </button>{" "}
      <button onClick={() => answer(false)} disabled={act.busy}>
        Not now
      </button>
      <ErrorBox error={act.error} />
    </div>
  );
}
