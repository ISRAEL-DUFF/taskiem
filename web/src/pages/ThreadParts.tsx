import { Icon } from "../icons";

/** The round button over a thread's corner (WhatsApp Web's): the number of
 * messages that came while the reader was further up, or, away from the
 * newest page, a way back to it. */
export function ThreadJump({
  detached = false,
  count,
  onClick,
}: {
  detached?: boolean;
  count: number;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="chat-jump"
      data-testid="chat-jump"
      onClick={onClick}
    >
      <Icon name="down" />
      <span className="sr-only">
        {detached
          ? "Jump to the latest messages"
          : count > 0
            ? `${count} new ${count === 1 ? "message" : "messages"}: scroll to the newest`
            : "Scroll to the newest messages"}
      </span>
      {count > 0 && (
        <span className="unread-count" aria-hidden="true">
          {count}
        </span>
      )}
    </button>
  );
}
