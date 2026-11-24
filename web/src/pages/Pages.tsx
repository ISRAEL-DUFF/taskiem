import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router-dom";
import { ApiError, get, patch, post, type Approval } from "../api";
import { Icon } from "../icons";
import { Sheet } from "../kit";
import { toast } from "../toast";
import { EmptyState, ErrorBox, Field, Items, Item, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Decide } from "./Approvals";
import { useConfirm } from "./ConfirmSheet";
import { MadeToolSheet } from "./MadeToolSheet";
import { DescribePageModal, StartersModal } from "./PageStarters";
import { Board, Chart, type Move } from "./PageG4";
import {
  stillWorking,
  workedWords,
  type ActResult,
  type DrawnAction,
  DATE_CHOICES,
  canSort,
  importWords,
  readPageFile,
  filtersParam,
  keptFilters,
  grouped,
  layout,
  liveQueue,
  type PageChanged,
  rowsWord,
  EMPTY,
  isRecords,
  newPageDoc,
  recordTables,
  withUses,
  type RecordView,
  linkFilters,
  runStatusLine,
  toneClass,
  viewWithFilters,
  type Tone,
  whoWords,
  type DrawnFilter,
  type DrawnPage,
  type DrawnPart,
  type DrawnRow,
  type PageDetail,
  type PageImported,
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
  const [starting, setStarting] = useState(false);
  const [describing, setDescribing] = useState(false);
  const data = list.data;
  return (
    <>
      <PageHeader
        title="Pages"
        description="Screens your business makes for itself from its tables: figures, lists and tables, for a phone first."
        actions={
          data?.can_edit && (
            <>
              <button className="primary" onClick={() => setMaking(true)}>
                <Icon name="plus" />
                New page
              </button>
              <button onClick={() => setStarting(true)}>
                <Icon name="dashboard" />
                From a starter
              </button>
              <button onClick={() => setDescribing(true)}>
                <Icon name="sparkle" />
                Describe it
              </button>
              <FromFile />
            </>
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
      {starting && <StartersModal onClose={() => setStarting(false)} />}
      {describing && <DescribePageModal onClose={() => setDescribing(false)} />}
    </>
  );
}

/** Makes a page from a file another page was saved as (Save as a file in
 * the editor): a draft, opened in the editor with what this business lacks
 * of what it shows. */
function FromFile() {
  const act = useAction();
  const navigate = useNavigate();
  const [said, setSaid] = useState<string | null>(null);
  return (
    <>
      <label className="button">
        <Icon name="file" />
        Make from a file
        <input
          type="file"
          className="sr-only"
          accept=".json,application/json"
          disabled={act.busy}
          onChange={(e) => {
            const file = e.target.files?.[0];
            e.target.value = "";
            if (!file) return;
            setSaid(null);
            void act.run(async () => {
              const read = readPageFile(await file.text());
              if ("error" in read) {
                setSaid(read.error);
                return;
              }
              const made = await post<PageImported>("/v1/pages/import", { document: read.document });
              navigate(`/pages/${made.page.name}/edit`, { state: { imported: importWords(made) } });
            }, false);
          }}
        />
      </label>
      {said && (
        <div className="warning" role="alert">
          {said}
        </div>
      )}
      <ErrorBox error={act.error} />
    </>
  );
}

/** Picks what a new page shows and makes it: one of the business's tables
 * (Show as a page), Taskiem's own records, or nothing yet, to add every
 * part in the editor (H5: a page made from the editor alone). */
function NewPage({ onClose }: { onClose: () => void }) {
  const tables = useLoad(() => get<{ tables: Table[] }>("/v1/tables?counts=no"), []);
  const records = useLoad(() => get<{ records: RecordView[] }>("/v1/page-records").catch(() => ({ records: [] as RecordView[] })), []);
  const pages = useLoad(() => get<PageList>("/v1/pages"), []);
  const [table, setTable] = useState("");
  const [title, setTitle] = useState("");
  const act = useAction();
  const navigate = useNavigate();
  const list = (tables.data?.tables ?? []).filter((t) => !t.kept_by);
  const views = recordTables(records.data?.records ?? []);
  const picked = table || list[0]?.id || views[0]?.id || EMPTY;
  const own = picked === EMPTY || isRecords(picked);
  return (
    <Modal title="New page" onClose={onClose}>
      {!!tables.error && <ErrorBox error={tables.error} />}
      {
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void act.run(async () => {
              if (!own) {
                const pg = await post<PageDetail>("/v1/pages", { from_table: picked });
                navigate(`/pages/${pg.name}/edit`);
                return;
              }
              const doc = newPageDoc(title, (pages.data?.pages ?? []).map((p) => p.name), views.find((v) => v.id === picked));
              const pg = await post<PageDetail>("/v1/pages", { document: withUses(doc, views) });
              navigate(`/pages/${pg.name}/edit`);
            }, false);
          }}
        >
          <Field label="What should it show?" hint="Taskiem starts it with how many there are and the rows as a list, or with nothing. You change it after.">
            <select value={picked} onChange={(e) => setTable(e.target.value)}>
              {list.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.title || t.name}
                </option>
              ))}
              {views.map((t) => (
                <option key={t.id} value={t.id}>
                  {t.title}
                </option>
              ))}
              <option value={EMPTY}>Nothing yet: I will add each part</option>
            </select>
          </Field>
          {own && (
            <Field label="Its name">
              <input value={title} maxLength={60} required onChange={(e) => setTitle(e.target.value)} />
            </Field>
          )}
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
      }
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
  // The page remembers a viewer's last filters on this device (pages §6);
  // a link's filters (a Tell me message's page button) come first.
  const [values, setValues] = useState<Record<string, string>>(() => linkFilters(search.get("filters")) ?? lastFilters(name));
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
  // Kept up to date (pages §9): the parts a notice names are read again,
  // in place. A figure from the page drawn again; a list or a table reads
  // its own first rows again, in the order it is in (More's pages go).
  const [fresh, setFresh] = useState<Record<string, DrawnPart>>({});
  const [ticks, setTicks] = useState<Record<string, number>>({});
  useEffect(() => setFresh({}), [view.data]);
  const current = useRef({ q, draft, data: view.data, reload: view.reload });
  current.current = { q, draft, data: view.data, reload: view.reload };
  usePageLive(name, draft, async (ids, all) => {
    const { q: query, draft: d, data, reload } = current.current;
    if (all || !data) return reload();
    const rows = new Set(data.blocks.filter((b) => b.type === "list" || b.type === "table").map((b) => b.id));
    if (ids.some((id) => !rows.has(id))) {
      const all = [query, d ? "draft=1" : ""].filter(Boolean).join("&");
      const next = await get<DrawnPage>(`/v1/pages/${name}/view${all ? `?${all}` : ""}`);
      setFresh((f) => ({ ...f, ...Object.fromEntries(next.blocks.filter((b) => ids.includes(b.id) && !rows.has(b.id)).map((b) => [b.id, b])) }));
    }
    setTicks((t) => ({ ...t, ...Object.fromEntries(ids.filter((id) => rows.has(id)).map((id) => [id, (t[id] ?? 0) + 1])) }));
  });
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
  const page = { ...view.data, blocks: view.data.blocks.map((b) => fresh[b.id] ?? b) };
  return <DrawnPageView page={page} values={values} onFilter={set} onRefresh={view.reload} ticks={ticks} />;
}

