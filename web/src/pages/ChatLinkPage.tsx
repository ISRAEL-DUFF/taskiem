import { useEffect } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { ApiError, get } from "../api";
import { useAuth } from "../auth";
import { hoursLines, openWords, type ChatLinkCard } from "../lib/personchat";
import { ErrorBox, Skeleton, useLoad } from "../ui";
import { ChatAvatar } from "./ChatAvatar";
import { rememberAppURL } from "./PersonChats";
import { useSwitch } from "./Switch";

// A business's chat link, /m/{code} (docs/spec/taskiem-for-everyone.md
// §8.2, §9): its name, photo and hours, Chat here in your browser and Get
// the Taskiem app. Signed out, Chat here runs the phone way in with the
// link (the business pays for the code), then opens the chat; signed in,
// it opens the chat at once (a business's session moves to You first).

/** Where the chat opens once the person is signed in. */
export const startPath = (code: string) => `/you/chats/new?code=${encodeURIComponent(code)}`;

/** Signed out: the phone way in, with the link the person came through. */
export const wayInPath = (code: string) => `/login?chat=${encodeURIComponent(code)}&next=${encodeURIComponent(startPath(code))}`;

export function ChatLinkPage() {
  const { code = "" } = useParams();
  const { me } = useAuth();
  const nav = useNavigate();
  const sw = useSwitch(startPath(code));
  const card = useLoad(() => get<ChatLinkCard>(`/v1/chat-links/${encodeURIComponent(code)}`), [code]);
  useEffect(() => rememberAppURL(card.data?.app), [card.data?.app]);
  const err = card.error;
  const gone = err instanceof ApiError && err.status === 410;
  const unknown = err instanceof ApiError && err.status === 404;
  const c = card.data;
  const personal = !!me && (me.personal || me.tenant_id === null);
  const chatHere = () => {
    if (!me) return nav(wayInPath(code));
    if (personal) return nav(startPath(code));
    // A business's session: the chat is the person's own, on You.
    sw.go("you");
  };
  if (gone || unknown)
    return (
      <div className="login card way-in chat-link-page" data-testid="chat-link-gone">
        <h1>{gone ? "This link no longer works" : "This is not a chat link"}</h1>
        <p className="way-in-line">{gone ? "The business made a new link. Ask them for it." : "Check the link, or ask the business for it."}</p>
        <p className="hint way-in-other">
          <Link to={me ? "/" : "/login"}>{me ? "Go to Taskiem" : "Sign in to Taskiem"}</Link>
        </p>
      </div>
    );
  if (!c)
    return (
      <div className="login card way-in chat-link-page">
        <h1 className="sr-only">A business's chat link</h1>
        {err ? <ErrorBox error={err} /> : <Skeleton rows={3} label="Opening the link…" />}
      </div>
    );
  const open = openWords(c.open_now);
  const hours = hoursLines(c.hours);
  const photo = c.photo?.urls?.["320"] ?? c.photo?.urls?.["800"] ?? Object.values(c.photo?.urls ?? {})[0];
  return (
    <div className="login card way-in chat-link-page" data-testid="chat-link-page">
      <div className="chat-link-who">
        <ChatAvatar name={c.name} logo={photo} className="chat-link-avatar" />
        <div>
          <h1>{c.name}</h1>
          {c.tagline && <p className="way-in-line">{c.tagline}</p>}
          <p className="hint">{[c.area, open].filter(Boolean).join(" · ")}</p>
        </div>
      </div>
      {hours.length > 0 && (
        <details className="chat-link-hours">
          <summary>Opening hours</summary>
          <ul>
            {hours.map((h) => (
              <li key={h}>{h}</li>
            ))}
          </ul>
          {c.hours?.note && <p className="hint">{c.hours.note}</p>}
        </details>
      )}
      {c.takes_chats ? (
        <>
          <button type="button" className="primary" disabled={sw.busy} onClick={chatHere} data-testid="chat-here">
            Chat here in your browser
          </button>
          <ErrorBox error={sw.error} />
          {c.app && (
            <a className="button" href={c.app} target="_blank" rel="noopener noreferrer" data-testid="get-app">
              Get the Taskiem app
            </a>
          )}
        </>
      ) : (
        <p className="notice" role="status" data-testid="not-taking-chats">
          {c.name} is not taking chats in Taskiem right now.{c.shop ? " You can still visit their shop." : ""}
        </p>
      )}
      {c.shop && (
        <p className="hint way-in-other">
          <a href={c.shop} target="_blank" rel="noopener noreferrer">
            Visit their shop
          </a>
        </p>
      )}
    </div>
  );
}
