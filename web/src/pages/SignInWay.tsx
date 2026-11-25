import { useEffect, type ReactNode } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { passkeysSupported } from "../passkeys";
import { useAction, ErrorBox } from "../ui";
import { AddEmail, Passkeys } from "./Account";
import { ChangePassword } from "./Password";

// A way to sign in on a new phone (docs/spec/taskiem-for-everyone.md §5.4;
// founder, 25 November 2026). A code to a phone proves only that someone
// holds the number today, so a person in a business proves a new device
// with a passkey, a link to their email or their password: never the PIN,
// which pays and approves only. Someone with none of them is let in
// anyway, so nobody is locked out of their own business, and is asked to
// add one: as a step before a business opens, for an account new to the
// apps (the server's user.sign_in_way_step), else a reminder on Today and
// Account (user.needs_sign_in_way) until they do.

/** Why, in plain words: the reminder's and the step's own line. */
export const SIGN_IN_WAY_WHY = "A code to your number isn't enough on its own if someone gets your SIM.";

/** The reminder on Today, Account and You, until a way is added. [to] is
 * where its button goes (Account's part; nothing on Account itself), or
 * [action] is the button. */
export function SignInWayCallout({ to, action }: { to?: string; action?: ReactNode }) {
  const { me } = useAuth();
  if (!me?.user?.needs_sign_in_way) return null;
  return (
    <div className="notice sign-in-way" role="note" data-testid="sign-in-way">
      <Icon name="lock" />
      <div className="grow">
        <strong>Add a way to sign in on a new phone.</strong>
        <div className="sub">{SIGN_IN_WAY_WHY} Add an email, a password or a passkey.</div>
      </div>
      {to && (
        <Link className="button small primary" to={to}>
          Add a way
        </Link>
      )}
      {action}
    </div>
  );
}

/** Account's part for adding a way: an email (confirmed by its link), a
 * password, or a passkey where this browser can make one. The existing
 * cards, under one heading. */
export function SignInWays() {
  const { me, refresh } = useAuth();
  const act = useAction();
  // The email's link opened in another tab: back here, look again.
  useEffect(() => {
    const back = () => {
      if (document.visibilityState === "visible") void refresh();
    };
    window.addEventListener("focus", back);
    document.addEventListener("visibilitychange", back);
    return () => {
      window.removeEventListener("focus", back);
      document.removeEventListener("visibilitychange", back);
    };
  }, [refresh]);
  return (
    <div id="sign-in-way" data-testid="sign-in-ways">
      <AddEmail />
      {me?.user?.email_placeholder && (
        <p className="hint">
          Opened the link?{" "}
          <button type="button" className="small" disabled={act.busy} onClick={() => void act.run(refresh, false)}>
            Check again
          </button>
        </p>
      )}
      <ErrorBox error={act.error} />
      <ChangePassword />
      {passkeysSupported() && <Passkeys onAdded={() => void refresh()} />}
    </div>
  );
}

/** The step a person new to the apps takes before a business opens: it
 * cannot be skipped; signing out is the only other way. */
export function SignInWayStep() {
  const { me, logout } = useAuth();
  return (
    <div className="login card sign-in-way-step" style={{ maxWidth: 640 }} data-testid="sign-in-way-step">
      <h1>Add a way to sign in on a new phone</h1>
      <p>
        {SIGN_IN_WAY_WHY} Before {me?.tenant_name || "your business"} opens, add one of these: an email, a password or a passkey.
      </p>
      <SignInWays />
      <p>
        <button type="button" onClick={() => void logout()}>
          Sign out
        </button>
      </p>
    </div>
  );
}
