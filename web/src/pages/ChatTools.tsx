import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import type { Prefill } from "../lib/intents";
import type { Purchase } from "../lib/purchases";
import type { Bill } from "../lib/bills";
import { BillView, PurchaseView } from "./ChatBuyer";
import { GroupForm } from "./SizesForms";
import { Link } from "react-router-dom";
import { get } from "../api";
import type { ChatRef } from "../lib/refs";
import { useAuth } from "../auth";
import { Icon } from "../icons";
import { InvoiceAdd, InvoiceView, MoneyAdd, MoneyEntryView } from "./ChatMoney";
import type { CustomerInvoice } from "../lib/invoices";
import type { MoneyEntry } from "../lib/chatmoney";
import { tileLetters, toneOf } from "../lib/tone";
import { KitEmpty, KitLoading, Pill, SavedTick, Sheet } from "../kit";
import type { Service } from "../lib/bookings";
import { bookLine, useLinkServices } from "../lib/linkbook";
import type { PriceList } from "../lib/pricelists";
import type { StockItem } from "../lib/stock";
import type { Table } from "../lib/tables";
import type { TradeOrder } from "../lib/tradeorders";
import {
  OrderView,
  PriceListForm,
  ProductAdd,
  ProductForm,
  ServiceForm,
  StockInForm,
  TableTool,
} from "./ChatToolForms";
import {
  BUSINESS_TOOLS,
  filterRows,
  categoryChips,
  toolsFor,
  type Tool,
  type ToolKey,
  type ToolRow,
} from "../lib/tools";

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

/** The rows with one business. In a business's conversation the tools of
 * what is between us (BETWEEN_US) show only what is with that business: a
 * row of it, or one the conversation's things name (an invoice to them, by
 * its id), and nothing until those are known; another tool's rows (our
 * price lists, to share) all stay. */
const inScope = (
  rows: ToolRow[],
  scope?: string,
  key?: ToolKey,
  ids?: Set<string>,
): ToolRow[] | undefined => {
  if (!scope) return rows;
  const strict = key !== undefined && BETWEEN_US.includes(key);
  if (strict && !ids) return undefined;
  return rows.filter((r) =>
    strict
      ? r.link === scope || !!ids?.has(r.id)
      : r.link === undefined || r.link === scope,
  );
};

/** The tools whose rows are between us and one business. */
const BETWEEN_US: ToolKey[] = ["purchases", "orders", "bills", "invoices"];

// What is between us and each business, read once for the panel and its
// sheet together, and again when the conversation is opened anew.
const scopeIdsCache = new Map<string, Promise<Set<string>>>();

/** The ids of what is between us and a business (invoices, bills, orders):
 * what a row with no business of its own is matched to; undefined while
 * they are read. */
function useScopeIds(scope?: string): Set<string> | undefined {
  const [got, setGot] = useState<{ scope: string; ids: Set<string> }>();
  useEffect(() => {
    if (!scope) return;
    let on = true;
    let p = scopeIdsCache.get(scope);
    if (!p) {
      p = get<{ refs: ChatRef[] }>(
        `/v1/link-chats/${encodeURIComponent(scope)}/refs`,
      ).then(
        (r) => new Set((r.refs ?? []).map((x) => x.id)),
        () => new Set<string>(),
      );
      scopeIdsCache.set(scope, p);
      // Shared by the panel and the sheet it opens, then read again: a new
      // invoice or order shows the next time either opens.
      window.setTimeout(() => scopeIdsCache.delete(scope), 5_000);
    }
    void p.then((ids) => on && setGot({ scope, ids }));
    return () => {
      on = false;
    };
  }, [scope]);
  // Another conversation's, while this one's are read: none yet.
  return got && got.scope === scope ? got.ids : undefined;
}

/** The row a request or a card points at: by its id anywhere, else by its
 * number among this business's rows only. A number is the seller's own
 * (INV-0001 from two suppliers is two bills), so outside a conversation a
 * number matching more than one row opens none. */
