import { useEffect, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { get, post } from "../api";
import { Icon } from "../icons";
import { EmptyState, ErrorBox, Field, Items, Item, Modal, PageHeader, Skeleton, useAction, useLoad } from "../ui";
import {
  DATE_CHOICES,
  filtersParam,
  keptFilters,
  grouped,
  layout,
  rowsWord,
  toneClass,
  viewWithFilters,
  whoWords,
  type DrawnFilter,
  type DrawnPage,
  type DrawnPart,
  type DrawnRow,
  type PageDetail,
  type PageList,
} from "../lib/pages";
import type { Table } from "../lib/tables";

// Pages (decision 0072, docs/pages.md): the business's own screens. The
// list, and a page drawn from what the server worked out for this person
// (GET /v1/pages/{name}/view): nothing here reads a table itself.

const ICONS: Record<string, string> = { box: "package", store: "storefront", grid: "dashboard" };
/** A page's picture as one of the app's icons. */
export const pageIcon = (icon: string) => ICONS[icon] ?? (icon || "tables");

/** The Pages area: the pages this person may open, pinned first. */
export function Pages() {
  const list = useLoad(() => get<PageList>("/v1/pages"), []);
  const [making, setMaking] = useState(false);
  const data = list.data;
  return (
    <>
      <PageHeader
        title="Pages"
        description="Screens your business makes for itself from its tables: figures, lists and tables, for a phone first."
        actions={
          data?.can_edit && (
            <button className="primary" onClick={() => setMaking(true)}>
              <Icon name="plus" />
              New page
            </button>
          )
        }
      />
      {list.error ? (
        <ErrorBox error={list.error} />
      ) : !data ? (
        <Skeleton rows={3} label="Loading your pages…" />
      ) : data.pages.length === 0 ? (
        <EmptyState
          icon="tables"
          action={
            data.can_edit && (
              <button className="primary" onClick={() => setMaking(true)}>
                Make your first page
              </button>
            )
          }
        >
          {data.can_edit ? "No pages yet. Make one from a table in a tap, then change what it shows." : "No pages have been shared with you yet."}
        </EmptyState>
      ) : (
        <Items label="Pages">
          {data.pages.map((p) => (
            <Item
              key={p.name}
              to={`/pages/${p.name}`}
              icon={pageIcon(p.icon)}
              title={p.title}
              side={p.pinned ? <span className="badge sf-status ok">Pinned</span> : !p.published ? <span className="badge sf-status muted">Draft</span> : undefined}
              sub={[p.about, data.can_edit ? whoWords(p.roles) : "", data.can_edit && p.published && p.has_draft ? "Changes not published" : ""].filter(Boolean).join(" · ") || undefined}
              testid="page-tile"
            />
          ))}
        </Items>
      )}
      {making && <NewPage onClose={() => setMaking(false)} />}
    </>
  );
}

/** Picks the table a new page shows, and makes it (Show as a page). */
function NewPage({ onClose }: { onClose: () => void }) {
  const tables = useLoad(() => get<{ tables: Table[] }>("/v1/tables?counts=no"), []);
  const [table, setTable] = useState("");
  const act = useAction();
  const navigate = useNavigate();
  const list = (tables.data?.tables ?? []).filter((t) => !t.kept_by);
  return (
    <Modal title="New page" onClose={onClose}>
      {!!tables.error && <ErrorBox error={tables.error} />}
      {tables.data && list.length === 0 ? (
        <p>
          A page shows one of your tables. <Link to="/tables">Make a table</Link> first.
        </p>
      ) : (
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              const pg = await post<PageDetail>("/v1/pages", { from_table: table || list[0]?.id });
              navigate(`/pages/${pg.name}/edit`);
            }, false);
          }}
        >
          <Field label="What should it show?" hint="Taskiem starts it with how many there are and the rows as a list. You change it after.">
            <select value={table || list[0]?.id || ""} onChange={(e) => setTable(e.target.value)}>
              {list.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.title || t.name}
                </option>
              ))}
            </select>
          </Field>
          <ErrorBox error={act.error} />
          <div className="toolbar">
            <button className="primary" disabled={act.busy || !tables.data}>
              Make the page
            </button>
            <button type="button" onClick={onClose}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </Modal>
  );
}

