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
  pageWords,
  statusTone,
  type ColumnView,
} from "../lib/tableView";
import { ErrorBox, fmtTime, useLoad } from "../ui";

/** Rows a page shows. */
export const PAGE = 25;

/**
 * A table as the chat's Tables tool shows it (five more flows, phase 0):
 * a pine header, the first column held while the rest scroll sideways
 * inside the frame, banded rows, each column drawn by its type (naira,
 * dates, yes/no and status pills, photos), a search over a text column,
 * and pages of 25 with "1–25 of 312". A tap on a row opens it.
 */
export function TableViewer({
  table,
  shop,
  canEdit,
  onOpen,
}: {
  table: Table;
  /** The shop's catalogue columns, when this table is its products. */
  shop?: Columns;
  canEdit: boolean;
  onOpen: (r: Row) => void;
}) {
  const textCols = table.columns.filter(
    (c) => c.type === "text" || c.type === "phone" || c.type === "email",
  );
  const [typed, setTyped] = useState("");
  const [q, setQ] = useState("");
  const [inCol, setInCol] = useState(textCols[0]?.name ?? "");
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
  const page = useLoad(
    () =>
      post<{ rows: Row[]; next_cursor: string | null; count?: number | null }>(
        `/v1/tables/${encodeURIComponent(table.id)}/rows/query`,
        {
          limit: PAGE,
          count: "exact",
          ...(cursor ? { cursor } : {}),
          ...(q && inCol
            ? {
                filters: [
                  {
                    column: inCol,
                    op: "ilike",
                    value: `%${q.replace(/[%_\\]/g, (m) => `\\${m}`)}%`,
                  },
                ],
              }
            : {}),
        },
      ),
    [table.id, cursor, q, inCol],
  );
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
          className="tk-tv-frame"
          role="region"
          aria-label={`${title}: its rows`}
          tabIndex={0}
        >
          <table className="tk-tv-table">
            <thead>
              <tr>
                {table.columns.map((c) => (
                  <th
                    key={c.name}
                    scope="col"
                    className={align(views.get(c.name))}
                  >
                    {c.title || c.name}
                  </th>
                ))}
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
                    const shown = cell(
                      r[c.name],
                      v,
                      photos.data?.get(String(r.id)),
                    );
                    return j === 0 ? (
                      <th key={c.name} scope="row" className={align(v)}>
                        {canEdit ? (
                          <button
                            type="button"
                            className="tk-tv-open"
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
            {pageWords(cursors.length, PAGE, rows.length, total)}
          </span>
          <button
            type="button"
            className="tk-btn soft"
            aria-label="Previous page"
            disabled={cursors.length === 0}
            onClick={() => setCursors(cursors.slice(0, -1))}
          >
            <Icon name="back" />
          </button>
          <button
            type="button"
            className="tk-btn soft"
            aria-label="Next page"
            disabled={!page.data?.next_cursor}
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

/** A cell as its column's kind draws it. */
function cell(value: unknown, v: ColumnView, photo?: string): ReactNode {
  if (value === null || value === undefined || value === "")
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
      return photo ? (
        <img className="tk-tv-thumb" src={photo} alt="" loading="lazy" />
      ) : (
        <span className="tk-tv-photos">{photoCount(value)}</span>
      );
    case "json":
      return <span className="tk-tv-json">{cellText(value)}</span>;
    default:
      return cellText(value);
  }
}

export type { Column };
