import { Fragment, type ReactNode } from "react";
import { dayLabel } from "../lib/linkchat";
import type { useThread } from "../lib/useThread";
import { Skeleton } from "../ui";

/** The words that mark where unread messages begin. */
export const unreadWords = (n: number) =>
  n === 1 ? "1 unread message" : `${n} unread messages`;

/**
 * The scrolling history of every conversation (a business's and Taskiem's):
 * the log a screen reader follows, older messages loading near the top, the
 * days, "N unread messages" where the unread begin, and the round button
 * over the corner. How a message looks is given: `render` is called with the
 * message and whether it starts a run of one sender's.
 */
export function ChatStream<T extends { id: string; created_at: string }>({
  items,
  thread,
  hasOlder,
  prefix,
  keyOf,
  render,
  startsRun,
  unread,
  empty,
  overlay,
}: {
  /** Undefined while loading. */
  items: T[] | undefined;
  thread: ReturnType<typeof useThread<T>>;
  hasOlder: boolean;
  /** "chat" or "taskiem": the start of its test ids. */
  prefix: string;
  keyOf: (m: T) => string;
  render: (m: T, ctx: { first: boolean }) => ReactNode;
  /** Whether a message starts a run of one sender's. */
  startsRun: (prev: T | undefined, m: T) => boolean;
  unread?: { id: string; count: number };
  empty: ReactNode;
  /** The round button, or anything over the corner. */
  overlay?: ReactNode;
}) {
  const days: { day: string; messages: T[] }[] = [];
  for (const m of items ?? []) {
    const day = dayLabel(m.created_at);
    const g = days[days.length - 1];
    if (g && g.day === day) g.messages.push(m);
    else days.push({ day, messages: [m] });
  }
  return (
    <div className="chat-stream-wrap">
      <div
        className="chat-stream"
        aria-live="polite"
        data-testid={`${prefix}-stream`}
        role="log"
        tabIndex={0}
        aria-label="Chat history"
        ref={thread.stream}
        onScroll={thread.onScroll}
      >
        {hasOlder && items && items.length > 0 && (
          <div
            ref={thread.topMark}
            className="chat-top"
            data-testid={`${prefix}-top`}
            aria-hidden="true"
          />
        )}
        {!items ? (
          <Skeleton rows={3} label="Loading the conversation…" />
        ) : items.length === 0 ? (
          <p className="hint" style={{ alignSelf: "center" }}>
            {empty}
          </p>
        ) : (
          days.map((g) => (
            <div key={g.day} className="chat-group">
              <span className="chat-day">{g.day}</span>
              {g.messages.map((m, i) => (
                <Fragment key={keyOf(m)}>
                  {unread && unread.id === keyOf(m) && (
                    <div
                      className="chat-unread"
                      data-unread
                      data-testid="chat-unread"
                    >
                      {unreadWords(unread.count)}
                    </div>
                  )}
                  {render(m, { first: startsRun(g.messages[i - 1], m) })}
                </Fragment>
              ))}
            </div>
          ))
        )}
      </div>
      {overlay}
    </div>
  );
}