/** A page over the chat (pages G3): its tile in the tools panel, or "/" and
 * its name. Drawn from the same view as the page, with its buttons. */
export function PageSheet({ name, onClose }: { name: string; onClose: () => void }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const q = filtersParam(values);
  const view = useLoad(() => get<DrawnPage>(`/v1/pages/${name}/view${q ? `?${q}` : ""}`), [name, q]);
  return (
    <Sheet title={view.data?.page.title ?? `/${name}`} sub={`/${name}`} onClose={onClose} testid="page-sheet">
      <div className="tk-sheet-list">
        {view.error ? (
          <ErrorBox error={view.error} />
        ) : !view.data ? (
          <Skeleton rows={3} label="Drawing the page…" />
        ) : (
          <DrawnPageView page={view.data} values={values} onFilter={(id, v) => setValues({ ...values, [id]: v })} onRefresh={view.reload} sheet />
        )}
        <p className="hint">
          <Link to={`/pages/${name}`}>Open the whole page</Link>
        </p>
      </div>
    </Sheet>
  );
}

/** Follows a page's live notices (GET /v1/pages/{name}/live) while it is
 * open: the parts named are read (read), a burst at most once in two
 * seconds. Without a live connection (refused, or no EventSource) the page
 * is read on opening and on Refresh, as before. */
