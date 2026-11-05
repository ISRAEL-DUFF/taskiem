import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { addRecent } from "../lib/emoji";
import {
  EMOJI_GROUPS,
  nameOf,
  searchEmoji,
  takesTone,
  withTone,
} from "../lib/emojiList";

const KEY = "taskiem-emoji-recent";
const TONE_KEY = "taskiem-emoji-tone";
const TONES = [
  { n: 0, label: "Default skin tone", swatch: "🖐️" },
  { n: 1, label: "Light skin tone", swatch: "🖐🏻" },
  { n: 2, label: "Medium-light skin tone", swatch: "🖐🏼" },
  { n: 3, label: "Medium skin tone", swatch: "🖐🏽" },
  { n: 4, label: "Medium-dark skin tone", swatch: "🖐🏾" },
  { n: 5, label: "Dark skin tone", swatch: "🖐🏿" },
];

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
function readTone(): number {
  try {
    const n = Number(localStorage.getItem(TONE_KEY));
    return Number.isInteger(n) && n >= 0 && n <= 5 ? n : 0;
  } catch {
    return 0;
  }
}

/**
 * The emoji panel above the composer (WhatsApp Web's): a search box, the
 * recently used first, then groups, each emoji a button named in words, a
 * skin tone for the hands. One emoji is in the tab order and the arrow keys
 * move among them (Home and End too), as in a grid; Down from the search box
 * goes into the grid. Unicode's own characters: no images, no outside host.
 */
export function EmojiPanel({
  onPick,
  onClose,
}: {
  onPick: (char: string) => void;
  onClose: () => void;
}) {
  const [recent, setRecent] = useState(readRecent);
  const [tone, setTone] = useState(readTone);
  const [q, setQ] = useState("");
  const [group, setGroup] = useState(
    recent.length > 0 ? "recent" : (EMOJI_GROUPS[0]?.id ?? ""),
  );
  const [cursor, setCursor] = useState(0); // the emoji that is in the tab order
  const search = useRef<HTMLInputElement>(null);
  const grid = useRef<HTMLDivElement>(null);
  const tabsBox = useRef<HTMLDivElement>(null);
  useEffect(() => search.current?.focus(), []);

  const items = useMemo(() => {
    const nameFor = (c: string) => nameOf(c) ?? c;
    const tone1 = (c: string) => withTone(c, tone);
    if (q.trim())
      return searchEmoji(q).map((e) => ({ char: tone1(e.char), name: e.name }));
    if (group === "recent")
      return recent.map((c) => ({ char: c, name: nameFor(c) }));
    return (EMOJI_GROUPS.find((g) => g.id === group)?.items ?? []).map((e) => ({
      char: tone1(e.char),
      name: e.name,
    }));
  }, [q, group, recent, tone]);
  useEffect(() => setCursor(0), [q, group]);

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
  const chooseTone = (n: number) => {
    setTone(n);
    try {
      localStorage.setItem(TONE_KEY, String(n));
    } catch {
      // Not kept.
    }
  };
  const tabs = [
    ...(recent.length > 0
      ? [{ id: "recent", label: "Recently used", icon: "🕘" }]
      : []),
    ...EMOJI_GROUPS,
  ];
  const cells = () =>
    Array.from(
      grid.current?.querySelectorAll<HTMLButtonElement>(".emoji") ?? [],
    );
  const move = (to: number) => {
    const list = cells();
    if (list.length === 0) return;
    const i = Math.max(0, Math.min(to, list.length - 1));
    setCursor(i);
    list[i]?.focus();
  };
  const onGridKey = (e: KeyboardEvent) => {
    const list = cells();
    const here = list.findIndex((c) => c === document.activeElement);
    if (here < 0) return;
    const cols =
      getComputedStyle(grid.current as Element).gridTemplateColumns.split(" ")
        .length || 1;
    const to: Record<string, number> = {
      ArrowRight: here + 1,
      ArrowLeft: here - 1,
      ArrowDown: here + cols,
      ArrowUp: here - cols,
      Home: 0,
      End: list.length - 1,
    };
    if (e.key in to) {
      e.preventDefault();
      if (e.key === "ArrowUp" && here - cols < 0) search.current?.focus();
      else move(to[e.key] as number);
    }
  };
  const onTabKey = (e: KeyboardEvent) => {
    const list = Array.from(
      tabsBox.current?.querySelectorAll<HTMLButtonElement>(".emoji-tab") ?? [],
    );
    const here = list.findIndex((c) => c === document.activeElement);
    if (here < 0 || (e.key !== "ArrowRight" && e.key !== "ArrowLeft")) return;
    e.preventDefault();
    const next =
      list[
        (here + (e.key === "ArrowRight" ? 1 : list.length - 1)) % list.length
      ];
    next?.focus();
    next?.click();
  };
  const hasHands = items.some((i) => takesTone(i.char));
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
        onKeyDown={(e) => {
          if (e.key === "ArrowDown") {
            e.preventDefault();
            move(0);
          }
        }}
      />
      {!q.trim() && (
        <div
          className="emoji-tabs"
          role="group"
          aria-label="Kinds of emoji"
          ref={tabsBox}
          onKeyDown={onTabKey}
        >
          {tabs.map((g) => (
            <button
              key={g.id}
              type="button"
              className="emoji-tab"
              aria-pressed={group === g.id}
              tabIndex={group === g.id ? 0 : -1}
              title={g.label}
              onClick={() => setGroup(g.id)}
            >
              <span aria-hidden="true">{g.icon}</span>
              <span className="sr-only">{g.label}</span>
            </button>
          ))}
        </div>
      )}
      {hasHands && (
        <div className="emoji-tones" role="group" aria-label="Skin tone">
          {TONES.map((t) => (
            <button
              key={t.n}
              type="button"
              className="emoji-tone"
              aria-pressed={tone === t.n}
              aria-label={t.label}
              title={t.label}
              onClick={() => chooseTone(t.n)}
            >
              <span aria-hidden="true">{t.swatch}</span>
            </button>
          ))}
        </div>
      )}
      <div
        ref={grid}
        className="emoji-grid"
        role="group"
        aria-label={
          q.trim()
            ? "Found"
            : (tabs.find((g) => g.id === group)?.label ?? "Emoji")
        }
        onKeyDown={onGridKey}
      >
        {items.length === 0 ? (
          <p className="hint">No emoji for that.</p>
        ) : (
          items.map((e, i) => (
            <button
              key={e.char}
              type="button"
              className="emoji"
              aria-label={e.name}
              title={e.name}
              tabIndex={i === cursor ? 0 : -1}
              onClick={() => pick(e.char)}
              onFocus={() => setCursor(i)}
            >
              {e.char}
            </button>
          ))
        )}
      </div>
    </div>
  );
}
