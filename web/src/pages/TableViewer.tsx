import { useEffect, useState, type ReactNode } from "react";
import { get, post } from "../api";
import { Icon } from "../icons";
import { KitEmpty, KitLoading, Pill } from "../kit";
import { naira } from "../lib/billing";
import { dateLabel } from "../lib/invoices";
import { photoCount } from "../lib/photos";
import { thumb, type Catalogue, type Columns } from "../lib/storefront";
import { cellText, type Column, type Row, type Table } from "../lib/tables";
import {
  columnView,
  moneyKobo,
  nextSort,
  pageWords,
  searchFilter,
  statusTone,
  type ColumnView,
  type Sort,
} from "../lib/tableView";
import { ErrorBox, fmtTime, useLoad } from "../ui";

/** Rows a page shows. */
export const PAGE = 25;

/**
 * A table as the chat's Tables tool shows it (five more flows, phase 0):
 * a pine header, the first column held while the rest scroll sideways
 * inside the frame, banded rows, each column drawn by its type (naira,
 * dates, yes/no and status pills, photos), a search over every text
 * column or one, a sort by any column from its header, and pages of 25
 * with "1–25 of 312". A tap on a row opens it.
 */
export function TableViewer({
  table,
  shop,
  canEdit,
  onOpen,
}: {
  table: Table;
  /** The shop's catalogue columns, when this table is its products; on
   * a size's row, variant_of holds its product's row id. */
  shop?: Columns & { variant_of?: string };
  canEdit: boolean;
  onOpen: (r: Row) => void;
}) {
  const textCols = table.columns.filter(
    (c) => c.type === "text" || c.type === "phone" || c.type === "email",
  );
  const [typed, setTyped] = useState("");
  const [q, setQ] = useState("");
  // Where a search looks: "" is every text column.
  const [inCol, setInCol] = useState("");
  const [sort, setSort] = useState<Sort | null>(null);
  // The cursors of the pages before this one (the first has none).
  const [cursors, setCursors] = useState<string[]>([]);
  const cursor = cursors[cursors.length - 1] ?? "";
  // A search a moment after the typing stops, back to its first page;
  // nothing when the words are the search already (on opening, too: a
  // page turned at once is kept).
  useEffect(() => {
    const words = typed.trim();
    if (words === q) return;
    const t = window.setTimeout(() => {
      setQ(words);
      setCursors([]);
    }, 300);
    return () => window.clearTimeout(t);
  }, [typed, q]);
  // What is asked for; a page that answers something else is the one
  // before, still showing while this one loads.
  const asking = JSON.stringify([table.id, cursor, q, inCol, sort]);
  const filter = searchFilter(q, inCol ? [inCol] : textCols.map((c) => c.name));
  const page = useLoad(
    () =>
      post<{ rows: Row[]; next_cursor: string | null; count?: number | null }>(
        `/v1/tables/${encodeURIComponent(table.id)}/rows/query`,
        {
          limit: PAGE,
          count: "exact",
          ...(cursor ? { cursor } : {}),
          ...(filter ? { filters: [filter] } : {}),
          ...(sort ? { order: [sort, { column: "id", desc: true }] } : {}),
        },
      ).then((p) => ({ ...p, asked: asking, at: cursors.length })),
    [asking],
  );
  const loading = page.data?.asked !== asking;
  // The shop's product photos, by row.
  const photos = useLoad(
    () =>
      shop?.photo
        ? get<Catalogue>("/v1/storefront/catalogue")
            .then(
              (c) =>
                new Map(
                  c.items.flatMap((it) => {
                    const src = thumb(it.photo);
                    return src ? [[it.id, src] as const] : [];
                  }),
                ),
            )
            .catch(() => new Map<string, string>())
        : Promise.resolve(new Map<string, string>()),
    [shop?.photo],
  );
  // A size's row shows its product's photo when it has none of its own.
  const photoOf = (r: Row) =>
    photos.data?.get(String(r.id)) ??
    (shop?.variant_of && !blank(r[shop.variant_of])
      ? photos.data?.get(String(r[shop.variant_of]))
      : undefined);
  const views = new Map(
    table.columns.map((c) => [c.name, columnView(c, shop)]),
  );
  const rows = page.data?.rows;
  const total = typeof page.data?.count === "number" ? page.data.count : null;
  const title = table.title || table.name;
  return (
    <div className="tk-tv">
      {textCols.length > 0 && (
        <form
          role="search"
          aria-label={`Search ${title}`}
          className="tk-tv-search"
          onSubmit={(e) => {
            e.preventDefault();
            setQ(typed.trim());
            setCursors([]);
          }}
        >
          <span className="tk-search">
            <Icon name="search" />
            <input
              type="search"
              value={typed}
              placeholder="Search"
              aria-label="Search rows"
              onChange={(e) => setTyped(e.target.value)}
            />
          </span>
          {textCols.length > 1 && (
            <select
              aria-label="Search in"
              value={inCol}
              onChange={(e) => {
                setInCol(e.target.value);
                setCursors([]);
              }}
            >
              <option value="">Everything</option>
              {textCols.map((c) => (
                <option key={c.name} value={c.name}>
                  {c.title || c.name}
                </option>
              ))}
            </select>
          )}
        </form>
      )}
      {!rows ? (
        page.error ? (
          <ErrorBox error={page.error} />
        ) : (
          <KitLoading label="Loading rows…" />
        )
      ) : rows.length === 0 ? (
        <KitEmpty
          icon="tables"
          title={q ? `Nothing found for “${q}”` : "No rows yet"}
        />
      ) : (
        <div
          className={`tk-tv-frame${loading ? " busy" : ""}`}
          role="region"
          aria-label={`${title}: its rows`}
          aria-busy={loading}
          tabIndex={0}
        >
          <table className="tk-tv-table">
            <thead>
              <tr>
                {table.columns.map((c) => {
                  const on = sort?.column === c.name ? sort : null;
                  return (
                    <th
                      key={c.name}
                      scope="col"
                      className={align(views.get(c.name))}
                      aria-sort={
                        on ? (on.desc ? "descending" : "ascending") : undefined
                      }
                    >
                      <button
                        type="button"
                        className="tk-tv-sort"
                        onClick={() => {
                          setSort(nextSort(sort, c.name));
                          setCursors([]);
                        }}
                      >
                        {c.title || c.name}
                      </button>
                    </th>
                  );
                })}
              </tr>
            </thead>
            <tbody>
              {rows.map((r, i) => (
                <tr
                  key={String(r.id ?? i)}
                  onClick={canEdit ? () => onOpen(r) : undefined}
                  className={canEdit ? "open" : undefined}
                >
                  {table.columns.map((c, j) => {
                    const v = views.get(c.name) ?? { kind: "text" as const };
                    const shown = cell(r[c.name], v, photoOf(r));
                    return j === 0 ? (
                      <th key={c.name} scope="row" className={align(v)}>
                        {canEdit ? (
                          <button
                            type="button"
                            className="tk-tv-open"
                            aria-label={
                              blank(r[c.name])
                                ? `Open row ${String(r.id ?? i + 1)}`
                                : undefined
                            }
                            onClick={(e) => {
                              e.stopPropagation();
                              onOpen(r);
                            }}
                          >
                            {shown}
                          </button>
                        ) : (
                          shown
                        )}
                      </th>
                    ) : (
                      <td key={c.name} className={align(v)}>
                        {shown}
                      </td>
                    );
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
      {rows && (cursors.length > 0 || rows.length > 0) && (
        <nav className="tk-tv-pages" aria-label="Pages">
          <span className="tk-tv-count" role="status">
            {loading
              ? "Loading…"
              : pageWords(page.data?.at ?? 0, PAGE, rows.length, total)}
          </span>
          <button
            type="button"
            className="tk-btn soft"
            aria-label="Previous page"
            disabled={loading || cursors.length === 0}
            onClick={() => setCursors(cursors.slice(0, -1))}
          >
            <Icon name="back" />
          </button>
          <button
            type="button"
            className="tk-btn soft"
            aria-label="Next page"
            disabled={loading || !page.data?.next_cursor}
            onClick={() => {
              const next = page.data?.next_cursor;
              if (next) setCursors([...cursors, next]);
            }}
          >
            <Icon name="chevron" />
          </button>
        </nav>
      )}
    </div>
  );
}

function align(v?: ColumnView): string | undefined {
  return v && (v.kind === "money" || v.kind === "number") ? "num" : undefined;
}

function blank(value: unknown): boolean {
  return value === null || value === undefined || value === "";
}

/** A cell as its column's kind draws it. */
function cell(value: unknown, v: ColumnView, photo?: string): ReactNode {
  if (v.kind === "photo" && photo)
    return <img className="tk-tv-thumb" src={photo} alt="" loading="lazy" />;
  if (blank(value))
    return v.kind === "photo" ? (
      <span className="tk-tv-none">No photo</span>
    ) : (
      <span className="tk-tv-none">—</span>
    );
  switch (v.kind) {
    case "money": {
      const k = moneyKobo(value, v.unit);
      return k === null ? cellText(value) : naira(k).replace(/\.00$/, "");
    }
    case "number": {
      const n = typeof value === "number" ? value : Number(value);
      return Number.isFinite(n) ? n.toLocaleString("en-NG") : cellText(value);
    }
    case "date":
      return dateLabel(String(value)) || cellText(value);
    case "datetime":
      return fmtTime(String(value));
    case "yesno": {
      const yes = value === true || /^(true|yes|1)$/i.test(String(value));
      return <Pill tone={yes ? "lime" : "plain"}>{yes ? "Yes" : "No"}</Pill>;
    }
    case "status": {
      const s = cellText(value);
      return <Pill tone={statusTone(s)}>{s}</Pill>;
    }
    case "photo":
      return <span className="tk-tv-photos">{photoCount(value)}</span>;
    case "json":
      return <span className="tk-tv-json">{cellText(value)}</span>;
    default:
      return cellText(value);
  }
}

export type { Column };