function usePageLive(name: string, draft: boolean, read: (ids: string[], all: boolean) => Promise<void> | void) {
  const reader = useRef(read);
  reader.current = read;
  useEffect(() => {
    if (!name || typeof EventSource === "undefined") return;
    const queue = liveQueue((ids, all) => reader.current(ids, all));
    let es: EventSource | undefined;
    let retry: number | undefined;
    let stopped = false;
    const open = () => {
      es = new EventSource(`/v1/pages/${name}/live${draft ? "?draft=1" : ""}`);
      es.addEventListener("changed", (ev) => queue.add(JSON.parse((ev as MessageEvent<string>).data) as PageChanged));
      es.addEventListener("gone", () => es?.close());
      es.onerror = () => {
        // Refused (too many open) or gone: tried again later.
        if (es?.readyState === EventSource.CLOSED && !stopped) {
          es.close();
          retry = window.setTimeout(open, 30000);
        }
      };
    };
    open();
    return () => {
      stopped = true;
      queue.stop();
      es?.close();
      window.clearTimeout(retry);
    };
  }, [name, draft]);
}

/** A page as drawn (the page itself, and the editor's preview). */
export function DrawnPageView({
  page,
  values,
  onFilter,
  onRefresh,
  preview = false,
  ticks = {},
  sheet = false,
}: {
  page: DrawnPage;
  values: Record<string, string>;
  onFilter: (id: string, v: string) => void;
  onRefresh: () => void;
  preview?: boolean;
  /** A count per list or table, raised when it is to read its first rows again. */
  ticks?: Record<string, number>;
  /** Drawn in a sheet over the chat (pages G3): the sheet has the title. */
  sheet?: boolean;
}) {
  const [open, setOpen] = useState<(DrawnRow & { part: string }) | null>(null);
  const name = page.page.name;
  const Heading = preview ? "h2" : "h1";
  const press = usePagePress(name, page.draft, values, onRefresh);
  // A part a tool is still working out is read again in a moment.
  const working = stillWorking(page);
  useEffect(() => {
    if (!working || preview) return;
    const t = window.setTimeout(onRefresh, 3000);
    return () => window.clearTimeout(t);
  }, [working, page.drawn_at]);
  const refresh = () =>
    // Refresh works out again what tools worked out (kept a minute otherwise).
    void (page.blocks.some((b) => b.from_tool) ? post(`/v1/pages/${name}/refresh`).catch(() => undefined) : Promise.resolve()).then(onRefresh);
  return (
    <div className="pg" data-testid={preview ? "page-preview" : "page"}>
      {!preview && !sheet && (
        <p className="crumbs">
          <Link to="/pages" aria-label="Back to Pages">
            Pages
          </Link>
        </p>
      )}
      <div className="page-header">
        <div className="page-heading">{!sheet && <Heading>{page.page.title}</Heading>}</div>
        {!preview && (
          <div className="page-actions">
            <button onClick={refresh}>
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
          <Part
            key={`${p.id}:${page.drawn_at}:${filtersParam(values)}`}
            part={p}
            name={name}
            draft={page.draft}
            values={values}
            onOpen={(r) => setOpen({ ...r, part: p.id })}
            tick={ticks[p.id] ?? 0}
            onPress={preview ? undefined : press.press}
            onMove={preview ? undefined : press.move}
            onChanged={onRefresh}
          />
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
            {open.run_status && (
              <div data-testid="page-detail-run">
                <dt>Where it is</dt>
                <dd>
                  {runStatusLine(open.run_status)}
                  {open.run_status.started_at && <span className="hint"> · started {fmtTime(open.run_status.started_at)}</span>}
                </dd>
              </div>
            )}
          </dl>
          <div className="toolbar">
            {!preview && (open.actions ?? []).map((a) => (
              <ActionButton key={a.index} a={a} onPress={() => (setOpen(null), void press.press(open.part, open.id, a))} />
            ))}
            {open.link && <Link to={open.link}>Open it</Link>}
            <button onClick={() => setOpen(null)}>Close</button>
          </div>
        </Modal>
      )}
      {press.node}
    </div>
  );
}

