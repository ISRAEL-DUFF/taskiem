import { useEffect, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { post } from "../api";
import { ErrorBox, useAction } from "../ui";

const channelNames: Record<string, string> = {
  whatsapp: "WhatsApp number",
  telegram: "Telegram account",
  slack: "Slack account",
};

/** Links a chat to the signed-in account from the link emailed after
 * "I already use Taskiem" (docs/spec/chat-way-in.md 10.1). The token is
 * read from the fragment, which no server sees, then dropped. */
export function LinkChat() {
  const [token] = useState(
    () => new URLSearchParams(window.location.hash.slice(1)).get("token") ?? "",
  );
  const [linked, setLinked] = useState("");
  const act = useAction();
  const tried = useRef(false);
  useEffect(() => {
    if (window.location.hash)
      window.history.replaceState(null, "", window.location.pathname);
    if (!token || tried.current) return;
    tried.current = true;
    void act.run(
      async () =>
        setLinked(
          (await post<{ channel: string }>("/v1/me/chat-link", { token }))
            .channel,
        ),
      false,
    );
  }, [token]);
  return (
    <div className="card" style={{ maxWidth: 560 }}>
      <h1>Link your chat</h1>
      {!token && (
        <p className="error">This page needs the link from your email.</p>
      )}
      {act.busy && <p>Linking…</p>}
      {linked && (
        <p role="status" data-testid="chat-linked">
          Your {channelNames[linked] ?? "chat"} is linked to this account.
          Taskiem has said so in the chat, and you can carry on there.
        </p>
      )}
      <ErrorBox error={act.error} />
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
