import { SavedSourceForm } from "./PageEditorG5";
import { useEffect, useState } from "react";
import { Link, useLocation, useNavigate, useParams } from "react-router-dom";
import { del, get, post, put } from "../api";
import type { ApproverRole } from "../forms/RolePicker";
import { Icon } from "../icons";
import { ErrorBox, Field, Items, Modal, PageHeader, Row, Skeleton, fmtTime, useAction, useLoad } from "../ui";
import { saveFile } from "../lib/saveFile";
import {
  PART_KINDS,
  conditionCount,
  pageFileName,
  versionTitle,
  columnOf,
  columnWords,
  columnsFor,
  freeId,
  move,
  newPart,
  opsFor,
  partName,
  partWords,
  problemsOf,
  publishWords,
  showColumn,
  whoWords,
  withUses,
  editorDoc,
  recordTables,
  runsWords,
  NO_TABLE_KINDS,
  OWN_TABLE_KINDS,
  feedingTools,
  isRecords,
  type PageChoices,
  type RecordView,
  type DrawnPage,
  type PageDetail,
  type PageDoc,
  type PageFilterDef,
  type PageVersion,
  type Part,
  type Tone,
} from "../lib/pages";
import type { Table } from "../lib/tables";
import { DrawnPageView } from "./Pages";
import { ActionsForm, PlaceForm, RowStatusForm, StatusForm, TitlePick, ToolSourceForm } from "./PageEditorParts";
import { BoardForm, ChartForm } from "./PageEditorG4";

// The page editor (pages §10, 5): no technical words on the way. An owner
// adds parts (a count, a total, a list, a table, a heading; and, pages H5,
// a figure or a list a tool works out, buttons, where a run is), picks the
// table each shows and what its rows show, moves them up and down, picks
// who may open the page, and sees it drawn at phone width beside them.
// Changes are a draft until published.

const TONES: { tone: Tone; words: string }[] = [
  { tone: "plain", words: "Plain" },
  { tone: "good", words: "Green (good)" },
  { tone: "warn", words: "Amber (needs a look)" },
  { tone: "bad", words: "Red (a problem)" },
];