/** Makes a first page from a table in one tap (Show as a page) and opens its editor. */
export function ShowAsPage({ table }: { table: Table }) {
  const act = useAction();
  const navigate = useNavigate();
  return (
    <>
      <button
        disabled={act.busy}
        onClick={() =>
          void act.run(async () => {
            const pg = await post<PageDetail>("/v1/pages", { from_table: table.id });
            navigate(`/pages/${pg.name}/edit`);
          }, false)
        }
      >
        <Icon name="dashboard" />
        Show as a page
      </button>
      {!!act.error && <ErrorBox error={act.error} />}
    </>
  );
}

const lastFilters = (name: string): Record<string, string> => {
  try {
    return JSON.parse(localStorage.getItem(`taskiem.page-filters.${name}`) ?? "{}") as Record<string, string>;
  } catch {
    return {};
  }
};

/** One page, drawn for the person looking: its filters, its figures, lists and tables. */
export function PageScreen() {
  const { name = "" } = useParams();
  const [search] = useSearchParams();
  const draft = search.get("draft") === "1";
  // The page remembers a viewer's last filters on this device (pages §6).
  const [values, setValues] = useState<Record<string, string>>(() => lastFilters(name));
  const keep = (next: Record<string, string>) => {
    setValues(next);
    try {
      localStorage.setItem(`taskiem.page-filters.${name}`, JSON.stringify(next));
    } catch {
      // no storage: kept for this visit only
    }
  };
  const q = filtersParam(values);
  // Saved filters the page no longer has are dropped: refused, the page
  // is drawn once more without them.
  const view = useLoad(
    () =>
      viewWithFilters(
        (query) => {
          const all = [query, draft ? "draft=1" : ""].filter(Boolean).join("&");
          return get<DrawnPage>(`/v1/pages/${name}/view${all ? `?${all}` : ""}`);
        },
        values,
        () => keep({}),
      ),
    [name, q, draft],
  );
  // Drawn: keep only the values for the filters (and choices) it has now.
  useEffect(() => {
    if (!view.data) return;
    const kept = keptFilters(values, view.data.filters);
    if (JSON.stringify(kept) !== JSON.stringify(values)) keep(kept);
  }, [view.data]);
  const set = (id: string, v: string) => keep({ ...values, [id]: v });
  if (view.error) {
    return (
      <>
        <p className="crumbs">
          <Link to="/pages" aria-label="Back to Pages">
            Pages
          </Link>
        </p>
        <ErrorBox error={view.error} />
      </>
    );
  }
  if (!view.data) return <Skeleton rows={4} label="Drawing the page…" />;
  return <DrawnPageView page={view.data} values={values} onFilter={set} onRefresh={view.reload} />;
}

/** A page as drawn (the page itself, and the editor's preview). */
export function DrawnPageView({
  page,
  values,
  onFilter,
  onRefresh,
  preview = false,
}: {
  page: DrawnPage;
  values: Record<string, string>;
  onFilter: (id: string, v: string) => void;
  onRefresh: () => void;
  preview?: boolean;
}) {
  const [open, setOpen] = useState<DrawnRow | null>(null);
  const name = page.page.name;
  const Heading = preview ? "h2" : "h1";
  return (
    <div className="pg" data-testid={preview ? "page-preview" : "page"}>
      {!preview && (
        <p className="crumbs">
          <Link to="/pages" aria-label="Back to Pages">
            Pages
          </Link>
        </p>
      )}
      <div className="page-header">
        <div className="page-heading">
          <Heading>{page.page.title}</Heading>
        </div>
        {!preview && (
          <div className="page-actions">
            <button onClick={onRefresh}>
              <Icon name="retry" />
              Refresh
            </button>
            {page.can_edit && (
              <Link className="button" to={`/pages/${name}/edit`}>
                <Icon name="pencil" />
                Change
              </Link>
            )}
          </div>
        )}
      </div>
      {page.page.about && <p className="hint">{page.page.about}</p>}
      {page.draft && !preview && (
        <div className="notice" role="note">
          This is the draft: people see the published page until you publish it.
        </div>
      )}
      {page.filters.length > 0 && <Filters filters={page.filters} values={values} onFilter={onFilter} page={page.page.title} />}
      {layout(page.blocks).map((p) =>
        Array.isArray(p) ? (
          <div className="pg-figures" key={p[0]?.id}>
            {p.map((f) => (
              <Figure key={f.id} part={f} />
            ))}
          </div>
        ) : (
          <Part key={`${p.id}:${page.drawn_at}:${filtersParam(values)}:${p.count ?? ""}`} part={p} name={name} draft={page.draft} values={values} onOpen={setOpen} />
        ),
      )}
      {open?.detail && (
        <Modal title={open.detail.title || "Details"} onClose={() => setOpen(null)}>
          <dl className="pg-lines" data-testid="page-detail">
            {open.detail.lines.map((l) => (
              <div key={l.label}>
                <dt>{l.label}</dt>
                <dd>{l.value || "—"}</dd>
              </div>
            ))}
          </dl>
          <div className="toolbar">
            <button onClick={() => setOpen(null)}>Close</button>
          </div>
        </Modal>
      )}
    </div>
  );
}

