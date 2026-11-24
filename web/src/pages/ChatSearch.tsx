import { useEffect, useRef, useState } from "react";
import { get } from "../api";
import { Icon } from "../icons";
import { listTime, previewLine, type LinkMessage } from "../lib/linkchat";
import { around, highlight } from "../lib/snippet";
import { ErrorBox } from "../ui";
import { DayPicker } from "./DayPicker";

/**
 * Search in a conversation, as WhatsApp's: a panel beside the thread (the
 * whole screen on a phone) with its own head, a box (focused at once), a
 * calendar to go to a day, and the messages found as rows (who, when, and
 * the words around the hit, marked). The conversation stays behind it; a
 * row takes the thread to that message.
 */
export function ChatSearchPanel({
  name,
  path,
  onClose,
  onJump,
  onDate,
}: {
  name: string;
  /** The conversation's API path (/v1/link-chats/<link>). */
  path: string;
  onClose: () => void;
  /** Go to a message of the thread. */
  onJump: (id: string) => void;
  /** Go to the first message of a day (or after it); false when there is none. */
  onDate: (day: string) => Promise<boolean>;
}) {
  const [q, setQ] = useState("");
  const [found, setFound] = useState<LinkMessage[]>();
  const [error, setError] = useState<unknown>();
  const [dateOpen, setDateOpen] = useState(false);
  const [note, setNote] = useState("");
  const list = useRef<HTMLUListElement>(null);
  const dateButton = useRef<HTMLButtonElement>(null);
  const closeDate = () => {
    setDateOpen(false);
    requestAnimationFrame(() => dateButton.current?.focus());
  };
  const query = q.trim();
  // Searched a moment after the typing stops; an older answer never replaces a newer.
  useEffect(() => {
    setError(undefined);
    if (query.length < 2) {
      setFound(undefined);
      return;
    }
    let live = true;
    const t = setTimeout(() => {
      get<{ messages: LinkMessage[] }>(
        `${path}/messages?q=${encodeURIComponent(query)}`,
      ).then(
        (r) => live && setFound(r.messages),
        (e: unknown) => live && setError(e),
      );
    }, 250);
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [query, path]);
  // Esc closes the calendar if it is open, else the panel (the chat stays); a
  // click elsewhere closes the calendar.
  useEffect(() => {
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented) return;
      e.preventDefault();
      if (dateOpen) closeDate();
      else onClose();
    };
    const away = (e: MouseEvent) => {
      if (dateOpen && !(e.target as Element).closest?.(".chat-date-anchor"))
        setDateOpen(false);
    };
    document.addEventListener("keydown", esc);
    document.addEventListener("mousedown", away);
    return () => {
      document.removeEventListener("keydown", esc);
      document.removeEventListener("mousedown", away);
    };
  }, [onClose, dateOpen]);
  const rows = () =>
    Array.from(list.current?.querySelectorAll<HTMLElement>("button") ?? []);
  return (
    <aside
      className="chat-info chat-search"
      aria-label="Search messages"
      data-testid="chat-search-panel"
    >
      <div className="chat-info-head">
        <button
          type="button"
          className="icon-button"
          aria-label="Close search"
          onClick={onClose}
        >
          <Icon name="close" />
        </button>
        <h3>Search messages</h3>
      </div>
      <div className="chat-search-row" role="search">
        <div className="chat-date-anchor">
          <button
            type="button"
            className="icon-button"
            aria-label="Search by date"
            aria-expanded={dateOpen}
            data-testid="chat-date-open"
            ref={dateButton}
            onClick={() => {
              setDateOpen(!dateOpen);
              setNote("");
            }}
          >
            <Icon name="calendar" />
          </button>
          {dateOpen && (
            <DayPicker
              onClose={closeDate}
              onPick={(day) => {
                closeDate();
                onDate(day).then(
                  (ok) => !ok && setNote("No messages on or after that day."),
                  (err: unknown) => setError(err),
                );
              }}
            />
          )}
        </div>
        <label className="search">
          <span className="sr-only">Search messages in {name}</span>
          <Icon name="search" />
          <input
            type="search"
            placeholder="Search"
            value={q}
            autoFocus
            data-testid="chat-search"
            onChange={(e) => setQ(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "ArrowDown") {
                e.preventDefault();
                rows()[0]?.focus();
              }
            }}
          />
        </label>
      </div>
      {note && (
        <p className="hint chat-date-note" role="status">
          {note}
        </p>
      )}
      <ErrorBox error={error} />
      <div className="chat-search-body">
        {query.length < 2 ? (
          <p className="hint chat-search-empty">
            Search for messages within {name}.
          </p>
        ) : !found ? (
          <p className="hint chat-search-empty" role="status">
            Searching…
          </p>
        ) : found.length === 0 ? (
          <p className="hint chat-search-empty" role="status">
            No messages say that.
          </p>
        ) : (
          <ul
            className="search-results"
            ref={list}
            aria-label={`${found.length} ${found.length === 1 ? "message" : "messages"} found`}
            onKeyDown={(e) => {
              if (e.key !== "ArrowDown" && e.key !== "ArrowUp") return;
              const all = rows();
              const at = all.indexOf(document.activeElement as HTMLElement);
              if (at < 0) return;
              e.preventDefault();
              if (e.key === "ArrowDown") all[at + 1]?.focus();
              else if (at === 0)
                (
                  e.currentTarget
                    .closest(".chat-search")
                    ?.querySelector<HTMLElement>("input[type=search]") ?? null
                )?.focus();
              else all[at - 1]?.focus();
            }}
          >
            {found.map((m) => (
              <li key={m.id}>
                <button
                  type="button"
                  className="search-result"
                  data-testid="chat-result"
                  onClick={() => onJump(m.id)}
                >
                  <span className="sr-top">
                    <span className="sr-who">
                      {m.mine ? "You" : (m.sender_name ?? name)}
                    </span>
                    <span className="sr-time">{listTime(m.created_at)}</span>
                  </span>
                  <span className="sr-text">
                    {highlight(
                      around(m.body ?? previewLine(m, name), query),
                      query,
                    ).map((p, i) =>
                      p.hit ? <mark key={i}>{p.text}</mark> : p.text,
                    )}
                  </span>
                </button>
              </li>
            ))}
          </ul>
        )}
      </div>
    </aside>
  );
}