export function PageEditor() {
  const { name = "" } = useParams();
  const navigate = useNavigate();
  const detail = useLoad(() => get<PageDetail>(`/v1/pages/${name}`), [name]);
  const tables = useLoad(() => get<{ tables: Table[] }>("/v1/tables?counts=no"), []);
  // Taskiem's own orders and invoices, offered next to the tables.
  const records = useLoad(() => get<{ records: RecordView[] }>("/v1/page-records").catch(() => ({ records: [] as RecordView[] })), []);
  // The business's tools and workflows a part or a button may use (H5).
  const choices = useLoad(() => get<PageChoices>("/v1/page-choices").catch(() => ({ tools: [], workflows: [] }) as PageChoices), []);
  const roles = useLoad(() => get<{ roles: ApproverRole[] }>("/v1/approver-roles?for=start").catch(() => ({ roles: [] as ApproverRole[] })), []);
  const list = useLoad(() => get<{ can_publish: boolean; pages: { name: string; pinned: boolean }[] }>("/v1/pages"), []);
  const [doc, setDoc] = useState<PageDoc | null>(null);
  const [dirty, setDirty] = useState(false);
  const [editing, setEditing] = useState<number | null>(null);
  const [adding, setAdding] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [preview, setPreview] = useState<DrawnPage | null>(null);
  const [previewKey, setPreviewKey] = useState(0);
  const [exported, setExported] = useState<string | null>(null);
  const imported = (useLocation().state as { imported?: string[] } | null)?.imported ?? [];
  const versions = useLoad(() => get<{ versions: PageVersion[] }>(`/v1/pages/${name}/versions`), [name, previewKey]);
  const act = useAction();
  useEffect(() => {
    const d = detail.data;
    if (d && !doc) setDoc(editorDoc(d.draft ?? d.document));
  }, [detail.data, doc]);
  const hasDraft = !!detail.data?.has_draft;
  useEffect(() => {
    if (!detail.data) return;
    let gone = false;
    get<DrawnPage>(`/v1/pages/${name}/view${hasDraft ? "?draft=1" : ""}`)
      .then((v) => !gone && setPreview(v))
      .catch(() => !gone && setPreview(null));
    return () => {
      gone = true;
    };
  }, [name, hasDraft, previewKey, detail.data]);
  if (detail.error) return <ErrorBox error={detail.error} />;
  if (!detail.data || !doc || !tables.data || !records.data || !choices.data) return <Skeleton rows={4} label="Opening the page…" />;
  const ch = choices.data;
  const d = detail.data;
  const all = [...tables.data.tables.filter((t) => !t.kept_by), ...recordTables(records.data.records)];
  const change = (next: PageDoc) => {
    setDoc(next);
    setDirty(true);
  };
  const setPart = (i: number, p: Part) => change({ ...doc, blocks: doc.blocks.map((b, j) => (j === i ? p : b)) });
  const problems = d.problems ?? [];
  const save = (then?: () => void) =>
    act.run(async () => {
      await put(`/v1/pages/${name}/draft`, { document: withUses(doc, all) });
      setDirty(false);
      detail.reload();
      setPreviewKey((k) => k + 1);
      then?.();
    }, "Draft saved");
  const pinned = list.data?.pages.find((p) => p.name === name)?.pinned ?? d.pinned;
  return (
    <>
      <p className="crumbs">
        <Link to="/pages" aria-label="Back to Pages">
          Pages
        </Link>
      </p>
      <PageHeader
        title={`Change ${doc.title}`}
        description="Pick what the page shows. People see the published page until you publish your changes."
        actions={
          <>
            <button className="primary" disabled={act.busy || !dirty} onClick={() => void save()}>
              Save draft
            </button>
            {list.data?.can_publish && (
              <button disabled={act.busy || (!dirty && !d.has_draft)} onClick={() => (dirty ? void save(() => setPublishing(true)) : setPublishing(true))}>
                Publish
              </button>
            )}
            {d.published && <Link to={`/pages/${name}`}>Open the page</Link>}
          </>
        }
      />
      <ErrorBox error={act.error} />
      {imported.length > 0 && (
        <div className="notice" role="note" data-testid="page-imported">
          <p>Made from the file as a draft.</p>
          {imported.map((line) => (
            <p key={line}>{line}</p>
          ))}
        </div>
      )}
      <p className="hint" data-testid="page-state">
        {d.published ? (d.has_draft || dirty ? "Published, with changes not published yet." : `Published.`) : "Not published yet: only people who change pages can see it."}
      </p>
      <div className="pg-editor">
        <div className="pg-editor-main">
          <section className="card">
            <h2>About the page</h2>
            <Field label="Name">
              <input value={doc.title} maxLength={60} onChange={(e) => change({ ...doc, title: e.target.value })} />
            </Field>
            <Field label="A line about it (optional)">
              <input value={doc.about ?? ""} maxLength={200} onChange={(e) => change({ ...doc, about: e.target.value || undefined })} />
            </Field>
            <WhoMayOpen roles={doc.roles ?? []} known={roles.data?.roles ?? []} onChange={(r) => change({ ...doc, roles: r.length ? r : undefined })} />
            {d.published && (
              <label className="switch-line">
                <input
                  type="checkbox"
                  checked={pinned}
                  onChange={(e) =>
                    void act.run(async () => {
                      await put(`/v1/pages/${name}/pin`, { pinned: e.target.checked });
                      list.reload();
                      detail.reload();
                    }, e.target.checked ? "Pinned to the menu" : "Unpinned")
                  }
                />{" "}
                <span>Pin to the menu (up to five pages)</span>
              </label>
            )}
            <PlaceForm doc={doc} onChange={change} />
            {(() => {
              const runs = runsWords(d.runs);
              return (
                runs && (
                  <p className={runs.over ? "warning" : "hint"} role="note" data-testid="page-runs">
                    {runs.line}
                    {runs.over && " That is more than your plan allows: fewer tools on the page, or a bigger plan."}
                  </p>
                )
              );
            })()}
          </section>

          <section className="card">
            <h2>What it shows</h2>
            {problems.filter((p) => !p.path.startsWith("/blocks/")).map((p) => (
              <div className="warning" role="note" key={p.path + p.message}>
                {p.message}
              </div>
            ))}
            <ol className="pg-parts">
              {doc.blocks.map((b, i) => {
                const mine = problemsOf(problems, i);
                return (
                  <li key={b.id} className="pg-part-row" data-testid="editor-part">
                    <div className="grow">
                      <strong>{partName(b)}</strong>
                      <span className="sub">{partWords(b, all)}</span>
                      {mine.map((p) => (
                        <span className="sub pg-problem" key={p.path + p.message}>
                          {p.message}
                        </span>
                      ))}
                    </div>
                    <div className="row-actions">
                      <button className="small" onClick={() => setEditing(editing === i ? null : i)} aria-expanded={editing === i}>
                        {editing === i ? "Done" : "Change"}
                        <span className="sr-only"> {partName(b)}</span>
                      </button>
                      <button className="small" disabled={i === 0} onClick={() => change({ ...doc, blocks: move(doc.blocks, i, -1) })}>
                        Move up<span className="sr-only"> {partName(b)}</span>
                      </button>
                      <button className="small" disabled={i === doc.blocks.length - 1} onClick={() => change({ ...doc, blocks: move(doc.blocks, i, 1) })}>
                        Move down<span className="sr-only"> {partName(b)}</span>
                      </button>
                      <button
                        className="small danger"
                        disabled={doc.blocks.length === 1}
                        onClick={() => (setEditing(null), change({ ...doc, blocks: doc.blocks.filter((_, j) => j !== i) }))}
                      >
                        Remove<span className="sr-only"> {partName(b)}</span>
                      </button>
                    </div>
                    {editing === i && <PartForm part={b} tables={all} choices={ch} filters={doc.filters ?? []} onChange={(p) => setPart(i, p)} />}
                  </li>
                );
              })}
            </ol>
            <button disabled={doc.blocks.length >= 12} onClick={() => setAdding(true)}>
              <Icon name="plus" />
              Add a part
            </button>
          </section>

          <FiltersForm doc={doc} tables={all} onChange={change} />

          <Versions
            versions={versions.data?.versions}
            error={versions.error}
            busy={act.busy}
            dirty={dirty || d.has_draft}
            onBringBack={(v) =>
              confirm(
                d.has_draft || dirty
                  ? `Bring back version ${v.version}? It takes the place of your changes not published yet.`
                  : `Bring back version ${v.version}? It becomes your draft: people keep seeing the published page until you publish it.`,
              ) &&
              void act.run(async () => {
                const back = await post<PageDetail>(`/v1/pages/${name}/versions/${v.version}/bring-back`);
                setDoc(editorDoc(back.draft ?? back.document));
                setDirty(false);
                detail.reload();
                setPreviewKey((k) => k + 1);
              }, `Version ${v.version} is your draft now`)
            }
          />

          <section className="card">
            <h2>Save as a file</h2>
            <p className="hint">
              Keep the page as a file, or use it to make the same page in another business. The file has what the page shows and how, never your rows or anyone&apos;s details.
            </p>
            <button
              disabled={act.busy}
              onClick={() =>
                void act.run(async () => {
                  const file = await get<PageDoc>(`/v1/pages/${name}/export`);
                  saveFile(new Blob([JSON.stringify(file, null, 2) + "\n"], { type: "application/json" }), pageFileName(name));
                  const left = conditionCount(d.document ?? d.draft) - conditionCount(file);
                  setExported(
                    left > 0
                      ? `Saved. ${left === 1 ? "A condition on a personal column was" : `${left} conditions on personal columns were`} left out of the file: add ${left === 1 ? "it" : "them"} again where you use it.`
                      : "Saved.",
                  );
                }, false)
              }
            >
              <Icon name="file" />
              Save as a file
            </button>
            {exported && (
              <p className="hint" role="status" data-testid="page-exported">
                {exported}
              </p>
            )}
          </section>

          <section className="card">
            <h2>Delete the page</h2>
            <p className="hint">The page goes for everyone, with its earlier versions. Your tables are not touched.</p>
            <button
              className="danger"
              onClick={() =>
                confirm(`Delete ${doc.title}?`) &&
                void act.run(async () => {
                  await del(`/v1/pages/${name}`);
                  navigate("/pages");
                }, "Page deleted")
              }
            >
              Delete the page
            </button>
          </section>
        </div>
        <aside className="pg-editor-preview" aria-label="How it looks on a phone">
          <h2 className="sr-only">How it looks on a phone</h2>
          <p className="hint">{dirty ? "Save the draft to see your changes here." : "How it looks on a phone"}</p>
          <div className="pg-phone">
            {preview ? <DrawnPageView page={preview} values={{}} onFilter={() => undefined} onRefresh={() => setPreviewKey((k) => k + 1)} preview /> : <Skeleton rows={3} label="Drawing the page…" />}
          </div>
        </aside>
      </div>
      {adding && (
        <AddPart
          tables={all}
          choices={ch}
          onClose={() => setAdding(false)}
          onAdd={(p) => {
            change({ ...doc, blocks: [...doc.blocks, p] });
            setEditing(doc.blocks.length);
            setAdding(false);
          }}
          parts={doc.blocks}
        />
      )}
      {publishing && (
        <Modal title={`Publish ${doc.title}`} onClose={() => setPublishing(false)}>
          {problems.length > 0 ? (
            <>
              <p>Put these right first:</p>
              <ul>
                {problems.map((p) => (
                  <li key={p.path + p.message}>{p.message}</li>
                ))}
              </ul>
              <div className="toolbar">
                <button onClick={() => setPublishing(false)}>Back to the page</button>
              </div>
            </>
          ) : (
            <>
              {publishWords(doc.title, doc.roles ?? [], d.shows ?? []).map((line) => (
                <p key={line}>{line}</p>
              ))}
              <div className="toolbar">
                <button
                  className="primary"
                  disabled={act.busy}
                  onClick={() =>
                    void act.run(async () => {
                      await post(`/v1/pages/${name}/publish`);
                      setPublishing(false);
                      detail.reload();
                      list.reload();
                      setPreviewKey((k) => k + 1);
                    }, "Published")
                  }
                >
                  Publish
                </button>
                <button onClick={() => setPublishing(false)}>Not yet</button>
              </div>
            </>
          )}
        </Modal>
      )}
    </>
  );
}