function Filters({ filters, values, onFilter, page }: { filters: DrawnFilter[]; values: Record<string, string>; onFilter: (id: string, v: string) => void; page: string }) {
  const [words, setWords] = useState<Record<string, string>>(() => ({ ...values }));
  return (
    <div className="pg-filters" role="search" aria-label={`Filter ${page}`}>
      {filters.map((f) => {
        const v = values[f.id] ?? "";
        switch (f.type) {
          case "search":
            return (
              <form
                key={f.id}
                className="pg-search"
                onSubmit={(e) => {
                  e.preventDefault();
                  onFilter(f.id, words[f.id] ?? "");
                }}
              >
                <input type="search" aria-label={f.label} placeholder={`${f.label}…`} value={words[f.id] ?? ""} onChange={(e) => setWords({ ...words, [f.id]: e.target.value })} />
                <button className="icon-button" aria-label={`${f.label}: go`}>
                  <Icon name="search" />
                </button>
              </form>
            );
          case "choice":
            return (
              <select key={f.id} aria-label={f.label} value={v} onChange={(e) => onFilter(f.id, e.target.value)}>
                <option value="">{`${f.label}: all`}</option>
                {(f.options ?? []).map((o) => (
                  <option key={o.value} value={o.value}>
                    {o.label}
                  </option>
                ))}
              </select>
            );
          case "dates":
            return (
              <select key={f.id} aria-label={f.label} value={v} onChange={(e) => onFilter(f.id, e.target.value)}>
                {DATE_CHOICES.map((d) => (
                  <option key={d.value} value={d.value}>
                    {d.words}
                  </option>
                ))}
              </select>
            );
          case "mine":
            return (
              <label key={f.id} className="switch-line">
                <input type="checkbox" checked={v === "on"} onChange={(e) => onFilter(f.id, e.target.checked ? "on" : "")} /> <span>{f.label}</span>
              </label>
            );
        }
        return null;
      })}
    </div>
  );
}

function Figure({ part }: { part: DrawnPart }) {
  if (part.error) return <div className="stat" data-testid="page-figure"><span className="stat-label">{part.label}</span><span className="stat-hint">{part.error}</span></div>;
  return (
    <div className={`stat pg-figure ${toneClass(part.tone)}`} data-testid="page-figure">
      <span className="stat-label">{part.label}</span>
      <span className="stat-value">{part.shown}</span>
      {part.change && <span className="stat-hint">{part.change.words}</span>}
      {part.over && <span className="stat-hint">{part.over}</span>}
    </div>
  );
}

function Part({ part, name, draft, values, onOpen }: { part: DrawnPart; name: string; draft: boolean; values: Record<string, string>; onOpen: (r: DrawnRow) => void }) {
  if (part.broken) {
    return (
      <div className="warning" role="note" data-testid="page-broken">
        {part.broken}
      </div>
    );
  }
  switch (part.type) {
    case "heading":
      return (
        <section className="pg-heading">
          <h2>{part.title}</h2>
          {part.text && <p className="hint">{part.text}</p>}
        </section>
      );
    case "figure":
      // The page's main figure, on pine.
      return (
        <section className="hero" aria-label={part.label} data-testid="page-figure">
          <div className="hero-top">
            <span className="hero-label">{part.label}</span>
          </div>
          <div className="hero-amount">{part.error ? "—" : part.shown}</div>
          {(part.change || part.error || part.over) && <p className="hero-sub">{part.error ?? part.change?.words ?? part.over}</p>}
        </section>
      );
  }
  return <Rows part={part} name={name} draft={draft} values={values} onOpen={onOpen} />;
}

