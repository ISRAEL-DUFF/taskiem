import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { post } from "../api";
import { chatAddress, type PendingChat } from "../lib/chatdoor";
import { useAuth } from "../auth";
import { proof } from "../passkeys";
import { ErrorBox, useAction } from "../ui";
import { ConfirmEmailFirst, ProofField } from "./Account";

const channelNames: Record<string, string> = {
  whatsapp: "WhatsApp number",
  telegram: "Telegram account",
  slack: "Slack account",
};

const appNames: Record<string, string> = {
  whatsapp: "WhatsApp",
  telegram: "Telegram",
  slack: "Slack",
};

/** Links a chat to the signed-in account from the link emailed after
 * "I already use Taskiem" (docs/spec/chat-way-in.md 10.1). Anyone can ask
 * for that email with someone else's address, so opening the link links
 * nothing: the page shows which chat it is and the person chooses, proving
 * it is them as for linking a number from Account. The link's secret is
 * read from the fragment, which no server sees, then dropped. */
export function LinkChat() {
  const { me } = useAuth();
  const [token] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "",
  );
  const [pending, setPending] = useState<PendingChat | null>(null);
  const [typed, setTyped] = useState("");
  const [linked, setLinked] = useState("");
  const [refused, setRefused] = useState(false);
  const look = useAction();
  const act = useAction();
  const tried = useRef(false);
  useEffect(() => {
    if (window.location.hash)
      window.history.replaceState(null, "", window.location.pathname);
    if (!token || tried.current) return;
    tried.current = true;
    // Only a look: nothing is linked until the person says so below.
    void look.run(
      async () =>
        setPending(
          await post<PendingChat>("/v1/me/chat-link/preview", { token }),
        ),
      false,
    );
  }, [token]);
  const app = pending ? (appNames[pending.channel] ?? "chat") : "chat";
  return (
    <div className="card" style={{ maxWidth: 560 }}>
      <h1>Link your chat</h1>
      {!token && (
        <p className="error">This page needs the link from your email.</p>
      )}
      {look.busy && <p>Checking the link…</p>}
      <ErrorBox error={look.error} />
      {pending && !linked && !refused && (
        <form
          data-testid="chat-link-confirm"
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const out = await post<{ channel: string }>("/v1/me/chat-link", {
                token,
                ...(await proof(me?.factors, typed, "chat.link")),
              });
              setTyped("");
              setLinked(out.channel);
            }, false);
          }}
        >
          <p>
            Someone asked to link this {app} to your Taskiem account
            {me?.user?.email ? ` (${me.user.email})` : ""}:
          </p>
          <p>
            <strong data-testid="chat-link-address">
              {app}: {chatAddress(pending)}
            </strong>
          </p>
          <div className="warning" role="note">
            Only continue if this is your own {app} and you asked for this
            yourself a moment ago. Whoever holds this chat will be able to act
            as you in Taskiem.
          </div>
          <ProofField to="link this chat" value={typed} onChange={setTyped} />
          <ErrorBox error={act.error} />
          <ConfirmEmailFirst error={act.error} />
          <div className="row-actions">
            <button className="primary" type="submit" disabled={act.busy}>
              Link this chat
            </button>
            <button
              type="button"
              disabled={act.busy}
              onClick={() =>
                void act.run(async () => {
                  await post("/v1/me/chat-link/refuse", { token });
                  setRefused(true);
                }, false)
              }
            >
              This is not mine
            </button>
          </div>
        </form>
      )}
      {linked && (
        <p role="status" data-testid="chat-linked">
          Your {channelNames[linked] ?? "chat"} is linked to this account.
          Taskiem has said so in the chat, and you can carry on there. For the
          first 24 hours that chat cannot send you a link to sign in or to
          choose a password; sign in here the way you usually do.
        </p>
      )}
      {refused && (
        <p role="status" data-testid="chat-link-refused">
          Nothing was linked, and the link no longer works. If you keep getting
          these emails and did not ask for them, you can ignore them: no chat
          is linked unless you say so here.
        </p>
      )}
      <p>
        <Link to="/account">Your linked chats</Link>
      </p>
    </div>
  );
}

/** Confirms an email added to an account (docs/spec/chat-way-in.md 10.4),
 * signed in or not: an account made in chat may have no other way in. */
export function ConfirmEmail() {
  const [q] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)),
  );
  const [email, setEmail] = useState("");
  const act = useAction();
  const tried = useRef(false);
  useEffect(() => {
    if (window.location.hash)
      window.history.replaceState(null, "", window.location.pathname);
    if (!q.get("token") || tried.current) return;
    tried.current = true;
    void act.run(
      async () =>
        setEmail(
          (
            await post<{ email: string }>("/v1/auth/email/confirm", {
              tenant: q.get("t") ?? "",
              token: q.get("token") ?? "",
            })
          ).email,
        ),
      false,
    );
  }, [q]);
  return (
    <div className="login card">
      <h1>Confirm your email</h1>
      {!q.get("token") && (
        <p className="error">This page needs the link from your email.</p>
      )}
      {act.busy && <p>Confirming…</p>}
      {email && (
        <p role="status" data-testid="email-confirmed">
          {email} is your account's email now. Taskiem sends receipts and
          invoices there.
        </p>
      )}
      <ErrorBox error={act.error} />
    </div>
  );
}
