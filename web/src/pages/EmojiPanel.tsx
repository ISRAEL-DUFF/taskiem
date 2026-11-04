import { useEffect, useMemo, useRef, useState } from "react";
import { addRecent, EMOJI_GROUPS, searchEmoji } from "../lib/emoji";

const KEY = "taskiem-emoji-recent";

function readRecent(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(KEY) ?? "[]") as unknown;
    return Array.isArray(v)
      ? v.filter((x): x is string => typeof x === "string")
      : [];
  } catch {
    return [];
  }
}

/**
 * The emoji panel above the composer (WhatsApp Web's): a search box, the
 * recently used first, then groups, each emoji a button named in words.
 * Unicode's own characters: no images and no outside host.
 */
export function EmojiPanel({
  onPick,
  onClose,
}: {
  onPick: (char: string) => void;
  onClose: () => void;
}) {
  const [recent, setRecent] = useState(readRecent);
  const [q, setQ] = useState("");
  const [group, setGroup] = useState(
    recent.length > 0 ? "recent" : (EMOJI_GROUPS[0]?.id ?? ""),
  );
  const search = useRef<HTMLInputElement>(null);
  useEffect(() => search.current?.focus(), []);

  const items = useMemo(() => {
    if (q.trim())
      return searchEmoji(q).map((e) => ({ char: e.char, name: e.name }));
    if (group === "recent")
      return recent.map((c) => ({
        char: c,
        name:
          EMOJI_GROUPS.flatMap((g) => g.items).find((i) => i.char === c)
            ?.name ?? c,
      }));
    return (EMOJI_GROUPS.find((g) => g.id === group)?.items ?? []).map((e) => ({
      char: e.char,
      name: e.name,
    }));
  }, [q, group, recent]);

  const pick = (char: string) => {
    const next = addRecent(recent, char);
    setRecent(next);
    try {
      localStorage.setItem(KEY, JSON.stringify(next));
    } catch {
      // Not kept: the panel still works.
    }
    onPick(char);
  };
  const tabs = [
    ...(recent.length > 0
      ? [{ id: "recent", label: "Recently used", icon: "🕘" }]
      : []),
    ...EMOJI_GROUPS,
  ];
  return (
    <div
      className="emoji-panel"
      role="group"
      aria-label="Emoji"
      data-testid="emoji-panel"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.stopPropagation();
          onClose();
        }
      }}
    >
      <input
        ref={search}
        type="search"
        className="emoji-search"
        placeholder="Search emoji"
        aria-label="Search emoji"
        value={q}
        onChange={(e) => setQ(e.target.value)}
      />
      {!q.trim() && (
        <div className="emoji-tabs" role="group" aria-label="Kinds of emoji">
          {tabs.map((g) => (
            <button
              key={g.id}
              type="button"
              className="emoji-tab"
              aria-pressed={group === g.id}
              title={g.label}
              onClick={() => setGroup(g.id)}
            >
              <span aria-hidden="true">{g.icon}</span>
              <span className="sr-only">{g.label}</span>
            </button>
          ))}
        </div>
      )}
      <div
        className="emoji-grid"
        role="group"
        aria-label={
          q.trim()
            ? "Found"
            : (tabs.find((g) => g.id === group)?.label ?? "Emoji")
        }
      >
        {items.length === 0 ? (
          <p className="hint">No emoji for that.</p>
        ) : (
          items.map((e) => (
            <button
              key={e.char}
              type="button"
              className="emoji"
              aria-label={e.name}
              title={e.name}
              onClick={() => pick(e.char)}
            >
              {e.char}
            </button>
          ))
        )}
      </div>
    </div>
  );
}