/** A list or a table, with More and (a table) its order by a press on a column. */
function Rows({ part, name, draft, values, onOpen }: { part: DrawnPart; name: string; draft: boolean; values: Record<string, string>; onOpen: (r: DrawnRow) => void }) {
  const [shown, setShown] = useState<DrawnPart>(part);
  const act = useAction();
  const fetchPart = (extra: string) => {
    const q = [filtersParam(values), draft ? "draft=1" : "", extra].filter(Boolean).join("&");
    return get<DrawnPart>(`/v1/pages/${name}/blocks/${part.id}?${q}`);
  };
  const more = () =>
    void act.run(async () => {
      const ord = shown.order ? `order=${shown.order.column}${shown.order.desc ? "&desc=1" : ""}&` : "";
      const next = await fetchPart(`${ord}cursor=${encodeURIComponent(shown.more ?? "")}`);
      setShown({ ...next, rows: [...(shown.rows ?? []), ...(next.rows ?? [])], groups: mergeGroups(shown, next) });
    }, false);
  const sort = (column: string) =>
    void act.run(async () => {
      const desc = shown.order?.column === column ? !shown.order.desc : false;
      setShown(await fetchPart(`order=${column}${desc ? "&desc=1" : ""}`));
    }, false);
  const rows = shown.rows ?? [];
  return (
    <section className={`card pg-part${shown.size === "half" ? " half" : ""}`} data-testid="page-part">
      <div className="pg-part-head">
        {shown.title && <h2>{shown.title}</h2>}
        {!shown.error && <span className="hint">{rowsWord(shown.count ?? 0)}</span>}
      </div>
      {shown.error ? (
        <p className="hint">{shown.error}</p>
      ) : rows.length === 0 ? (
        <p className="hint">{shown.empty}</p>
      ) : shown.type === "list" ? (
        grouped(shown).map((g) => (
          <div key={g.key} className="pg-group">
            {g.label && (
              <h3>
                {g.label} <span className="hint">({g.rows.length})</span>
              </h3>
            )}
            <Items label={g.label || shown.title || "Rows"}>
              {g.rows.map((r) => (
                <li key={r.id} data-testid="page-row">
                  <button type="button" className="item pg-row" disabled={!r.detail} onClick={() => onOpen(r)}>
                    <span className="grow">
                      <span className="item-top">
                        <span className="title">{r.title || "—"}</span>
                        {r.state && <span className={`badge sf-status ${toneClass(r.state.tone)}`}>{r.state.text}</span>}
                      </span>
                      {r.sub && <span className="sub">{r.sub}</span>}
                    </span>
                    {r.detail && <Icon name="chevron" className="go" />}
                  </button>
                </li>
              ))}
            </Items>
          </div>
        ))
      ) : (
        <table className="cards pg-table">
          <thead>
            <tr>
              {(shown.columns ?? []).map((c) => (
                <th key={c.column} aria-sort={shown.order?.column === c.column ? (shown.order.desc ? "descending" : "ascending") : undefined}>
                  <button type="button" className="link" onClick={() => sort(c.column)}>
                    {c.label}
                  </button>
                </th>
              ))}
              {shown.has_detail && (
                <th>
                  <span className="sr-only">Open</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} data-testid="page-row">
                {(r.cells ?? []).map((cell, i) => (
                  <td key={i} data-label={shown.columns?.[i]?.label}>
                    {cell || "—"}
                  </td>
                ))}
                {shown.has_detail && (
                  <td>
                    <button type="button" className="link" onClick={() => onOpen(r)}>
                      Open<span className="sr-only"> {r.cells?.[0]}</span>
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      <ErrorBox error={act.error} />
      {shown.more && (
        <button disabled={act.busy} onClick={more}>
          More
        </button>
      )}
    </section>
  );
}

function mergeGroups(a: DrawnPart, b: DrawnPart): DrawnPart["groups"] {
  if (!a.groups && !b.groups) return undefined;
  const out = [...(a.groups ?? [])];
  for (const g of b.groups ?? []) if (!out.some((x) => x.key === g.key)) out.push(g);
  return out;
}
