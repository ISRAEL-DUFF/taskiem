import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Icon } from "../icons";

/**
 * One conversation in the chat list, as WhatsApp's row: a round avatar, the
 * name and the time of the last message on the first line, its preview (with
 * a tick before one of ours) and the unread count on the second, a pin or a
 * mute mark. The open conversation's row is highlighted.
 */
export function ChatRow({
  to,
  letter,
  title,
  time,
  preview,
  lead,
  unread = 0,
  pinned = false,
  muted = false,
  selected = false,
  typing = false,
  accent = false,
  testid,
}: {
  to: string;
  letter: string;
  title: ReactNode;
  time?: string;
  preview: ReactNode;
  /** Before the preview: the ticks of our own last message. */
  lead?: ReactNode;
  unread?: number;
  pinned?: boolean;
  muted?: boolean;
  selected?: boolean;
  typing?: boolean;
  /** The avatar in the brand's tint (Taskiem's own conversation). */
  accent?: boolean;
  testid?: string;
}) {
  return (
    <li data-testid={testid}>
      <Link
        className={`chat-row${selected ? " selected" : ""}`}
        to={to}
        aria-current={selected ? "true" : undefined}
      >
        <span
          className={`chat-avatar${accent ? " accent" : ""}`}
          aria-hidden="true"
        >
          {letter.slice(0, 1).toUpperCase()}
        </span>
        <span className="chat-row-main">
          <span className="chat-row-top">
            <span className="title">{title}</span>
            {time && (
              <span className={`chat-row-time${unread > 0 ? " unread" : ""}`}>
                {time}
              </span>
            )}
          </span>
          <span className="chat-row-sub">
            <span className={`preview${typing ? " typing" : ""}`}>
              {lead}
              <span className="preview-text">{preview}</span>
            </span>
            <span className="chat-row-tags">
              {pinned && (
                <span className="tag" title="Pinned">
                  <Icon name="pin" />
                  <span className="sr-only">Pinned</span>
                </span>
              )}
              {muted && (
                <span className="tag" title="Muted">
                  <Icon name="mute" />
                  <span className="sr-only">Muted</span>
                </span>
              )}
              {unread > 0 && (
                <span className="unread-count">
                  <span aria-hidden="true">{unread}</span>
                  <span className="sr-only">
                    {unread} unread {unread === 1 ? "message" : "messages"}
                  </span>
                </span>
              )}
            </span>
          </span>
        </span>
      </Link>
    </li>
  );
}
