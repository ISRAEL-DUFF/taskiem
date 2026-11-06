import { useCallback, useEffect, useRef, useState, type KeyboardEvent } from "react";
import type { Prefill } from "../lib/intents";
import { Link } from "react-router-dom";
import { get } from "../api";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { KitEmpty, KitLoading, Pill, SavedTick, Sheet } from "../kit";
import type { Service } from "../lib/bookings";
import type { PriceList } from "../lib/pricelists";
import type { StockItem } from "../lib/stock";
import type { Table } from "../lib/tables";
import type { TradeOrder } from "../lib/tradeorders";
import {
  OrderView,
  PriceListForm,
  ProductAdd,
  ProductForm,
  ServiceAdd,
  ServiceView,
  StockInForm,
  TableTool,
} from "./ChatToolForms";
import {
  filterRows,
  toolsFor,
  type Tool,
  type ToolKey,
  type ToolRow,
} from "../lib/tools";
import { initialsOf } from "./ChatAvatar";

// Chat tools (docs/spec/chat-tools.md §3): the tools panel over the
// composer (the grid button, or "/"), and the sheet (kit.tsx) a tool opens
// in, growing out of its tile.

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

/** A tool's rows, "failed" when they could not be loaded. */
type Loaded = ToolRow[] | "failed";

/** Each tool's rows, loaded while the panel or sheet is open. */
function useToolRows(tools: Tool[]) {
  const [rows, setRows] = useState<Partial<Record<ToolKey, Loaded>>>({});
  const [tick, setTick] = useState(0);
  const keys = tools.map((t) => t.key).join(",");
  useEffect(() => {
    let live = true;
    const urls = new Map<string, Promise<unknown>>();
    for (const t of tools) {
      if (!urls.has(t.url))
        urls.set(
          t.url,
          get(t.url).catch(() => undefined),
        );
      void urls.get(t.url)?.then((data) => {
        if (live)
          setRows((r) => ({
            ...r,
            [t.key]: data === undefined ? "failed" : t.rows(data),
          }));
      });
    }
    return () => {
      live = false;
    };
  }, [keys, tick]);
  const retry = useCallback(() => {
    setRows({});
    setTick((n) => n + 1);
  }, []);
  return { rows, retry };
}

/** The arrow keys move between the tiles of a grid of three. */
function moveTiles(e: KeyboardEvent<HTMLElement>) {
  const tiles = Array.from(
    e.currentTarget.querySelectorAll<HTMLButtonElement>(".tk-tile"),
  );
  const at = tiles.indexOf(document.activeElement as HTMLButtonElement);
  if (at < 0) return;
  const cols =
    getComputedStyle(
      e.currentTarget.querySelector(".tk-tools-grid") ?? e.currentTarget,
    ).gridTemplateColumns.split(" ").length || 3;
  const step = {
    ArrowRight: 1,
    ArrowLeft: -1,
    ArrowDown: cols,
    ArrowUp: -cols,
  }[e.key];
  if (!step) return;
  e.preventDefault();
  tiles[Math.min(tiles.length - 1, Math.max(0, at + step))]?.focus();
}