function WhoMayOpen({ roles, known, onChange }: { roles: string[]; known: ApproverRole[]; onChange: (r: string[]) => void }) {
  const shown = [...known.filter((r) => r.name !== "owner"), ...roles.filter((r) => !known.some((k) => k.name === r)).map((name) => ({ name, builtin: false, holders: 0 }))];
  const [typed, setTyped] = useState("");
  return (
    <fieldset className="sf-choices" data-testid="page-roles">
      <legend>Who may open it</legend>
      <p className="hint">{roles.length === 0 ? "Everyone in the business." : `${whoWords(roles)}.`} It shows them what you choose here, whatever else they may see.</p>
      {shown.map((r) => (
        <label key={r.name} className="row">
          <input type="checkbox" checked={roles.includes(r.name)} onChange={(e) => onChange(e.target.checked ? [...roles, r.name] : roles.filter((x) => x !== r.name))} />
          <span>{r.name.replace(/_/g, " ")}</span>
        </label>
      ))}
      <span className="toolbar">
        <input aria-label="Another role" placeholder="Another role, like support" value={typed} onChange={(e) => setTyped(e.target.value)} />
        <button
          type="button"
          disabled={!typed.trim()}
          onClick={() => {
            const n = typed
              .trim()
              .toLowerCase()
              .replace(/[^a-z0-9]+/g, "_")
              .replace(/^_+|_+$/g, "");
            if (/^[a-z]/.test(n) && !roles.includes(n)) onChange([...roles, n]);
            setTyped("");
          }}
        >
          Add
        </button>
      </span>
    </fieldset>
  );
}

