import { useState } from "react";
import { Link } from "react-router-dom";
import { useAuth } from "../auth";
import { passkeysSupported } from "../passkeys";

/** Administrator permissions: the people the banner speaks to. */
const ADMIN = ["member.manage", "role.manage", "secret.manage", "policy.manage", "git.manage", "connector.manage", "pii.reveal", "pii.erase"];
const KEY = "taskiem:passkey-nudge";
const QUIET_DAYS = 30;

function quietUntil(): number {
  try {
    return Number(localStorage.getItem(KEY) ?? 0);
  } catch {
    return 0;
  }
}

/** Suggests a passkey to an administrator who has none, on a device that
 * can make one (docs/spec/optional-passkeys.md §6). No one is made to add
 * one; "Not now" hides it on this device for 30 days. */
export function PasskeyNudge() {
  const { me } = useAuth();
  const [hidden, setHidden] = useState(() => Date.now() < quietUntil());
  const admin = ADMIN.some((p) => me?.permissions.includes(p));
  if (hidden || !admin || !me?.factors || me.factors.passkey || !passkeysSupported()) return null;
  return (
    <div className="notify-ask" role="region" aria-label="Passkey" data-testid="passkey-nudge">
      <span>Add a passkey: sign in with your fingerprint or face, and keep your account safe even if your password is found out.</span>
      <span className="row-actions">
        <Link className="button primary small" to="/account">
          Add a passkey
        </Link>
        <button
          type="button"
          className="small"
          onClick={() => {
            try {
              localStorage.setItem(KEY, String(Date.now() + QUIET_DAYS * 86_400_000));
            } catch {
              // Not kept in this browser: it shows again next time.
            }
            setHidden(true);
          }}
        >
          Not now
        </button>
      </span>
    </div>
  );
}