export function startRow(
  rows: ToolRow[],
  rowId: string,
  scope?: string,
): ToolRow | undefined {
  const byId = rows.find((x) => x.id === rowId);
  if (byId) return byId;
  const named = (inScope(rows, scope) ?? []).filter(
    (x) => (x.data as { reference?: string } | undefined)?.reference === rowId,
  );
  return named.length === 1 ? named[0] : undefined;
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

/** A tile of the tools menu that is not a tool: a tap runs it. */
export interface ExtraTile {
  key: string;
  title: string;
  icon: string;
  line: string;
  onPick: () => void;
}

/** The extra tiles what follows "/" names. */
export const extrasFor = (extras: ExtraTile[] | undefined, query: string) => {
  const q = query.trim().toLowerCase();
  return (extras ?? []).filter(
    (x) => !q || x.title.toLowerCase().startsWith(q),
  );
};

/** The panel of tiles over the composer. */
export function ToolsPanel({
  query,
  onOpen,
  onClose,
  leaving = false,
  scope,
  supplier,
  business,
  extras,
}: {
  /** What follows "/" in the box, when it was typed. */
  query: string;
  onOpen: (k: ToolKey, from: DOMRect) => void;
  onClose: () => void;
  leaving?: boolean;
  /** The business's conversation it is open in: its orders and bills. */
  scope?: string;
  /** In a supplier's conversation (they share prices): ordering comes first. */
  supplier?: string;
  /** The business the conversation is with, for the heading. */
  business?: string;
  extras?: ExtraTile[];
}) {
  const more = extrasFor(extras, query);
  const { can, me } = useAuth();
  // In a business's conversation, the tools about that business only
  // (docs/spec/business-chat-tools.md §3); the rest are in Taskiem's.
  const listed = toolsFor(can, query, me?.roles, me?.delivers).filter(
    (t) => !scope || BUSINESS_TOOLS.includes(t.key),
  );
  const { rows: loaded } = useToolRows(listed);
  const ids = useScopeIds(scope);
  const rows: typeof loaded = Object.fromEntries(
    Object.entries(loaded).map(([k, r]) => [
      k,
      Array.isArray(r) ? inScope(r, scope, k as ToolKey, ids) : r,
    ]),
  );
  const q = query.trim().toLowerCase();
  // Their shop, first, for everyone in a business's conversation: to order
  // from where they may, else to look around (business-chat-tools.md §3).
  const shopFor = business ?? supplier;
  const ordering =
    !!shopFor &&
    (!q || ["order", "their shop", "shop"].some((w) => w.startsWith(q)));
  // Book, after it: their services, shown only when they offer any.
  const services = useLinkServices(shopFor ? scope : undefined);
  const booking =
    !!shopFor && !!services?.services.length && (!q || "book".startsWith(q));
  const tools: Tool[] = [
    ...(ordering
      ? [
          {
            key: "order",
            title: "Their shop",
            icon: "plus",
            perm: "",
            url: "",
            page: "/storefront/suppliers",
            rows: () => [],
            line: () => `From ${shopFor}`,
            more: "",
          } satisfies Tool,
        ]
      : []),
    ...(booking && services
      ? [
          {
            key: "book",
            title: "Book",
            icon: "calendar",
            perm: "",
            url: "",
            page: "",
            rows: () => [],
            line: () => bookLine(services),
            more: "",
          } satisfies Tool,
        ]
      : []),
    ...listed,
  ];
  if (ordering) rows.order = [];
  if (booking) rows.book = [];
  const recent = recentTools()
    .map((k) => tools.find((t) => t.key === k))
    .filter((t): t is Tool => !!t);
  // It fits the room above the message bar, the keyboard's too: the
  // tiles scroll inside it while its heading and Recent stay in place.
  const panel = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    const el = panel.current;
    const bar = el?.parentElement;
    if (!el || !bar) return;
    const fit = () => {
      const top = window.visualViewport?.offsetTop ?? 0;
      const room = bar.getBoundingClientRect().top - top - 12;
      el.style.maxHeight = `${Math.max(160, Math.floor(room))}px`;
    };
    fit();
    const vv = window.visualViewport;
    window.addEventListener("resize", fit);
    vv?.addEventListener("resize", fit);
    const ro =
      typeof ResizeObserver === "function" ? new ResizeObserver(fit) : null;
    ro?.observe(bar);
    return () => {
      window.removeEventListener("resize", fit);
      vv?.removeEventListener("resize", fit);
      ro?.disconnect();
    };
  }, []);
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
      ref={panel}
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
        <span>{business ? `With ${business}` : "Opens over the chat"}</span>
      </div>
      {tools.length + more.length === 0 ? (
        <p className="tk-tools-none">
          No tool called “{query}”. Enter sends it as a message.
        </p>
      ) : (
        <div className="tk-tools-grid tk-stagger">
          {tools.map((t) => {
            const r = rows[t.key];
            const badge = Array.isArray(r) ? t.badge?.(r) : undefined;
            const title =
              Array.isArray(r) && t.titleFor ? t.titleFor(r) : t.title;
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
                  <span className="tk-tile-name">{title}</span>
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
          {more.map((x) => (
            <button
              key={x.key}
              type="button"
              className="tk-tile"
              onClick={() => {
                onClose();
                x.onPick();
              }}
            >
              <span className="tk-tile-icon">
                <Icon name={x.icon} />
              </span>
              <span>
                <span className="tk-tile-name">{x.title}</span>
                <span className="tk-tile-line">{x.line}</span>
              </span>
            </button>
          ))}
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

/** What a tool's row opens: its form, or a look at it. */
/** Whether the person may change what a tool shows (else they see it). */
export function mayEdit(tool: ToolKey, can: (p: string) => boolean): boolean {
  switch (tool) {
    case "products":
    case "stock":
    case "tables":
      return can("table.edit");
    case "services":
      return can("table.edit") && can("storefront.manage");
    case "purchases":
      return can("storefront.manage");
    case "orders":
      return can("storefront.manage|order.handle");
    case "sell":
      return can("table.edit");
    case "order":
      // Drafting needs table.edit; sending and the supplier, storefront.manage.
      return can("table.edit") && can("storefront.manage");
    case "bills":
    case "invoices":
    case "money":
      return can("invoice.manage");
    default:
      return can("storefront.manage");
  }
}

function Detail({
  tool,
  row,
  onSaved,
  prefill,
  canEdit,
  onReorder,
  onCancel,
  scope,
}: {
  tool: ToolKey;
  row: ToolRow | "add";
  onSaved: (line: string | null) => void;
  prefill?: Prefill;
  canEdit: boolean;
  onReorder?: (p: Purchase) => void;
  /** Back to the list without saving (a form's Cancel). */
  onCancel?: () => void;
  /** The business whose conversation the sheet was opened from. */
  scope?: string;
}) {
  if (row === "add")
    return tool === "services" ? (
      <ServiceForm onSaved={onSaved} prefill={prefill} />
    ) : tool === "invoices" ? (
      <InvoiceAdd onSaved={onSaved} onCancel={onCancel} />
    ) : tool === "money" ? (
      <MoneyAdd onSaved={onSaved} onCancel={onCancel} />
    ) : (
      <ProductAdd onSaved={onSaved} />
    );
  switch (tool) {
    case "products":
      return (
        <ProductForm
          item={row.data as StockItem}
          onSaved={onSaved}
          onCancel={onCancel}
          prefill={prefill}
          canEdit={canEdit}
          link={scope}
        />
      );
    case "stock":
      return (
        <StockInForm
          item={row.data as StockItem}
          onSaved={onSaved}
          prefill={prefill}
          canEdit={canEdit}
        />
      );
    case "services":
      return (
        <ServiceForm
          service={row.data as Service}
          onSaved={onSaved}
          canEdit={canEdit}
        />
      );
    case "prices":
      return (
        <PriceListForm
          list={row.data as PriceList}
          onSaved={onSaved}
          canEdit={canEdit}
        />
      );
    case "orders":
      return (
        <OrderView
          order={row.data as TradeOrder}
          onSaved={onSaved}
          canEdit={canEdit}
        />
      );
    case "tables":
      return (
        <TableTool
          table={row.data as Table}
          onSaved={onSaved}
          canEdit={canEdit}
        />
      );
    case "purchases":
      return (
        <PurchaseView
          purchase={row.data as Purchase}
          onSaved={onSaved}
          onReorder={
            onReorder && (row.data as Purchase).link_id ? onReorder : undefined
          }
        />
      );
    case "bills":
      return (
        <BillView bill={row.data as Bill} onSaved={onSaved} canEdit={canEdit} />
      );
    case "invoices":
      return (
        <InvoiceView
          invoice={row.data as CustomerInvoice}
          onSaved={onSaved}
          canEdit={canEdit}
        />
      );
    case "money":
      return (
        <MoneyEntryView
          entry={row.data as MoneyEntry}
          onSaved={onSaved}
          canEdit={canEdit}
        />
      );
    case "order":
      return null;
  }
}

/** A product row that can be made one of a product's sizes: an item of
 * its own, not sold in sizes and colours, nor one of them. */
function plainItem(r: ToolRow): boolean {
  const it = r.data as StockItem | undefined;
  return !!it && !it.variant_of && !it.choices?.length;
}

/** The tools that add a new one from their sheet, and the button's words. */
const ADDS: Partial<Record<ToolKey, string>> = {
  products: "Add product",
  services: "Add service",
  invoices: "New invoice",
  money: "Add cash or expense",
};

/** A tool's sheet: its list (search, chips, the full page), and each row's
 * form; a save shows the tick, tells the change as a diary line where the
 * tool was opened, and closes. */
export function ToolSheet({
  tool: key,
  from,
  onClose,
  onSaved,
  start,
  scope,
  onReorder,
  onNewOrder,
  onTools,
}: {
  tool: ToolKey;
  from?: DOMRect | null;
  onClose: () => void;
  /** The business's conversation it is open in: its orders and bills only. */
  scope?: string;
  /** Your orders: an order again, in the Order sheet. */
  onReorder?: (p: Purchase) => void;
  /** Your orders, in a supplier's conversation: a new order to them. */
  onNewOrder?: () => void;
  /** Where a typed request points: a row, or a new one, filled in; or a
   * chip already on (a category picked in "@"). */
  start?: { rowId?: string; add?: boolean; prefill?: Prefill; chip?: string };
  /** Tells a change (its diary line). */
  onSaved?: (line: string) => Promise<void> | void;
  /** Back from the list: the tools panel again. */
  onTools?: () => void;
}) {
  const { can, me } = useAuth();
  const tool = toolsFor(can, "", me?.roles, me?.delivers).find(
    (t) => t.key === key,
  );
  const [search, setSearch] = useState("");
  const [chip, setChip] = useState<string | undefined>(start?.chip);
  const [open, setOpen] = useState<ToolRow | "add" | undefined>(
    start?.add ? "add" : undefined,
  );
  const [saved, setSaved] = useState(false);
  const [shut, setShut] = useState(false);
  // Make one product with sizes and colours: the items chosen, then the
  // form (Products only, for those who may change them).
  const [picking, setPicking] = useState(false);
  const [picked, setPicked] = useState<string[]>([]);
  const [grouping, setGrouping] = useState(false);
  const { rows: all, retry } = useToolRows(tool ? [tool] : []);
  const ids = useScopeIds(scope);
  const rows = all[key];
  useEffect(() => remember(key), [key]);
  // A request about a row opens its form once the rows are here.
  const started = useRef(false);
  useEffect(() => {
    if (started.current || !start?.rowId || !Array.isArray(rows)) return;
    started.current = true;
    // By its id, or by its number (a bill's card names it by its number),
    // among this business's rows.
    const r = startRow(rows, start.rowId, scope);
    if (r) setOpen(r);
  }, [rows, start?.rowId, scope]);
  if (!tool) return null;
  // In a business's conversation, the orders and bills with that business.
  const list = Array.isArray(rows) ? inScope(rows, scope, key, ids) : undefined;
  const shown = list ? filterRows(list, chip, search) : [];
  const title = list && tool.titleFor ? tool.titleFor(list) : tool.title;
  // The rows' categories, as chips after the tool's own.
  const cats = list ? categoryChips(list, chip) : [];
  const name = title.toLowerCase();
  const done = (line: string | null) => {
    if (line) void onSaved?.(line);
    setSaved(true);
    // The tick draws, then the sheet goes and the line lands in the chat.
    window.setTimeout(() => setShut(true), 1300);
  };
  const add = ADDS[key];
  const groupable =
    key === "products" &&
    mayEdit(key, can) &&
    (list ?? []).filter(plainItem).length >= 2;
  const pickedItems = (list ?? [])
    .filter((r) => picked.includes(r.id))
    .map((r) => r.data as StockItem);
  const stopPicking = () => {
    setPicking(false);
    setPicked([]);
  };
  return (
    <Sheet
      title={
        grouping
          ? "Make one product"
          : open === "add"
            ? (add ?? title)
            : open
              ? open.name
              : title
      }
      count={open || grouping ? undefined : list?.length}
      crumb={open && open !== "add" ? title : undefined}
      from={from}
      onClose={onClose}
      onBack={
        open
          ? () => setOpen(undefined)
          : grouping
            ? () => setGrouping(false)
            : picking
              ? stopPicking
              : undefined
      }
      onOut={onTools}
      shut={shut}
      testid="tool-sheet"
      footer={
        open || grouping ? undefined : picking ? (
          <button
            type="button"
            className="tk-cta"
            disabled={picked.length < 2}
            onClick={() => setGrouping(true)}
          >
            {picked.length < 2
              ? "Choose at least two"
              : `Make one product of ${picked.length}`}
          </button>
        ) : key === "purchases" && onNewOrder ? (
          <button type="button" className="tk-cta" onClick={onNewOrder}>
            New order
          </button>
        ) : add && mayEdit(key, can) ? (
          <button
            type="button"
            className="tk-cta"
            onClick={() => setOpen("add")}
          >
            <Icon name="plus" />
            {add}
          </button>
        ) : (
          <Link className="tk-cta" to={tool.page} onClick={onClose}>
            {tool.more}
          </Link>
        )
      }
    >
      {saved && <SavedTick />}
      {grouping ? (
        <GroupForm
          items={pickedItems}
          onSaved={(line) => {
            stopPicking();
            done(line);
          }}
          onCancel={() => setGrouping(false)}
        />
      ) : open ? (
        <Detail
          tool={key}
          row={open}
          onSaved={done}
          prefill={start?.prefill}
          canEdit={mayEdit(key, can)}
          onReorder={onReorder}
          onCancel={() => setOpen(undefined)}
          scope={scope}
        />
      ) : (
        <>
          <label className="tk-search">
            <Icon name="search" />
            <span className="sr-only">Search {name}</span>
            <input
              type="search"
              placeholder={`Search ${name}`}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
            />
          </label>
          {list && (tool.chips || cats.length > 0) && (
            <div className="tk-chips" role="group" aria-label="Show">
              <button
                type="button"
                aria-pressed={!chip}
                onClick={() => setChip(undefined)}
              >
                All
              </button>
              {[...(tool.chips ?? []), ...cats].map((c) => {
                const n = list.filter((r) => r.tags.includes(c.tag)).length;
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
          {groupable && (
            <p className="tk-group-bar">
              {picking ? (
                <>
                  <span role="status">
                    Choose the items that are one product in sizes and colours.
                  </span>
                  <button
                    type="button"
                    className="tk-btn soft"
                    onClick={stopPicking}
                  >
                    Cancel
                  </button>
                </>
              ) : (
                <button
                  type="button"
                  className="tk-btn soft"
                  onClick={() => setPicking(true)}
                >
                  Make one product with sizes and colours
                </button>
              )}
            </p>
          )}
          <div className="tk-sheet-list">
            {rows === undefined || (Array.isArray(rows) && !list) ? (
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
                  {add
                    ? `${add} to put the first one here.`
                    : `${tool.more} to add the first.`}
                </KitEmpty>
              ) : (
                <KitEmpty icon="search" title="Nothing matches">
                  Try another word, or show all.
                </KitEmpty>
              )
            ) : (
              <>
                <ul className="tk-stagger">
                  {shown.map((r) => (
                    <li key={r.id}>
                      {picking ? (
                        <label
                          className={`tk-row tk-pick${plainItem(r) ? "" : " off"}`}
                        >
                          <input
                            type="checkbox"
                            checked={picked.includes(r.id)}
                            disabled={!plainItem(r)}
                            onChange={(e) =>
                              setPicked(
                                e.target.checked
                                  ? [...picked, r.id]
                                  : picked.filter((x) => x !== r.id),
                              )
                            }
                          />
                          <span className="tk-row-words">
                            <span className="tk-row-name">{r.name}</span>
                            <span className="tk-row-sub">
                              {plainItem(r)
                                ? r.sub
                                : "Already sold in sizes and colours"}
                            </span>
                          </span>
                        </label>
                      ) : (
                        <button
                          type="button"
                          className="tk-row"
                          onClick={() => setOpen(r)}
                        >
                          <span
                            className="tk-row-tile"
                            aria-hidden="true"
                            data-tone={
                              tool.rowIcon ? undefined : toneOf(r.name)
                            }
                          >
                            {tool.rowIcon ? (
                              <Icon name={tool.rowIcon} />
                            ) : (
                              tileLetters(r.name)
                            )}
                          </span>
                          <span className="tk-row-words">
                            <span className="tk-row-name">{r.name}</span>
                            <span className="tk-row-sub">{r.sub}</span>
                          </span>
                          {r.pill && (
                            <Pill tone={r.pill.tone}>{r.pill.text}</Pill>
                          )}
                        </button>
                      )}
                    </li>
                  ))}
                </ul>
                {/* The full page, for what the sheet does not do: after the
                  list, as the boards keep the head to the title. */}
                <Link
                  className="tk-sheet-more"
                  to={tool.page}
                  onClick={onClose}
                >
                  {tool.more}
                  <Icon name="chevron" />
                </Link>
              </>
            )}
          </div>
        </>
      )}
    </Sheet>
  );
}