/** Pressing a page's button (POST /v1/pages/{name}/act): a tool's form
 * filled in, opened in its sheet; a signal after a Yes; an approval's
 * sheet. The server reads the row again and works the values out. */
function usePagePress(name: string, draft: boolean, values: Record<string, string>, reload: () => void) {
  const { confirm, node: asking } = useConfirm();
  const [tool, setTool] = useState<{ command: string; values: Record<string, unknown>; button: string } | null>(null);
  const [approval, setApproval] = useState<Approval | null>(null);
  const q = [filtersParam(values), draft ? "draft=1" : ""].filter(Boolean).join("&");
  const path = `/v1/pages/${name}/act${q ? `?${q}` : ""}`;
  const press = (block: string, row: string | undefined, a: DrawnAction) => pressBody(a, { block, action: a.index, ...(row !== undefined && { row }) });
  // A board's card moved to another column (G4): the board's move tool,
  // given filled in by the server as a row's button is.
  const move = (block: string, row: string, to: string) => pressBody({ index: 0, label: "Move", kind: "tool" }, { block, action: 0, row, to });
  const pressBody = async (a: DrawnAction, body: Record<string, unknown>) => {
    try {
      const r = await post<ActResult>(path, body);
      if (r.kind === "tool" && r.tool) {
        setTool({ command: r.tool.command, values: r.tool.inputs, button: r.tool.button });
      } else if (r.kind === "signal") {
        if (!(await confirm({ title: r.asks ?? `${a.label}?`, ok: `Yes, ${a.label.toLowerCase()}` }))) return;
        const sent = await post<ActResult>(path, { ...body, yes: true });
        toast(sent.words ?? "Done.");
        reload();
      } else if (r.kind === "approve" && r.approval) {
        setApproval(r.approval as Approval);
      }
    } catch (e) {
      toast(e instanceof ApiError ? e.message : "That did not work. Try again.", "error");
      if (e instanceof ApiError && e.status === 409) reload();
    }
  };
  const node = (
    <>
      {asking}
      {tool && (
        <MadeToolSheet
          command={tool.command}
          values={tool.values}
          button={tool.button}
          onClose={() => (setTool(null), reload())}
        />
      )}
      {approval && <Decide a={approval} decision="approved" showSubject onClose={() => setApproval(null)} onDone={() => (setApproval(null), reload())} />}
    </>
  );
  return { press, move, node };
}

