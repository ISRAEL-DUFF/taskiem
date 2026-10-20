import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { currentAgentSession, del, get, patch, post, put, upload } from "../api";
import { useAuth } from "../auth";
import { Help } from "../onboarding";
import { Badge, EmptyState, ErrorBox, Field, Modal, PageHeader, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { Icon } from "../icons";
import {
  COLUMN_TYPES,
  TABLE_ACTIONS,
  TYPE_HELP,
  cellText,
  cellValue,
  columnErrors,
  exampleStep,
  importError,
  nameError,
  newColumn,
  parseCSV,
  personalByDefault,
  type Column,
  type Row,
  type Table,
  type TablesStatus,
} from "../lib/tables";

interface Conn {
  id: string;
  environment: string;
  connector: string;
  name: string;
  provisioned_by: string | null;
}

/** Data tables: the organisation's records, kept in its Xenibase project
 * and reached by workflows through the data_tables connection. */
export function Tables() {
  const { can } = useAuth();
  const list = useLoad(() => get<{ tables: Table[] }>("/v1/tables"), []);
  const status = useLoad(() => get<TablesStatus>("/v1/tables/status"), []);
  const [adding, setAdding] = useState(false);
  const edit = can("table.edit");
  const unavailable = status.data && !status.data.available;
  return (
    <>
      <PageHeader
        title="Tables"
        description="Records your business keeps (customers, debtors, staff, stock) that workflows read and write. No spreadsheet needed."
        actions={
          edit && (
            <button className="primary" disabled={!!unavailable} onClick={() => setAdding(true)}>
              <Icon name="plus" />
              New table
            </button>
          )
        }
      />
      <Help topic="tables" />
      {unavailable && (
        <div className="notice">
          Data tables are not available on this Taskiem: no Xenibase is configured. An owner can bring the organisation's own Xenibase project below.
        </div>
      )}
      <ErrorBox error={list.error} />
      {!list.data && !list.error && <Skeleton />}
      {list.data && list.data.tables.length === 0 && (
        <EmptyState
          icon="tables"
          action={
            edit &&
            !unavailable && (
              <button className="primary" onClick={() => setAdding(true)}>
                Create your first table
              </button>
            )
          }
        >
          A table holds rows of records, such as your debtors with their phone numbers and balances. Workflows read and write it like any Xenibase table.
        </EmptyState>
      )}
      {list.data && list.data.tables.length > 0 && (
        <table>
          <thead>
            <tr>
              <th>Table</th>
              <th>Columns</th>
              <th>Rows</th>
              <th>Changed</th>
            </tr>
          </thead>
          <tbody>
            {list.data.tables.map((t) => (
              <tr key={t.id}>
                <td>
                  <Link to={`/tables/${t.id}`}>{t.title || t.name}</Link>
                  <div className="hint">
                    <code>{t.name}</code>
                  </div>
                </td>
                <td>{t.columns.map((c) => c.name).join(", ")}</td>
                <td>{t.row_count === undefined ? <span className="hint">—</span> : t.row_count.toLocaleString()}</td>
                <td>{fmtTime(t.updated_at)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {adding && <NewTable onClose={() => setAdding(false)} onDone={() => (setAdding(false), list.reload(), status.reload())} />}
      {can("connection.manage") && status.data && <ProjectSettings status={status.data} tables={list.data?.tables.length ?? 0} onChange={() => (status.reload(), list.reload())} />}
    </>
  );
}

/** The columns builder, shared by the new-table form and the add-column form. */
function ColumnsEditor({ columns, onChange, errors }: { columns: Column[]; onChange: (c: Column[]) => void; errors: Map<number, string> }) {
  const set = (i: number, patchValue: Partial<Column>) => onChange(columns.map((c, j) => (j === i ? { ...c, ...patchValue } : c)));
  return (
    <div>
      {columns.map((c, i) => (
        <div key={i} className="card" style={{ marginBottom: 8, padding: 12 }}>
          <div className="row" style={{ alignItems: "flex-end", flexWrap: "wrap", gap: 8 }}>
            <Field label="Name" hint={errors.get(i)}>
              <input value={c.name} placeholder="balance_kobo" onChange={(e) => set(i, { name: e.target.value.trim() })} required />
            </Field>
            <Field label="Title">
              <input value={c.title} placeholder="Balance (kobo)" onChange={(e) => set(i, { title: e.target.value })} />
            </Field>
            <Field label="Type" hint={TYPE_HELP[c.type]}>
              <select value={c.type} onChange={(e) => set(i, { type: e.target.value as Column["type"], personal: personalByDefault(e.target.value as Column["type"]) })}>
                {COLUMN_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </Field>
            <label className="hint" style={{ display: "flex", gap: 4, alignItems: "center" }}>
              <input type="checkbox" checked={c.required} onChange={(e) => set(i, { required: e.target.checked })} /> required
            </label>
            <label className="hint" style={{ display: "flex", gap: 4, alignItems: "center" }}>
              <input type="checkbox" checked={c.personal} onChange={(e) => set(i, { personal: e.target.checked })} /> personal data
            </label>
            {columns.length > 1 && (
              <button type="button" className="icon-button" aria-label={`Remove column ${c.name || i + 1}`} onClick={() => onChange(columns.filter((_, j) => j !== i))}>
                <Icon name="close" />
              </button>
            )}
          </div>
        </div>
      ))}
      <button type="button" onClick={() => onChange([...columns, newColumn()])}>
        <Icon name="plus" />
        Add a column
      </button>
    </div>
  );
}

function NewTable({ onClose, onDone }: { onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState("");
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [columns, setColumns] = useState<Column[]>([{ ...newColumn(), name: "name", title: "Name", required: true }]);
  const act = useAction();
  const problems = columnErrors(columns);
  const errors = new Map(problems.filter((p) => p.index >= 0).map((p) => [p.index, p.message]));
  const nameBad = name ? nameError(name) : null;
  const ok = !nameBad && name !== "" && problems.length === 0;
  return (
    <Modal title="New table" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!ok) return;
          void act.run(async () => (await post("/v1/tables", { name, title, description, columns }), onDone()));
        }}
      >
        <p className="hint">The first table makes your organisation's data project, which takes a minute or two. Every table gets an id, created_at and updated_at.</p>
        <div className="row">
          <Field label="Name" hint={nameBad ?? "Lower-case letters, digits and underscores, e.g. debtors"}>
            <input value={name} onChange={(e) => setName(e.target.value.trim())} required autoFocus />
          </Field>
          <Field label="Title">
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Debtors" />
          </Field>
        </div>
        <Field label="Description">
          <input value={description} onChange={(e) => setDescription(e.target.value)} />
        </Field>
        <h3>Columns</h3>
        <ColumnsEditor columns={columns} onChange={setColumns} errors={errors} />
        {problems.filter((p) => p.index < 0).map((p) => (
          <div key={p.message} className="error-text">
            {p.message}
          </div>
        ))}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !ok}>
            {act.busy ? "Creating…" : "Create table"}
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** Settings > Data tables: the project's state and "use my own Xenibase project". */
function ProjectSettings({ status, tables, onChange }: { status: TablesStatus; tables: number; onChange: () => void }) {
  const conns = useLoad(() => get<{ connections: Conn[] }>("/v1/connections"), []);
  const [chosen, setChosen] = useState("");
  const act = useAction();
  const own = conns.data?.connections.filter((c) => c.connector === "xenibase" && c.environment === "prod" && !c.provisioned_by) ?? [];
  return (
    <section className="card" style={{ marginTop: 24 }}>
      <h2>Data project</h2>
      <p className="hint">
        Tables live in a Xenibase project, one per organisation. Taskiem makes it on the first table in the platform's Xenibase and keeps its connection, named <code>data_tables</code>, up to date. You may bring your own project instead, before any table exists.
      </p>
      <dl className="kv">
        <dt>State</dt>
        <dd>
          <Badge value={status.status === "ready" ? "ok" : status.status} /> {status.own ? "your own project" : status.status === "none" ? "made on the first table" : "provisioned by Taskiem"}
        </dd>
        {status.project_ref && (
          <>
            <dt>Project</dt>
            <dd>
              <code>{status.project_ref}</code> {status.api_url && <span className="hint">{status.api_url}</span>}
            </dd>
          </>
        )}
        {status.token_expires_at && (
          <>
            <dt>Token expires</dt>
            <dd>
              {fmtTime(status.token_expires_at)} {!status.own && <span className="hint">(renewed by Taskiem two weeks before)</span>}
            </dd>
          </>
        )}
        {status.error && (
          <>
            <dt>Last error</dt>
            <dd className="error-text">{status.error}</dd>
          </>
        )}
      </dl>
      {tables === 0 && (
        <form
          className="row"
          style={{ alignItems: "flex-end", marginTop: 12 }}
          onSubmit={(e) => {
            e.preventDefault();
            if (!chosen) return;
            void act.run(async () => (await put("/v1/tables/project", { connection_id: chosen }), onChange()));
          }}
        >
          <Field label="Use my own Xenibase project" hint="A xenibase connection in prod with the project's secret key and data API URL; it is tested first.">
            <select value={chosen} onChange={(e) => setChosen(e.target.value)}>
              <option value="">Choose a connection…</option>
              {own.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
          </Field>
          <button className="primary" disabled={act.busy || !chosen}>
            Use this project
          </button>
        </form>
      )}
      <ErrorBox error={act.error} />
    </section>
  );
}

// --- one table ---

export function TablePage() {
  const { id = "" } = useParams();
  const { can } = useAuth();
  const navigate = useNavigate();
  const table = useLoad(() => get<Table>(`/v1/tables/${id}`), [id]);
  const [adding, setAdding] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const edit = can("table.edit");
  if (table.error) return <ErrorBox error={table.error} />;
  if (!table.data) return <Skeleton />;
  const t = table.data;
  return (
    <>
      <PageHeader
        title={t.title || t.name}
        description={
          <>
            <code>{t.name}</code>
            {t.description ? ` · ${t.description}` : ""}
            {t.row_count !== undefined ? ` · about ${t.row_count.toLocaleString()} rows` : ""}
          </>
        }
        actions={
          <>
            <Link to="/tables">All tables</Link>
            {edit && (
              <button onClick={() => setAdding(true)}>
                <Icon name="plus" />
                Add a column
              </button>
            )}
            {edit && (
              <button onClick={() => setDeleting(true)} className="danger">
                Delete table
              </button>
            )}
          </>
        }
      />
      <Rows table={t} edit={edit} onChanged={table.reload} />
      <section className="card" style={{ marginTop: 24 }}>
        <h2>Columns</h2>
        <table>
          <thead>
            <tr>
              <th>Column</th>
              <th>Type</th>
              <th>Required</th>
              <th>Personal</th>
              {edit && <th><span className="sr-only">Actions</span></th>}
            </tr>
          </thead>
          <tbody>
            {t.columns.map((c) => (
              <ColumnRow key={c.name} table={t} column={c} edit={edit} onChanged={table.reload} />
            ))}
          </tbody>
        </table>
      </section>
      <UseInWorkflow table={t} />
      {adding && <AddColumn table={t} onClose={() => setAdding(false)} onDone={() => (setAdding(false), table.reload())} />}
      {deleting && (
        <ConfirmDelete
          what={`the table ${t.name} and every row`}
          name={t.name}
          onClose={() => setDeleting(false)}
          run={async () => (await del(`/v1/tables/${t.id}`, { confirm: t.name }), navigate("/tables"))}
        />
      )}
    </>
  );
}

function ColumnRow({ table, column, edit, onChanged }: { table: Table; column: Column; edit: boolean; onChanged: () => void }) {
  const [dropping, setDropping] = useState(false);
  return (
    <tr>
      <td>
        <code>{column.name}</code>
        {column.title && <div className="hint">{column.title}</div>}
      </td>
      <td>{column.type}</td>
      <td>{column.required ? "yes" : ""}</td>
      <td>{column.personal ? "yes" : ""}</td>
      {edit && (
        <td>
          <button onClick={() => setDropping(true)}>Drop</button>
          {dropping && (
            <ConfirmDelete
              what={`the column ${column.name} and its data`}
              name={column.name}
              onClose={() => setDropping(false)}
              run={async () => (await del(`/v1/tables/${table.id}/columns/${column.name}`, { confirm: column.name }), setDropping(false), onChanged())}
            />
          )}
        </td>
      )}
    </tr>
  );
}

function AddColumn({ table, onClose, onDone }: { table: Table; onClose: () => void; onDone: () => void }) {
  const [column, setColumn] = useState<Column>(newColumn());
  const act = useAction();
  const bad = column.name ? (nameError(column.name) ?? (table.columns.some((c) => c.name === column.name) ? "the table already has this column" : null)) : null;
  return (
    <Modal title="Add a column" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (bad || !column.name) return;
          void act.run(async () => (await post(`/v1/tables/${table.id}/columns`, column), onDone()));
        }}
      >
        <p className="hint">A required column cannot be added once the table has rows.</p>
        <ColumnsEditor columns={[column]} onChange={(cs) => setColumn(cs[0] ?? newColumn())} errors={new Map(bad ? [[0, bad]] : [])} />
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy || !!bad || !column.name}>
            Add
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** A typed confirmation, as Xenibase's own editor asks for drops. */
function ConfirmDelete({ what, name, onClose, run }: { what: string; name: string; onClose: () => void; run: () => Promise<unknown> }) {
  const [typed, setTyped] = useState("");
  const act = useAction();
  return (
    <Modal title="Delete" onClose={onClose}>
      <p>
        This deletes {what}. There is no undo. Type <code>{name}</code> to confirm.
      </p>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (typed !== name) return;
          void act.run(run);
        }}
      >
        <Field label="Name">
          <input value={typed} onChange={(e) => setTyped(e.target.value)} autoFocus />
        </Field>
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary danger" disabled={act.busy || typed !== name}>
            Delete
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** The rows grid: pages, search, inline add, edit and delete, CSV import and export. */
function Rows({ table, edit, onChanged }: { table: Table; edit: boolean; onChanged: () => void }) {
  const [q, setQ] = useState("");
  const [search, setSearch] = useState("");
  const [cursors, setCursors] = useState<string[]>([]); // the cursors of the pages before this one
  const cursor = cursors[cursors.length - 1] ?? "";
  const page = useLoad(
    () => get<{ rows: Row[]; next_cursor: string | null }>(`/v1/tables/${table.id}/rows?limit=50${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}${search ? `&q=${encodeURIComponent(search)}` : ""}`),
    [table.id, cursor, search, table.updated_at],
  );
  const [editing, setEditing] = useState<Row | null>(null);
  const [adding, setAdding] = useState(false);
  const [file, setFile] = useState<File | null>(null);
  const [fileError, setFileError] = useState<string | null>(null);
  const act = useAction();
  const reload = () => (page.reload(), onChanged());
  const textColumns = table.columns.filter((c) => c.type === "text" || c.type === "phone" || c.type === "email");
  const header = ["id", ...table.columns.map((c) => c.name)];
  useEffect(() => {
    setFileError(null);
    if (!file) return;
    void file.text().then((text) => setFileError(importError(parseCSV(text).header, table.columns)));
  }, [file, table.columns]);
  return (
    <section className="card">
      <div className="toolbar" style={{ flexWrap: "wrap", gap: 8 }}>
        <form
          className="row"
          onSubmit={(e) => {
            e.preventDefault();
            setCursors([]);
            setSearch(q);
          }}
        >
          <input type="search" value={q} placeholder={textColumns.length > 0 ? `Search ${textColumns[0]?.name} (or column:text)` : "Search column:text"} onChange={(e) => setQ(e.target.value)} aria-label="Search rows" />
          <button>Search</button>
        </form>
        {edit && (
          <button className="primary" onClick={() => setAdding(true)}>
            <Icon name="plus" />
            Add a row
          </button>
        )}
        {/* Agents may not export a business's table (docs/agents.md). */}
        {!currentAgentSession() && (
          <a className="button" href={`/v1/tables/${table.id}/export`} download={`${table.name}.csv`}>
            Export CSV
          </a>
        )}
        {edit && (
          <form
            className="row"
            onSubmit={(e) => {
              e.preventDefault();
              if (!file || fileError) return;
              const form = new FormData();
              form.append("file", file);
              void act.run(async () => {
                const out = await upload<{ imported: number; rows: number }>(`/v1/tables/${table.id}/import`, form);
                setFile(null);
                (e.target as HTMLFormElement).reset();
                reload();
                return out;
              });
            }}
          >
            <Field label="Import CSV" hint={fileError ?? "A header row naming the columns; up to 5,000 rows"}>
              <input type="file" accept=".csv,text/csv" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
            </Field>
            <button disabled={act.busy || !file || !!fileError}>Import</button>
          </form>
        )}
      </div>
      <ErrorBox error={page.error ?? act.error} />
      {!page.data && !page.error && <Skeleton />}
      {page.data && page.data.rows.length === 0 && <div className="empty">{search ? "No rows match." : "No rows yet."}</div>}
      {page.data && page.data.rows.length > 0 && (
        <table>
          <thead>
            <tr>
              {header.map((h) => (
                <th key={h}>{h}</th>
              ))}
              {edit && <th><span className="sr-only">Actions</span></th>}
            </tr>
          </thead>
          <tbody>
            {page.data.rows.map((r) => (
              <tr key={String(r.id)}>
                {header.map((h) => (
                  <td key={h}>{cellText(r[h])}</td>
                ))}
                {edit && (
                  <td style={{ whiteSpace: "nowrap" }}>
                    <button onClick={() => setEditing(r)}>Edit</button>{" "}
                    <button
                      disabled={act.busy}
                      onClick={() => {
                        if (!window.confirm(`Delete row ${String(r.id)}?`)) return;
                        void act.run(async () => (await del(`/v1/tables/${table.id}/rows/${String(r.id)}`), reload()));
                      }}
                    >
                      Delete
                    </button>
                  </td>
                )}
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {page.data && (cursors.length > 0 || page.data.next_cursor) && (
        <div className="toolbar">
          <button disabled={cursors.length === 0} onClick={() => setCursors(cursors.slice(0, -1))}>
            Previous
          </button>
          <button disabled={!page.data.next_cursor} onClick={() => page.data?.next_cursor && setCursors([...cursors, page.data.next_cursor])}>
            Next
          </button>
        </div>
      )}
      {(adding || editing) && (
        <RowForm
          table={table}
          row={editing}
          onClose={() => (setAdding(false), setEditing(null))}
          onDone={() => (setAdding(false), setEditing(null), reload())}
        />
      )}
    </section>
  );
}

/** Add or edit one row; values typed as text and turned into the column's type. */
function RowForm({ table, row, onClose, onDone }: { table: Table; row: Row | null; onClose: () => void; onDone: () => void }) {
  const [values, setValues] = useState<Record<string, string>>(Object.fromEntries(table.columns.map((c) => [c.name, row ? cellText(row[c.name]) : ""])));
  const act = useAction();
  return (
    <Modal title={row ? `Edit row ${String(row.id)}` : "Add a row"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const body = Object.fromEntries(table.columns.map((c) => [c.name, cellValue(c.type, values[c.name] ?? "")]));
          void act.run(async () => {
            if (row) await patch(`/v1/tables/${table.id}/rows/${String(row.id)}`, { set: body });
            else await post(`/v1/tables/${table.id}/rows`, { row: body });
            onDone();
          });
        }}
      >
        {table.columns.map((c) => (
          <Field key={c.name} label={(c.title || c.name) + (c.required ? " *" : "")} hint={TYPE_HELP[c.type]}>
            {c.type === "boolean" ? (
              <select value={values[c.name] ?? ""} onChange={(e) => setValues({ ...values, [c.name]: e.target.value })}>
                <option value="">—</option>
                <option value="true">yes</option>
                <option value="false">no</option>
              </select>
            ) : (
              <input
                type={c.type === "date" ? "date" : c.type === "email" ? "email" : c.type === "phone" ? "tel" : "text"}
                value={values[c.name] ?? ""}
                required={c.required}
                onChange={(e) => setValues({ ...values, [c.name]: e.target.value })}
              />
            )}
          </Field>
        ))}
        <ErrorBox error={act.error} />
        <div className="toolbar">
          <button type="submit" className="primary" disabled={act.busy}>
            Save
          </button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** How a workflow reaches the table. */
function UseInWorkflow({ table }: { table: Table }) {
  return (
    <section className="card" style={{ marginTop: 24 }}>
      <h2>Use in a workflow</h2>
      <p className="hint">
        Workflows reach this table like any Xenibase table: connector <code>xenibase@1</code>, connection <code>{table.connection}</code>, table <code>{table.name}</code>. The AI builder knows its columns.
      </p>
      <dl className="kv">
        {TABLE_ACTIONS.map((a) => (
          <div key={a.action}>
            <dt>
              <code>{a.action}</code>
            </dt>
            <dd>{a.what}</dd>
          </div>
        ))}
      </dl>
      <pre>{exampleStep(table)}</pre>
    </section>
  );
}
