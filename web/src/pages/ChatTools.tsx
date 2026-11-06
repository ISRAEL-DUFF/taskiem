import { useEffect, useId, useRef, useState } from "react";
import { Link } from "react-router-dom";
import { get } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { initialsOf } from "./ChatAvatar";
import { useSheetDrag } from "../lib/sheetDrag";
import {
  filterRows,
  toolsFor,
  type Tool,
  type ToolKey,
  type ToolRow,
} from "../lib/tools";
import { useFocusTrap } from "../ui";

// Chat tools (docs/spec/chat-tools.md §3): the tools panel over the
// composer (the grid button, or "/"), and the sheet a tool opens in: up
// from the bottom on a phone, a drawer on a tablet, a panel beside the
// conversation on a computer. The chat stays where it was underneath.

const RECENT = "taskiem-tools-recent";

function recentTools(): ToolKey[] {
  try {
    return JSON.parse(localStorage.getItem(RECENT) ?? "[]") as ToolKey[];
  } catch {
    return [];
  }
}

function remember(k: ToolKey) {
  try {
    localStorage.setItem(
      RECENT,
      JSON.stringify([k, ...recentTools().filter((x) => x !== k)].slice(0, 2)),
    );
  } catch {
    // Storage blocked: no recent tools.
  }
}

/** Each tool's rows, loaded once while the panel or sheet is open. */
function useToolRows(tools: Tool[]) {
  const [rows, setRows] = useState<Partial<Record<ToolKey, ToolRow[]>>>({});
  const keys = tools.map((t) => t.key).join(",");
  useEffect(() => {
    let live = true;
    const urls = new Map<string, Promise<unknown>>();
    for (const t of tools) {
      // A tool that cannot load (nothing set up yet) lists nothing.
      if (!urls.has(t.url))
        urls.set(
          t.url,
          get(t.url).catch(() => undefined),
        );
      void urls.get(t.url)?.then((data) => {
        if (live)
          setRows((r) => ({
            ...r,
            [t.key]: data === undefined ? [] : t.rows(data),
          }));
      });
    }
    return () => {
      live = false;
    };
  }, [keys]);
  return rows;
}

/** The panel of tiles over the composer. */
export function ToolsPanel({
  query,
  onOpen,
  onClose,
}: {
  /** What follows "/" in the box, when it was typed. */
  query: string;
  onOpen: (k: ToolKey) => void;
  onClose: () => void;
}) {
  const { can } = useAuth();
  const tools = toolsFor(can, query);
  const rows = useToolRows(tools);
  const recent = recentTools()
    .map((k) => tools.find((t) => t.key === k))
    .filter((t): t is Tool => !!t);
  return (
    <section
      className="tk-tools"
      aria-label="Tools"
      data-testid="tools-panel"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onClose();
        }
      }}
    >
      <div className="tk-tools-head">
        <h2>Tools</h2>
        <span>Opens over the chat</span>
      </div>
      {tools.length === 0 ? (
        <p className="tk-tools-none">No tool called “{query}”.</p>
      ) : (
        <div className="tk-tools-grid tk-stagger">
          {tools.map((t) => {
            const r = rows[t.key];
            const badge = r && t.badge?.(r);
            return (
              <button
                key={t.key}
                type="button"
                className="tk-tile"
                onClick={() => onOpen(t.key)}
              >
                {badge && (
                  <span className={`tk-pill ${badge.tone} tk-tile-badge`}>
                    {badge.text}
                  </span>
                )}
                <span className="tk-tile-icon">
                  <Icon name={t.icon} />
                </span>
                <span>
                  <span className="tk-tile-name">{t.title}</span>
                  <span className="tk-tile-line">{r ? t.line(r) : " "}</span>
                </span>
              </button>
            );
          })}
        </div>
      )}
      {recent.length > 0 && !query && (
        <div className="tk-tools-recent">
          Recent
          {recent.map((t) => (
            <button key={t.key} type="button" onClick={() => onOpen(t.key)}>
              {t.title}
            </button>
          ))}
        </div>
      )}
    </section>
  );
}