/** A page's or a row's button: greyed, with why, when it is not this person's. */
function ActionButton({ a, onPress, small }: { a: DrawnAction; onPress: () => void; small?: boolean }) {
  return (
    <button
      type="button"
      className={small ? "small" : a.kind === "tool" ? "primary" : undefined}
      disabled={!!a.disabled}
      title={a.disabled}
      aria-description={a.disabled}
      data-testid="page-action"
      onClick={onPress}
    >
      {a.label}
    </button>
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
  if (part.working)
    return (
      <div className="stat" data-testid="page-figure" role="status">
        <span className="stat-label">{part.label}</span>
        <span className="stat-hint">{part.working}</span>
      </div>
    );
  if (part.unavailable)
    return (
      <div className="stat" data-testid="page-figure">
        <span className="stat-label">{part.label}</span>
        <span className="stat-hint" data-testid="page-unavailable">{part.unavailable}</span>
      </div>
    );
  if (part.error) return <div className="stat" data-testid="page-figure"><span className="stat-label">{part.label}</span><span className="stat-hint">{part.error}</span></div>;
  return (
    <div className={`stat pg-figure ${toneClass(part.tone)}`} data-testid="page-figure">
      <span className="stat-label">{part.label}</span>
      <span className="stat-value">{part.shown}</span>
      {part.change && <span className="stat-hint">{part.change.words}</span>}
      {part.over && <span className="stat-hint">{part.over}</span>}
      {part.worked_at && <span className="stat-hint">{workedWords(part.worked_at)}</span>}
    </div>
  );
}

type Press = (block: string, row: string | undefined, a: DrawnAction) => Promise<void>;

function Part({
  part,
  name,
  draft,
  values,
  onOpen,
  tick,
  onPress,
  onMove,
  onChanged,
}: {
  part: DrawnPart;
  name: string;
  draft: boolean;
  values: Record<string, string>;
  onOpen: (r: DrawnRow) => void;
  tick: number;
  onPress?: Press;
  onMove?: Move;
  onChanged: () => void;
}) {
  if (part.broken) {
    return (
      <div className="warning" role="note" data-testid="page-broken">
        {part.broken}
      </div>
    );
  }
  if (part.unavailable) {
    return (
      <section className="card pg-part" data-testid="page-part">
        {(part.title || part.label) && (
          <div className="pg-part-head">
            <h2>{part.title || part.label}</h2>
          </div>
        )}
        <p className="hint" data-testid="page-unavailable">
          {part.unavailable}
        </p>
      </section>
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
          <div className="hero-amount">{part.error || part.working ? "—" : part.shown}</div>
          {(part.change || part.error || part.over || part.working || part.worked_at) && (
            <p className="hero-sub">{part.error ?? part.working ?? part.change?.words ?? part.over ?? workedWords(part.worked_at)}</p>
          )}
        </section>
      );
    case "buttons":
      // The page's own buttons (pages §7).
      return (
        <section className="pg-buttons" aria-label={part.title || "Buttons"} data-testid="page-buttons">
          {part.title && <h2>{part.title}</h2>}
          <div className="toolbar">
            {(part.actions ?? []).map((a) => (
              <ActionButton key={a.index} a={a} onPress={() => void onPress?.(part.id, undefined, a)} />
            ))}
          </div>
          {(part.actions ?? []).filter((a) => a.disabled).map((a) => (
            <p key={a.index} className="hint">
              {a.label}: {a.disabled}
            </p>
          ))}
        </section>
      );
    case "status":
      // A run's state in plain words (pages §4).
      return (
        <section className="card pg-part" data-testid="page-status">
          <div className="pg-part-head">
            <h2>{part.title}</h2>
            {part.words && part.status && <span className={`badge sf-status ${toneClass(part.tone)}`}>{part.words}</span>}
          </div>
          {part.error ? (
            <p className="hint">{part.error}</p>
          ) : !part.status ? (
            <p className="hint">{part.words}</p>
          ) : (
            <>
              {part.started_at && <p className="hint">Started {fmtTime(part.started_at)}{part.ended_at && `, ended ${fmtTime(part.ended_at)}`}</p>}
              {(part.waiting_for ?? []).map((wf, i) => (
                <p key={i}>Waiting for {wf.who}</p>
              ))}
            </>
          )}
        </section>
      );
    case "board":
      return <Board part={part} name={name} draft={draft} values={values} onOpen={onOpen} onPress={onPress} onMove={onMove} />;
    case "chart":
      return <Chart part={part} />;
  }
  return <Rows part={part} name={name} draft={draft} values={values} onOpen={onOpen} tick={tick} onPress={onPress} onChanged={onChanged} />;
}

/** A list or a table, with More and (a table) its order by a press on a column. */
function Rows({
  part,
  name,
  draft,
  values,
  onOpen,
  tick,
  onPress,
  onChanged,
}: {
  part: DrawnPart;
  name: string;
  draft: boolean;
  values: Record<string, string>;
  onOpen: (r: DrawnRow) => void;
  tick: number;
  onPress?: Press;
  onChanged: () => void;
}) {
  const [shown, setShown] = useState<DrawnPart>(part);
  const hasActions = (shown.rows ?? []).some((r) => (r.actions ?? []).length > 0);
  const rowActions = (r: DrawnRow) =>
    onPress && (r.actions ?? []).length > 0 ? (
      <span className="pg-row-actions">
        {(r.actions ?? []).map((a) => (
          <ActionButton key={a.index} a={a} small onPress={() => void onPress(part.id, r.id, a)} />
        ))}
      </span>
    ) : null;
  // A yes/no column ticked in place (table.edit): the row changed in its table.
  const setTick = (r: DrawnRow, i: number, v: boolean) =>
    void act.run(async () => {
      const col = shown.columns?.[i]?.column;
      if (!col || !shown.table_id) return;
      await patch(`/v1/tables/${shown.table_id}/rows/${r.id}`, { set: { [col]: v } });
      onChanged();
    }, false);
  const act = useAction();
  const fetchPart = (extra: string) => {
    const q = [filtersParam(values), draft ? "draft=1" : "", extra].filter(Boolean).join("&");
    return get<DrawnPart>(`/v1/pages/${name}/blocks/${part.id}?${q}`);
  };
  // A live notice: its first rows read again, in the order it is in.
  const order = useRef(shown.order);
  order.current = shown.order;
  useEffect(() => {
    if (tick === 0) return;
    const o = order.current;
    fetchPart(o ? `order=${o.column}${o.desc ? "&desc=1" : ""}` : "")
      .then(setShown)
      .catch(() => undefined);
  }, [tick]);
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
        {!shown.error && !shown.working && <span className="hint">{rowsWord(shown.count ?? 0)}</span>}
      </div>
      {shown.error ? (
        <p className="hint">{shown.error}</p>
      ) : shown.working ? (
        <p className="hint" role="status">{shown.working}</p>
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
                  {r.link && !r.detail ? (
                    <Link className="item pg-row" to={r.link}>
                      <RowWords r={r} />
                      <Icon name="chevron" className="go" />
                    </Link>
                  ) : (
                    <button type="button" className="item pg-row" disabled={!r.detail} onClick={() => onOpen(r)}>
                      <RowWords r={r} />
                      {r.detail && <Icon name="chevron" className="go" />}
                    </button>
                  )}
                  {rowActions(r)}
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
                  {canSort(c) ? (
                    <button type="button" className="link" onClick={() => sort(c.column)}>
                      {c.label}
                    </button>
                  ) : (
                    c.label
                  )}
                </th>
              ))}
              {(shown.has_detail || rows.some((r) => r.link)) && (
                <th>
                  <span className="sr-only">Open</span>
                </th>
              )}
              {hasActions && onPress && (
                <th>
                  <span className="sr-only">Buttons</span>
                </th>
              )}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.id} data-testid="page-row">
                {(r.cells ?? []).map((cell, i) => (
                  <td key={i} data-label={shown.columns?.[i]?.label}>
                    {r.ticks?.[i] != null ? (
                      <input
                        type="checkbox"
                        checked={!!r.ticks[i]}
                        disabled={act.busy}
                        aria-label={`${shown.columns?.[i]?.label ?? ""}: ${r.cells?.[0] ?? ""}`}
                        data-testid="page-tick"
                        onChange={(e) => setTick(r, i, e.target.checked)}
                      />
                    ) : (
                      cell || "—"
                    )}
                    {i === 0 && <RowRun r={r} />}
                  </td>
                ))}
                {shown.has_detail ? (
                  <td>
                    <button type="button" className="link" onClick={() => onOpen(r)}>
                      Open<span className="sr-only"> {r.cells?.[0]}</span>
                    </button>
                  </td>
                ) : (
                  rows.some((x) => x.link) && (
                    <td>
                      {r.link && (
                        <Link to={r.link}>
                          Open<span className="sr-only"> {r.cells?.[0]}</span>
                        </Link>
                      )}
                    </td>
                  )
                )}
                {hasActions && onPress && <td data-label="Buttons">{rowActions(r)}</td>}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {shown.worked_at && <p className="hint">{workedWords(shown.worked_at)}</p>}
      <ErrorBox error={act.error} />
      {shown.more && (
        <button disabled={act.busy} onClick={more}>
          More
        </button>
      )}
    </section>
  );
}

function RowWords({ r }: { r: DrawnRow }) {
  return (
    <span className="grow">
      <span className="item-top">
        <span className="title">{r.title || "—"}</span>
        {r.state && <span className={`badge sf-status ${toneClass(r.state.tone)}`}>{r.state.text}</span>}
      </span>
      {r.sub && <span className="sub">{r.sub}</span>}
      <RowRun r={r} />
    </span>
  );
}

/** A row's own status (pages §4): where its run is, in words. */
function RowRun({ r }: { r: DrawnRow }) {
  const s = r.run_status;
  if (!s) return null;
  return (
    <span className="sub pg-row-run" data-testid="page-row-run">
      <span className={`badge sf-status ${toneClass(s.tone as Tone)}`}>{runStatusLine(s)}</span>
    </span>
  );
}

function mergeGroups(a: DrawnPart, b: DrawnPart): DrawnPart["groups"] {
  if (!a.groups && !b.groups) return undefined;
  const out = [...(a.groups ?? [])];
  for (const g of b.groups ?? []) if (!out.some((x) => x.key === g.key)) out.push(g);
  return out;
}