/** The panel of tiles over the composer. */
export function ToolsPanel({
  query,
  onOpen,
  onClose,
  leaving = false,
}: {
  /** What follows "/" in the box, when it was typed. */
  query: string;
  onOpen: (k: ToolKey, from: DOMRect) => void;
  onClose: () => void;
  leaving?: boolean;
}) {
  const { can } = useAuth();
  const tools = toolsFor(can, query);
  const { rows } = useToolRows(tools);
  const recent = recentTools()
    .map((k) => tools.find((t) => t.key === k))
    .filter((t): t is Tool => !!t);
  // A tap outside the composer closes it (its button toggles it).
  useEffect(() => {
    if (leaving) return;
    const out = (e: PointerEvent) => {
      const t = e.target as Element | null;
      if (t && !t.closest(".tk-tools, .composer")) onClose();
    };
    document.addEventListener("pointerdown", out);
    return () => document.removeEventListener("pointerdown", out);
  }, [leaving, onClose]);
  return (
    <section
      className={`tk-tools${leaving ? " leaving" : ""}`}
      aria-label="Tools"
      data-testid="tools-panel"
      onKeyDown={(e) => {
        if (e.key === "Escape") {
          e.preventDefault();
          onClose();
        } else moveTiles(e);
      }}
    >
      <div className="tk-tools-head">
        <h2>Tools</h2>
        <span>Opens over the chat</span>
      </div>
      {tools.length === 0 ? (
        <p className="tk-tools-none">
          No tool called “{query}”. Enter sends it as a message.
        </p>
      ) : (
        <div className="tk-tools-grid tk-stagger">
          {tools.map((t) => {
            const r = rows[t.key];
            const badge = Array.isArray(r) ? t.badge?.(r) : undefined;
            const line =
              r === undefined
                ? " "
                : r === "failed"
                  ? "Could not load"
                  : t.line(r);
            return (
              <button
                key={t.key}
                type="button"
                className="tk-tile"
                onClick={(e) =>
                  onOpen(t.key, e.currentTarget.getBoundingClientRect())
                }
              >
                <span className="tk-tile-icon">
                  <Icon name={t.icon} />
                </span>
                <span>
                  <span className="tk-tile-name">{t.title}</span>
                  {/* A count rolls into place once it is known. */}
                  <span className="tk-tile-line tk-roll" key={line}>
                    {line}
                  </span>
                </span>
                {/* After the words, so the tile is read by its name first. */}
                {badge && (
                  <span className="tk-tile-badge">
                    <Pill tone={badge.tone}>{badge.text}</Pill>
                  </span>
                )}
              </button>
            );
          })}
        </div>
      )}
      {recent.length > 0 && !query && (
        <div className="tk-tools-recent">
          Recent
          {recent.map((t) => (
            <button
              key={t.key}
              type="button"
              onClick={(e) =>
                onOpen(t.key, e.currentTarget.getBoundingClientRect())
              }
            >
              {t.title}
            </button>
          ))}
        </div>
      )}
    </section>
  );
}

/** A row's tile: the first letters of the name's first two words. */
const tileLetters = (name: string) =>
  initialsOf(
    name
      .split(/\s+/)
      .filter((w) => /^\p{L}/u.test(w))
      .join(" ") || name,
  );

/** What a tool's row opens: its form, or a look at it. */
function Detail({
  tool,
  row,
  onSaved,
  prefill,
}: {
  tool: ToolKey;
  row: ToolRow | "add";
  onSaved: (line: string | null) => void;
  prefill?: Prefill;
}) {
  if (row === "add")
    return tool === "services" ? <ServiceAdd onSaved={onSaved} prefill={prefill} /> : <ProductAdd onSaved={onSaved} />;
  switch (tool) {
    case "products":
      return <ProductForm item={row.data as StockItem} onSaved={onSaved} prefill={prefill} />;
    case "stock":
      return <StockInForm item={row.data as StockItem} onSaved={onSaved} prefill={prefill} />;
    case "services":
      return <ServiceView service={row.data as Service} />;
    case "prices":
      return <PriceListForm list={row.data as PriceList} onSaved={onSaved} />;
    case "orders":
      return <OrderView order={row.data as TradeOrder} onSaved={onSaved} />;
    case "tables":
      return <TableTool table={row.data as Table} onSaved={onSaved} />;
  }
}

/** The tools that add a new one from their sheet, and the button's words. */
const ADDS: Partial<Record<ToolKey, string>> = { products: "Add product", services: "Add service" };

/** A tool's sheet: its list (search, chips, the full page), and each row's
 * form; a save shows the tick, tells the change as a diary line where the
 * tool was opened, and closes. */