function AddPart({ tables, parts, choices, onClose, onAdd }: { tables: Table[]; parts: Part[]; choices: PageChoices; onClose: () => void; onAdd: (p: Part) => void }) {
  const [kind, setKind] = useState("count");
  const [table, setTable] = useState(tables[0]?.name ?? "");
  // A board and a chart read the business's own tables, never Taskiem's records.
  const own = tables.filter((t) => !isRecords(t.name));
  const offered = OWN_TABLE_KINDS.includes(kind) ? own : tables;
  const picked = offered.find((t) => t.name === table) ?? offered[0];
  // What a kind needs that the business may not have yet, in words.
  const lacks = (k: string): string => {
    if (k === "tool-figure" && feedingTools(choices, "figure").length === 0) return "None of your tools only looks up a figure yet";
    if ((k === "tool-list" || k === "tool-chart" || k === "tool-board") &&feedingTools(choices, "list").length === 0) return "None of your tools only looks up rows yet";
    if (OWN_TABLE_KINDS.includes(k) && own.length === 0) return "No table of your own yet";
    if (k === "status" && choices.workflows.length === 0) return "No workflow is in use yet";
    if (k === "saved-list" && !(choices.saved_kinds ?? []).length) return "No kind of saved things made yet";
    if (k === "saved-list") return "";
    if (k === "buttons" && choices.tools.length === 0 && !choices.workflows.some((w) => w.signals.length || w.approvals.length)) return "No tool or workflow to press yet";
    if (k === "status" || k === "buttons" || k.startsWith("tool-")) return "";
    return k !== "heading" && tables.length === 0 ? "No table yet" : "";
  };
  return (
    <Modal title="Add a part" onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          onAdd(newPart(kind, NO_TABLE_KINDS.includes(kind) ? undefined : picked, parts, choices));
        }}
      >
        <fieldset className="sf-choices">
          <legend>What kind?</legend>
          {PART_KINDS.map((k) => (
            <label key={k.kind} className="row">
              <input type="radio" name="kind" checked={kind === k.kind} disabled={!!lacks(k.kind)} onChange={() => setKind(k.kind)} />
              <span>
                {k.words} <span className="hint">· {lacks(k.kind) || k.hint}</span>
              </span>
            </label>
          ))}
        </fieldset>
        {!NO_TABLE_KINDS.includes(kind) && (
          <Field label="From which table, or Taskiem's orders or invoices?">
            <select value={picked?.name ?? ""} onChange={(e) => setTable(e.target.value)}>
              {offered.map((t) => (
                <option key={t.name} value={t.name}>
                  {t.title || t.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        <div className="toolbar">
          <button className="primary">Add</button>
          <button type="button" onClick={onClose}>
            Cancel
          </button>
        </div>
      </form>
    </Modal>
  );
}

/** A column picker over a table's columns. */
function ColumnPick({ label, value, cols, onChange, none }: { label: string; value: string; cols: { name: string; words: string }[]; onChange: (c: string) => void; none?: string }) {
  return (
    <Field label={label}>
      <select value={value} onChange={(e) => onChange(e.target.value)}>
        {none !== undefined && <option value="">{none}</option>}
        {cols.map((c) => (
          <option key={c.name} value={c.name}>
            {c.words}
          </option>
        ))}
      </select>
    </Field>
  );
}

/** One part's choices, in plain words. */
function PartForm({ part, tables, choices, filters, onChange }: { part: Part; tables: Table[]; choices: PageChoices; filters: PageFilterDef[]; onChange: (p: Part) => void }) {
  const t = tables.find((x) => x.name === part.source?.table);
  const set = (p: Partial<Part>) => onChange({ ...part, ...p });
  if (part.type === "buttons") {
    return (
      <div className="pg-part-form">
        <Field label="Title (optional)">
          <input value={part.title ?? ""} maxLength={60} onChange={(e) => set({ title: e.target.value || undefined })} />
        </Field>
        <ActionsForm actions={part.actions ?? []} row={undefined} filters={filters} choices={choices} onChange={(actions) => set({ actions })} />
      </div>
    );
  }
  if (part.type === "status") return <StatusForm part={part} choices={choices} onChange={onChange} />;
  if (part.source?.saved !== undefined) return <SavedSourceForm part={part} kinds={choices.saved_kinds ?? []} onChange={onChange} />;
  if (part.source?.tool !== undefined && part.type === "figure") {
    return (
      <div className="pg-part-form">
        <Field label="Label">
          <input value={part.label ?? ""} maxLength={40} onChange={(e) => set({ label: e.target.value })} />
        </Field>
        <ToolSourceForm part={part} choices={choices} filters={filters} onChange={onChange} />
        <label className="switch-line">
          <input type="checkbox" checked={!!part.naira} onChange={(e) => set({ naira: e.target.checked || undefined })} /> <span>It is money (shown in naira)</span>
        </label>
        <Field label="Colour">
          <select value={part.tone ?? "plain"} onChange={(e) => set({ tone: e.target.value === "plain" ? undefined : (e.target.value as Tone) })}>
            {TONES.map((x) => (
              <option key={x.tone} value={x.tone}>
                {x.words}
              </option>
            ))}
          </select>
        </Field>
        <label className="switch-line">
          <input type="checkbox" checked={!!part.lead} onChange={(e) => set({ lead: e.target.checked || undefined })} /> <span>The page's main figure, drawn large</span>
        </label>
      </div>
    );
  }
  if (part.source?.tool !== undefined && part.type === "list") {
    const item = part.item ?? { title: "" };
    const field = (v: string | undefined) => /^=item\.([a-zA-Z_][a-zA-Z0-9_]*)$/.exec(v ?? "")?.[1];
    return (
      <div className="pg-part-form">
        <Field label="Title">
          <input value={part.title ?? ""} maxLength={60} onChange={(e) => set({ title: e.target.value || undefined })} />
        </Field>
        <ToolSourceForm part={part} choices={choices} filters={filters} onChange={onChange} />
        {field(item.title) !== undefined || !item.title ? (
          <Field label="Each row's title is the answer's" hint="The name it has in the tool's answer, like name">
            <input value={field(item.title) ?? ""} maxLength={40} onChange={(e) => set({ item: { ...item, title: e.target.value ? `=item.${e.target.value.replace(/[^a-zA-Z0-9_]/g, "")}` : "" } })} />
          </Field>
        ) : (
          <p className="hint">Each row's title is as the tool's own answer says it.</p>
        )}
        {field(item.sub) !== undefined || !item.sub ? (
          <Field label="The line under it is the answer's (optional)">
            <input value={field(item.sub) ?? ""} maxLength={40} onChange={(e) => set({ item: { ...item, sub: e.target.value ? `=item.${e.target.value.replace(/[^a-zA-Z0-9_]/g, "")}` : undefined } })} />
          </Field>
        ) : (
          <p className="hint">The line under it is as the tool's own answer says it.</p>
        )}
        <Field label="When there are none, say">
          <input value={part.empty ?? ""} maxLength={120} placeholder="Nothing to show" onChange={(e) => set({ empty: e.target.value || undefined })} />
        </Field>
      </div>
    );
  }
  if (part.type === "heading") {
    return (
      <div className="pg-part-form">
        <Field label="Words">
          <input value={part.title ?? ""} maxLength={80} onChange={(e) => set({ title: e.target.value })} />
        </Field>
        <Field label="A line under it (optional)">
          <input value={part.text ?? ""} maxLength={300} onChange={(e) => set({ text: e.target.value || undefined })} />
        </Field>
      </div>
    );
  }
  const any = columnsFor(t, "any");
  const tablePick = (
    <Field label="From which table?">
      <select value={part.source?.table ?? ""} onChange={(e) => set({ source: { table: e.target.value } })}>
        {tables.map((x) => (
          <option key={x.name} value={x.name}>
            {x.title || x.name}
          </option>
        ))}
      </select>
    </Field>
  );
  const conditions = <Conditions part={part} table={t} onChange={onChange} />;
  // A board and a chart (G4): PageEditorG4.tsx.
  if (part.type === "board") return <BoardForm part={part} tables={tables} choices={choices} filters={filters} conditions={conditions} onChange={onChange} />;
  if (part.type === "chart") return <ChartForm part={part} tables={tables} choices={choices} filters={filters} conditions={part.source?.tool ? null : conditions} onChange={onChange} />;
  if (part.type === "figure") {
    return (
      <div className="pg-part-form">
        <Field label="Label">
          <input value={part.label ?? ""} maxLength={40} onChange={(e) => set({ label: e.target.value })} />
        </Field>
        {tablePick}
        <Field label="Shows">
          <select value={part.value === "sum" ? `sum:${part.of ?? ""}` : "count"} onChange={(e) => (e.target.value === "count" ? set({ value: "count", of: undefined, naira: undefined }) : set({ value: "sum", of: e.target.value.slice(4) }))}>
            <option value="count">How many rows</option>
            {columnsFor(t, "sum").map((c) => (
              <option key={c.name} value={`sum:${c.name}`}>
                {`${c.words} added up`}
              </option>
            ))}
          </select>
        </Field>
        {part.value === "sum" && (
          <label className="switch-line">
            <input type="checkbox" checked={!!part.naira} onChange={(e) => set({ naira: e.target.checked || undefined })} /> <span>It is money (shown in naira)</span>
          </label>
        )}
        <Field label="Only this period, beside the one before">
          <select
            value={part.change ? `${part.change.period}:${part.change.column}` : ""}
            onChange={(e) => {
              const [period, column] = e.target.value.split(":");
              set({ change: period && column ? { period: period as "day" | "week" | "month", column } : undefined });
            }}
          >
            <option value="">All the rows</option>
            {columnsFor(t, "date").flatMap((c) =>
              (["day", "week", "month"] as const).map((p) => (
                <option key={`${p}:${c.name}`} value={`${p}:${c.name}`}>
                  {`This ${p} by ${c.words.toLowerCase()}`}
                </option>
              )),
            )}
          </select>
        </Field>
        <Field label="Colour">
          <select value={part.tone ?? "plain"} onChange={(e) => set({ tone: e.target.value === "plain" ? undefined : (e.target.value as Tone) })}>
            {TONES.map((x) => (
              <option key={x.tone} value={x.tone}>
                {x.words}
              </option>
            ))}
          </select>
        </Field>
        <label className="switch-line">
          <input type="checkbox" checked={!!part.lead} onChange={(e) => set({ lead: e.target.checked || undefined })} /> <span>The page's main figure, drawn large</span>
        </label>
        {part.value === "count" && (
          <label className="switch-line">
            <input type="checkbox" checked={!!part.alert} onChange={(e) => set({ alert: e.target.checked || undefined })} />{" "}
            <span>An alert while it is above nought: it shows in the chat's alerts, and opens the page</span>
          </label>
        )}
        {conditions}
      </div>
    );
  }
  const rowExtras = (
    <>
      <ActionsForm actions={part.actions ?? []} row={t} filters={filters} choices={choices} onChange={(actions) => set({ actions: actions.length ? actions : undefined })} />
      {choices.workflows.length > 0 && <RowStatusForm part={part} table={t} choices={choices} onChange={onChange} />}
    </>
  );
  const detailCols = (part.detail?.lines ?? []).map((l) => columnOf(l.value)).filter(Boolean);
  const detail = (
    <fieldset className="sf-choices">
      <legend>Opening a row shows</legend>
      {any.map((c) => (
        <label key={c.name} className="row">
          <input
            type="checkbox"
            checked={detailCols.includes(c.name)}
            disabled={!detailCols.includes(c.name) && detailCols.length >= 12}
            onChange={(e) => {
              const lines = (part.detail?.lines ?? []).filter((l) => columnOf(l.value) !== c.name);
              if (e.target.checked) lines.push({ label: c.words, value: showColumn(c.name) });
              set({ detail: lines.length || part.detail ? { title: part.detail?.title || part.item?.title || showColumn(any[0]?.name ?? ""), lines } : undefined });
            }}
          />
          <span>{c.words}</span>
        </label>
      ))}
    </fieldset>
  );
  if (part.type === "list") {
    const item = part.item ?? { title: "" };
    const order = part.source?.order?.[0];
    return (
      <div className="pg-part-form">
        <Field label="Title">
          <input value={part.title ?? ""} maxLength={60} onChange={(e) => set({ title: e.target.value || undefined })} />
        </Field>
        {tablePick}
        <TitlePick part={part} cols={any} onChange={onChange} />
        <ColumnPick label="The line under it" value={columnOf(item.sub)} cols={any} none="Nothing" onChange={(c) => set({ item: { ...item, sub: c ? showColumn(c) : undefined } })} />
        <ColumnPick
          label="Its state, at the end"
          value={columnOf(item.state?.text)}
          cols={columnsFor(t, "group")}
          none="Nothing"
          onChange={(c) => set({ item: { ...item, state: c ? { ...(item.state ?? {}), text: showColumn(c) } : undefined } })}
        />
        <ColumnPick label="In groups by" value={part.group?.column ?? ""} cols={columnsFor(t, "group")} none="No groups" onChange={(c) => set({ group: c ? { column: c } : undefined })} />
        <ColumnPick
          label="In order of"
          value={order ? `${order.column}:${order.desc ? "1" : ""}` : ""}
          cols={columnsFor(t, "pick").flatMap((c) => [
            { name: `${c.name}:1`, words: `${c.words}, newest or largest first` },
            { name: `${c.name}:`, words: `${c.words}, oldest or smallest first` },
          ])}
          none="Newest first"
          onChange={(v) => {
            const [column, desc] = v.split(":");
            set({ source: { table: part.source?.table ?? "", ...part.source, order: column ? [{ column, desc: desc === "1" || undefined }] : undefined } });
          }}
        />
        <Field label="When there are none, say">
          <input value={part.empty ?? ""} maxLength={120} placeholder="Nothing to show" onChange={(e) => set({ empty: e.target.value || undefined })} />
        </Field>
        {detail}
        {conditions}
        {rowExtras}
      </div>
    );
  }
  // A table.
  const shown = (part.columns ?? []).map((c) => c.column);
  return (
    <div className="pg-part-form">
      <Field label="Title">
        <input value={part.title ?? ""} maxLength={60} onChange={(e) => set({ title: e.target.value || undefined })} />
      </Field>
      {tablePick}
      <fieldset className="sf-choices">
        <legend>Columns it shows (up to six)</legend>
        {any.map((c) => (
          <label key={c.name} className="row">
            <input
              type="checkbox"
              checked={shown.includes(c.name)}
              disabled={!shown.includes(c.name) && shown.length >= 6}
              onChange={(e) =>
                set({ columns: e.target.checked ? [...(part.columns ?? []), { column: c.name }] : (part.columns ?? []).filter((x) => x.column !== c.name) })
              }
            />
            <span>{c.words}</span>
          </label>
        ))}
      </fieldset>
      {(part.columns ?? [])
        .filter((c) => columnsFor(t, "number").some((n) => n.name === c.column))
        .map((c) => (
          <label key={c.column} className="switch-line">
            <input
              type="checkbox"
              checked={!!c.naira}
              onChange={(e) => set({ columns: (part.columns ?? []).map((x) => (x.column === c.column ? { ...x, naira: e.target.checked || undefined } : x)) })}
            />{" "}
            <span>{`${columnWords(t, c.column)} is money (shown in naira)`}</span>
          </label>
        ))}
      {/* A yes/no column of the business's own table may be ticked in place, by those who may change its rows. */}
      {!isRecords(t?.name) &&
        (part.columns ?? [])
          .filter((c) => t?.columns.some((x) => x.name === c.column && x.type === "boolean" && !x.personal))
          .map((c) => (
            <label key={`edit-${c.column}`} className="switch-line">
              <input
                type="checkbox"
                checked={!!c.edit}
                onChange={(e) => set({ columns: (part.columns ?? []).map((x) => (x.column === c.column ? { ...x, edit: e.target.checked || undefined } : x)) })}
              />{" "}
              <span>{`${columnWords(t, c.column)} can be ticked here, by those who may change the table's rows`}</span>
            </label>
          ))}
      {detail}
      {conditions}
      {rowExtras}
    </div>
  );
}

/** "Only rows where": up to five conditions, each a column, how and a value. */
function Conditions({ part, table, onChange }: { part: Part; table: Table | undefined; onChange: (p: Part) => void }) {
  const list = part.source?.filters ?? [];
  const cols = columnsFor(table, "pick");
  const setList = (filters: typeof list) => onChange({ ...part, source: { table: part.source?.table ?? "", ...part.source, filters: filters.length ? filters : undefined } });
  return (
    <fieldset className="sf-choices pg-conditions">
      <legend>Only rows where</legend>
      {list.length === 0 && <p className="hint">Every row.</p>}
      {list.map((c, i) => {
        const col = table?.columns.find((x) => x.name === c.column);
        const ops = opsFor(col);
        const needsValue = c.op !== "is_null" && c.op !== "not_null";
        return (
          <div className="toolbar" key={i}>
            <select aria-label={`Condition ${i + 1}: column`} value={c.column} onChange={(e) => setList(list.map((x, j) => (j === i ? { column: e.target.value, op: "eq", value: "" } : x)))}>
              {cols.map((x) => (
                <option key={x.name} value={x.name}>
                  {x.words}
                </option>
              ))}
            </select>
            <select aria-label={`Condition ${i + 1}: how`} value={c.op} onChange={(e) => setList(list.map((x, j) => (j === i ? { ...x, op: e.target.value } : x)))}>
              {ops.map((o) => (
                <option key={o.op} value={o.op}>
                  {o.words}
                </option>
              ))}
            </select>
            {needsValue &&
              (col?.type === "boolean" ? (
                <select aria-label={`Condition ${i + 1}: value`} value={String(c.value ?? true)} onChange={(e) => setList(list.map((x, j) => (j === i ? { ...x, value: e.target.value === "true" } : x)))}>
                  <option value="true">Yes</option>
                  <option value="false">No</option>
                </select>
              ) : (
                <input
                  aria-label={`Condition ${i + 1}: value`}
                  value={String(c.value ?? "")}
                  type={col?.type === "date" ? "date" : "text"}
                  onChange={(e) => {
                    const raw = e.target.value;
                    const v = (col?.type === "integer" || col?.type === "number") && raw !== "" && !isNaN(Number(raw)) ? Number(raw) : raw;
                    setList(list.map((x, j) => (j === i ? { ...x, value: v } : x)));
                  }}
                />
              ))}
            <button type="button" className="small" onClick={() => setList(list.filter((_, j) => j !== i))}>
              Remove<span className="sr-only"> condition {i + 1}</span>
            </button>
          </div>
        );
      })}
      {list.length < 5 && cols.length > 0 && (
        <button type="button" className="small" onClick={() => setList([...list, { column: cols[0]?.name ?? "", op: "eq", value: "" }])}>
          Add a condition
        </button>
      )}
    </fieldset>
  );
}

/** The page's filters: a search, a choice, dates, Mine. */
function FiltersForm({ doc, tables, onChange }: { doc: PageDoc; tables: Table[]; onChange: (d: PageDoc) => void }) {
  const filters = doc.filters ?? [];
  const used = tables.filter((t) => doc.blocks.some((b) => b.source?.table === t.name));
  const cols = (what: "group" | "date" | "mine") => {
    const seen = new Map<string, string>();
    for (const t of used) for (const c of columnsFor(t, what)) if (!seen.has(c.name)) seen.set(c.name, c.words);
    return [...seen].map(([name, words]) => ({ name, words }));
  };
  const setList = (f: PageFilterDef[]) => onChange({ ...doc, filters: f.length ? f : undefined });
  const add = (type: PageFilterDef["type"]) => {
    const id = freeId(filters, type === "search" ? "q" : type);
    const f: PageFilterDef = { id, type };
    if (type === "choice") Object.assign(f, { column: cols("group")[0]?.name ?? "", options: [] as { value: string }[], label: "Show" });
    if (type === "dates") Object.assign(f, { column: cols("date")[0]?.name ?? "created_at", label: "When" });
    if (type === "mine") Object.assign(f, { column: cols("mine")[0]?.name ?? "", label: "Mine" });
    if (type === "search") f.label = "Search";
    setList([...filters, f]);
  };
  const setOne = (i: number, f: PageFilterDef) => setList(filters.map((x, j) => (j === i ? f : x)));
  const kinds: { type: PageFilterDef["type"]; words: string; ok: boolean }[] = [
    { type: "search", words: "A search", ok: !filters.some((f) => f.type === "search") },
    { type: "choice", words: "A choice", ok: cols("group").length > 0 },
    { type: "dates", words: "Dates", ok: !filters.some((f) => f.type === "dates") },
    { type: "mine", words: "Mine", ok: cols("mine").length > 0 && !filters.some((f) => f.type === "mine") },
  ];
  return (
    <section className="card">
      <h2>Filters at the top</h2>
      <p className="hint">They narrow the lists and tables. A person's last choice is kept on their phone.</p>
      {filters.map((f, i) => (
        <div className="pg-filter-row" key={f.id} data-testid="editor-filter">
          <strong>{f.label || f.type}</strong>
          {f.type === "choice" && (
            <>
              <ColumnPick label="Picks from" value={f.column ?? ""} cols={cols("group")} onChange={(c) => setOne(i, { ...f, column: c })} />
              <Field label="The choices, with commas between">
                <input
                  value={(f.options ?? []).map((o) => o.value).join(", ")}
                  onChange={(e) =>
                    setOne(i, {
                      ...f,
                      options: e.target.value
                        .split(",")
                        .map((v) => v.trim())
                        .filter(Boolean)
                        .slice(0, 20)
                        .map((value) => ({ value })),
                    })
                  }
                />
              </Field>
            </>
          )}
          {f.type === "dates" && <ColumnPick label="By" value={f.column ?? ""} cols={cols("date").length ? cols("date") : [{ name: "created_at", words: "When added" }]} onChange={(c) => setOne(i, { ...f, column: c })} />}
          {f.type === "mine" && <ColumnPick label="Rows whose email or phone number is the person's own, in" value={f.column ?? ""} cols={cols("mine")} onChange={(c) => setOne(i, { ...f, column: c })} />}
          <button type="button" className="small danger" onClick={() => setList(filters.filter((_, j) => j !== i))}>
            Remove<span className="sr-only"> {f.label || f.type}</span>
          </button>
        </div>
      ))}
      {filters.length < 4 && (
        <div className="toolbar">
          {kinds
            .filter((k) => k.ok)
            .map((k) => (
              <button type="button" className="small" key={k.type} onClick={() => add(k.type)}>
                <Icon name="plus" />
                {k.words}
              </button>
            ))}
        </div>
      )}
    </section>
  );
}

/** The versions people have seen, newest first, each with who published
 * it, when and what it changed; Bring back makes one the draft. */
function Versions({
  versions,
  error,
  busy,
  dirty,
  onBringBack,
}: {
  versions: PageVersion[] | undefined;
  error: unknown;
  busy: boolean;
  dirty: boolean;
  onBringBack: (v: PageVersion) => void;
}) {
  return (
    <section className="card" data-testid="page-versions">
      <h2>Earlier versions</h2>
      <p className="hint">Each time the page was published. Bringing one back makes it your draft: people see it only once you publish it again.</p>
      {error ? (
        <ErrorBox error={error} />
      ) : !versions ? (
        <Skeleton rows={2} label="Loading the versions…" />
      ) : versions.length === 0 ? (
        <p className="hint">Not published yet.</p>
      ) : (
        <Items label="Earlier versions">
          {versions.map((v) => (
            <Row
              key={v.version}
              icon="clock"
              tone={v.current ? "ok" : undefined}
              title={versionTitle(v)}
              sub={
                <span className="sub">
                  {`${v.published_by}, ${fmtTime(v.published_at)}. ${v.summary}`}
                </span>
              }
              testid="page-version"
            >
              {(!v.current || dirty) && (
                <button type="button" className="small" disabled={busy} onClick={() => onBringBack(v)}>
                  Bring back<span className="sr-only"> version {v.version}</span>
                </button>
              )}
            </Row>
          ))}
        </Items>
      )}
    </section>
  );
}
