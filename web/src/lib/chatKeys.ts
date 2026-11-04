// The keys a desktop reader expects of a chat: the shortcuts, and where they
// are allowed. Plain keys (no Ctrl, Alt or Cmd, so the browser's own are
// left alone) that do nothing while a field has the focus, so typing a "j"
// in a message is only ever a "j".

export type Shortcut = "search" | "new" | "next" | "previous" | "help";

export type KeyLike = {
  key: string;
  ctrlKey?: boolean;
  metaKey?: boolean;
  altKey?: boolean;
};

/** What a key press asks for, or nothing. `typing` is true while a field (or
 * a dialog) has the focus. */
export function shortcutOf(e: KeyLike, typing: boolean): Shortcut | undefined {
  if (typing || e.ctrlKey || e.metaKey || e.altKey) return undefined;
  switch (e.key) {
    case "/":
      return "search";
    case "n":
      return "new";
    case "j":
      return "next";
    case "k":
      return "previous";
    case "?":
      return "help";
  }
  return undefined;
}

/** True where a key is a character being typed or a choice being made: a
 * field, a select, an editable block, or anything inside a dialog. */
export function isTyping(el: Element | null): boolean {
  if (!el) return false;
  const tag = el.tagName;
  if (tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT") return true;
  if ((el as HTMLElement).isContentEditable) return true;
  return !!el.closest?.('[role="dialog"], [role="menu"]');
}

/** The id one step on from the current one (or the first, with none open),
 * stopping at either end as a list does. */
export function step(
  ids: string[],
  current: string | undefined,
  by: 1 | -1,
): string | undefined {
  if (ids.length === 0) return undefined;
  const at = current === undefined ? -1 : ids.indexOf(current);
  if (at < 0) return by === 1 ? ids[0] : ids[ids.length - 1];
  return ids[Math.min(ids.length - 1, Math.max(0, at + by))];
}

export const SHORTCUTS: { keys: string[]; does: string }[] = [
  { keys: ["/"], does: "Find a business or a message" },
  { keys: ["n"], does: "Ask a business to link" },
  { keys: ["j"], does: "Next chat" },
  { keys: ["k"], does: "Previous chat" },
  { keys: ["↑", "↓"], does: "Move through the chats (in the list), Enter opens one" },
  { keys: ["Esc"], does: "Close the panel, then the chat" },
  { keys: ["Enter"], does: "Send the message" },
  { keys: ["Shift", "Enter"], does: "A new line in the message" },
  { keys: ["?"], does: "Show these keys" },
];
