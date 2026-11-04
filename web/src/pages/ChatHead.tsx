import { useEffect, useRef, useState, type ReactNode } from "react";
import { Icon } from "../icons";
import { KEYS_EVENT } from "../lib/chatKeys";

/**
 * The head of every conversation: Back (on a phone), the avatar, the name
 * (with "typing…" under it), and the menu, whose last entry lists the keys.
 * The menu closes on Esc (the focus going back to its button) and on a click
 * elsewhere. `items` are the conversation's own entries; they are given the
 * way to close the menu.
 */
export function ChatHead({
  onBack,
  avatar,
  title,
  menuLabel,
  items,
}: {
  onBack: () => void;
  avatar: ReactNode;
  title: ReactNode;
  menuLabel: string;
  items?: (close: () => void) => ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!open) return;
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      setOpen(false);
      button.current?.focus();
    };
    const away = (e: MouseEvent) =>
      !(e.target as Element).closest?.(".chat-menu-wrap") && setOpen(false);
    document.addEventListener("keydown", esc);
    document.addEventListener("mousedown", away);
    return () => {
      document.removeEventListener("keydown", esc);
      document.removeEventListener("mousedown", away);
    };
  }, [open]);
  const close = () => setOpen(false);
  return (
    <div className="chat-head">
      <button
        type="button"
        className="icon-button chat-back"
        onClick={onBack}
        aria-label="Back to the chats"
      >
        <Icon name="back" />
      </button>
      {avatar}
      <h2>{title}</h2>
      <div className="chat-menu-wrap">
        <button
          type="button"
          className="icon-button"
          ref={button}
          aria-label={menuLabel}
          aria-expanded={open}
          aria-haspopup="true"
          onClick={() => setOpen(!open)}
        >
          <Icon name="more" />
        </button>
        {open && (
          <div className="chat-menu" data-testid="chat-menu">
            {items?.(close)}
            <button
              type="button"
              onClick={() => {
                close();
                window.dispatchEvent(new Event(KEYS_EVENT));
              }}
            >
              Keyboard
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