/** A tool's sheet: its list, a search, its chips and the full page. */
export function ToolSheet({
  tool: key,
  onClose,
}: {
  tool: ToolKey;
  onClose: () => void;
}) {
  const { can } = useAuth();
  const tool = toolsFor(can).find((t) => t.key === key);
  const ref = useRef<HTMLElement>(null);
  const id = useId();
  const [search, setSearch] = useState("");
  const [chip, setChip] = useState<string>();
  const rows = useToolRows(tool ? [tool] : [])[key];
  useFocusTrap(ref, true, onClose);
  const drag = useSheetDrag(ref, onClose);
  useEffect(() => remember(key), [key]);
  // On a computer the chat makes room for the panel beside it.
  useEffect(() => {
    document.documentElement.classList.add("tk-tool-open");
    return () => document.documentElement.classList.remove("tk-tool-open");
  }, []);
  if (!tool) return null;
  const shown = rows ? filterRows(rows, chip, search) : [];
  return (
    <>
      <div className="tk-veil" onMouseDown={onClose} aria-hidden="true" />
      <section
        ref={ref}
        className="tk-sheet"
        role="dialog"
        aria-modal="true"
        aria-labelledby={id}
        tabIndex={-1}
        data-testid="tool-sheet"
        onKeyDown={(e) => {
          // Esc closes the sheet only, not the conversation under it.
          if (e.key === "Escape") {
            e.preventDefault();
            onClose();
          }
        }}
      >
        <div className="tk-sheet-grab" aria-hidden="true" {...drag} />
        <div className="tk-sheet-head">
          <h2 id={id}>
            {tool.title}
            {rows && <span className="tk-sheet-count">{rows.length}</span>}
          </h2>
          <button
            type="button"
            className="tk-round"
            aria-label="Close"
            onClick={onClose}
          >
            <Icon name="close" />
          </button>
        </div>
        <label className="tk-search">
          <Icon name="search" />
          <span className="sr-only">Search {tool.title.toLowerCase()}</span>
          <input
            type="search"
            placeholder={`Search ${tool.title.toLowerCase()}`}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </label>
        {tool.chips && rows && (
          <div className="tk-chips" role="group" aria-label="Show">
            <button
              type="button"
              aria-pressed={!chip}
              onClick={() => setChip(undefined)}
            >
              All
            </button>
            {tool.chips.map((c) => {
              const n = rows.filter((r) => r.tags.includes(c.tag)).length;
              return (
                <button
                  key={c.tag}
                  type="button"
                  aria-pressed={chip === c.tag}
                  onClick={() => setChip(chip === c.tag ? undefined : c.tag)}
                >
                  {c.label} · {n}
                </button>
              );
            })}
          </div>
        )}
        <div className="tk-sheet-list">
          {!rows ? (
            <div className="tk-sheet-loading" aria-busy="true">
              <span className="tk-shimmer" />
              <span className="tk-shimmer" />
              <span className="tk-shimmer" />
            </div>
          ) : shown.length === 0 ? (
            <p className="tk-sheet-empty">
              {rows.length === 0
                ? `No ${tool.title.toLowerCase()} yet.`
                : "Nothing matches."}
            </p>
          ) : (
            <ul className="tk-stagger">
              {shown.map((r) => (
                <li key={r.id}>
                  <Link className="tk-row" to={r.href} onClick={onClose}>
                    <span className="tk-row-tile" aria-hidden="true">
                      {initialsOf(
                        r.name
                          .split(/\s+/)
                          .filter((w) => /^\p{L}/u.test(w))
                          .join(" ") || r.name,
                      )}
                    </span>
                    <span className="tk-row-words">
                      <span className="tk-row-name">{r.name}</span>
                      <span className="tk-row-sub">{r.sub}</span>
                    </span>
                    {r.pill && (
                      <span className={`tk-pill ${r.pill.tone}`}>
                        {r.pill.text}
                      </span>
                    )}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
        <div className="tk-sheet-foot">
          <Link className="tk-cta" to={tool.page} onClick={onClose}>
            {tool.more}
          </Link>
        </div>
      </section>
    </>
  );
}