export function ToolSheet({
  tool: key,
  from,
  onClose,
  onSaved,
  start,
}: {
  tool: ToolKey;
  from?: DOMRect | null;
  onClose: () => void;
  /** Where a typed request points: a row, or a new one, filled in. */
  start?: { rowId?: string; add?: boolean; prefill?: Prefill };
  /** Tells a change (its diary line). */
  onSaved?: (line: string) => Promise<void> | void;
}) {
  const { can } = useAuth();
  const tool = toolsFor(can).find((t) => t.key === key);
  const [search, setSearch] = useState("");
  const [chip, setChip] = useState<string>();
  const [open, setOpen] = useState<ToolRow | "add" | undefined>(start?.add ? "add" : undefined);
  const [saved, setSaved] = useState(false);
  const [shut, setShut] = useState(false);
  const { rows: all, retry } = useToolRows(tool ? [tool] : []);
  const rows = all[key];
  useEffect(() => remember(key), [key]);
  // A request about a row opens its form once the rows are here.
  const started = useRef(false);
  useEffect(() => {
    if (started.current || !start?.rowId || !Array.isArray(rows)) return;
    started.current = true;
    const r = rows.find((x) => x.id === start.rowId);
    if (r) setOpen(r);
  }, [rows, start?.rowId]);
  if (!tool) return null;
  const list = Array.isArray(rows) ? rows : undefined;
  const shown = list ? filterRows(list, chip, search) : [];
  const name = tool.title.toLowerCase();
  const done = (line: string | null) => {
    if (line) void onSaved?.(line);
    setSaved(true);
    // The tick draws, then the sheet goes and the line lands in the chat.
    window.setTimeout(() => setShut(true), 1300);
  };
  const add = ADDS[key];
  return (
    <Sheet
      title={open === "add" ? (add ?? tool.title) : open ? open.name : tool.title}
      count={open ? undefined : list?.length}
      from={from}
      onClose={onClose}
      onBack={open ? () => setOpen(undefined) : undefined}
      shut={shut}
      testid="tool-sheet"
      footer={
        open ? undefined : (
          <span className="tk-foot-two">
            <Link className={add && can("table.edit") ? "tk-btn ghost" : "tk-cta"} to={tool.page} onClick={onClose}>
              {tool.more}
            </Link>
            {add && can("table.edit") && (
              <button type="button" className="tk-cta" onClick={() => setOpen("add")}>
                {add}
              </button>
            )}
          </span>
        )
      }
    >
      {saved && <SavedTick />}
      {open ? (
        <Detail tool={key} row={open} onSaved={done} prefill={start?.prefill} />
      ) : (
        <>
          <label className="tk-search">
            <Icon name="search" />
            <span className="sr-only">Search {name}</span>
            <input type="search" placeholder={`Search ${name}`} value={search} onChange={(e) => setSearch(e.target.value)} />
          </label>
          {tool.chips && list && (
            <div className="tk-chips" role="group" aria-label="Show">
              <button type="button" aria-pressed={!chip} onClick={() => setChip(undefined)}>
                All
              </button>
              {tool.chips.map((c) => {
                const n = list.filter((r) => r.tags.includes(c.tag)).length;
                return (
                  <button key={c.tag} type="button" aria-pressed={chip === c.tag} onClick={() => setChip(chip === c.tag ? undefined : c.tag)}>
                    {c.label} · {n}
                  </button>
                );
              })}
            </div>
          )}
          <div className="tk-sheet-list">
            {rows === undefined ? (
              <KitLoading label={`Loading ${name}…`} />
            ) : rows === "failed" ? (
              <KitEmpty
                icon="info"
                title={`Your ${name} could not be loaded`}
                action={
                  <button type="button" className="tk-btn soft" onClick={retry}>
                    Try again
                  </button>
                }
              >
                Check your connection, then try again.
              </KitEmpty>
            ) : shown.length === 0 ? (
              rows.length === 0 ? (
                <KitEmpty icon={tool.icon} title={`No ${name} yet`}>
                  {add ? `${add} to put the first one here.` : `${tool.more} to add the first.`}
                </KitEmpty>
              ) : (
                <KitEmpty icon="search" title="Nothing matches">
                  Try another word, or show all.
                </KitEmpty>
              )
            ) : (
              <ul className="tk-stagger">
                {shown.map((r) => (
                  <li key={r.id}>
                    <button type="button" className="tk-row" onClick={() => setOpen(r)}>
                      <span className="tk-row-tile" aria-hidden="true">
                        {tileLetters(r.name)}
                      </span>
                      <span className="tk-row-words">
                        <span className="tk-row-name">{r.name}</span>
                        <span className="tk-row-sub">{r.sub}</span>
                      </span>
                      {r.pill && <Pill tone={r.pill.tone}>{r.pill.text}</Pill>}
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      )}
    </Sheet>
  );
}
